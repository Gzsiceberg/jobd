package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestLocalJobBatches(t *testing.T) {
	for _, action := range []string{"urgent", "retry"} {
		t.Run(action, func(t *testing.T) {
			q := testLocalQueue(t)
			a, _ := q.submit([]string{"a"})
			b, _ := q.submit([]string{"b"})
			if action == "retry" {
				for range 2 {
					job, err := q.claim()
					if err != nil || job == nil {
						t.Fatalf("claim: %v %v", job, err)
					}
					code := 1
					if err := q.Finish(context.Background(), job.ID, Result{ExitCode: &code, Error: "failed"}); err != nil {
						t.Fatal(err)
					}
				}
			}
			c, _ := q.submit([]string{"c"})
			invalid := "local-999"
			if action == "urgent" {
				job, err := q.claim()
				if err != nil || job == nil {
					t.Fatalf("claim: %v %v", job, err)
				}
				invalid = job.ID
				// The first job is now running; use c and b as valid queued targets.
				a = b
				b = c
			} else {
				invalid = c.ID
			}
			ids := []string{b.ID, invalid, "local-999", a.ID, b.ID}
			body, _ := json.Marshal(map[string]any{"ids": ids})
			response := httptest.NewRecorder()
			q.routes().ServeHTTP(response, httptest.NewRequest("POST", "/jobs/"+action, strings.NewReader(string(body))))
			if response.Code != 200 {
				t.Fatalf("response: %d %s", response.Code, response.Body.String())
			}
			var result batchResult
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result.Succeeded, []string{b.ID, a.ID}) || len(result.Failed) != 2 || result.Failed[0].ID != invalid || result.Failed[1].ID != "local-999" {
				t.Fatalf("result: %+v", result)
			}
			var queued []string
			for _, job := range queueJobs(t, q) {
				if job.Status == "queued" {
					queued = append(queued, job.ID)
				}
			}
			want := []string{b.ID, a.ID}
			if action == "retry" {
				want = append([]string{c.ID}, want...)
			}
			if !reflect.DeepEqual(queued, want) {
				t.Fatalf("queue: %v want %v", queued, want)
			}
		})
	}
}

func TestLocalBatchValidation(t *testing.T) {
	for _, action := range []string{"urgent", "retry", "remove"} {
		q := testLocalQueue(t)
		job, err := q.submit([]string{"job"})
		if err != nil {
			t.Fatal(err)
		}
		if action == "retry" {
			if _, err := q.claim(); err != nil {
				t.Fatal(err)
			}
			code := 1
			if err := q.Finish(context.Background(), job.ID, Result{ExitCode: &code, Error: "failed"}); err != nil {
				t.Fatal(err)
			}
		}
		before := queueJobs(t, q)
		ids := make([]string, 101)
		for i := range ids {
			ids[i] = job.ID
		}
		oversized, _ := json.Marshal(map[string]any{"ids": ids})
		tooLong, _ := json.Marshal(map[string]any{"ids": []string{job.ID, strings.Repeat("x", 4097)}})
		for _, body := range []string{`{}`, `{"ids":[]}`, `{"ids":[""]}`, `{"ids":[1]}`, `{"ids":["local-1"],"unknown":true}`, `{"ids":["local-1",""]}`, `{"ids":["local-1","   "]}`, `{"ids":["local-1",1]}`, string(oversized), string(tooLong)} {
			response := httptest.NewRecorder()
			q.routes().ServeHTTP(response, httptest.NewRequest("POST", "/jobs/"+action, strings.NewReader(body)))
			if response.Code != 400 {
				t.Fatalf("%s: %d %s", body, response.Code, response.Body.String())
			}
			if after := queueJobs(t, q); !reflect.DeepEqual(after, before) {
				t.Fatalf("malformed batch mutated jobs: before=%+v after=%+v", before, after)
			}
		}
		body, _ := json.Marshal(map[string]any{"ids": ids[:100]})
		response := httptest.NewRecorder()
		q.routes().ServeHTTP(response, httptest.NewRequest("POST", "/jobs/"+action, strings.NewReader(string(body))))
		var result batchResult
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != 200 || !reflect.DeepEqual(result.Succeeded, []string{job.ID}) || len(result.Failed) != 0 {
			t.Fatalf("maximum batch: %d %s (%v)", response.Code, response.Body.String(), err)
		}
	}
}

func TestLocalBatchAllTargetsFailed(t *testing.T) {
	for _, action := range []string{"urgent", "retry", "remove"} {
		q := testLocalQueue(t)
		response := httptest.NewRecorder()
		q.routes().ServeHTTP(response, httptest.NewRequest("POST", "/jobs/"+action, strings.NewReader(`{"ids":["local-999","local-998","local-999"]}`)))
		var result batchResult
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != 200 {
			t.Fatalf("response: %d %s (%v)", response.Code, response.Body.String(), err)
		}
		if result.Succeeded == nil || len(result.Succeeded) != 0 || len(result.Failed) != 2 {
			t.Fatalf("result: %+v", result)
		}
		for i, id := range []string{"local-999", "local-998"} {
			if result.Failed[i].ID != id || result.Failed[i].Error == "" {
				t.Fatalf("failure: %+v", result.Failed[i])
			}
		}
	}
}
