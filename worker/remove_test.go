package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestBatchRemove(t *testing.T) {
	q := testLocalQueue(t)
	finished, _ := q.submit([]string{"finished"})
	q.claim()
	zero := 0
	if err := q.Finish(context.Background(), finished.ID, Result{ExitCode: &zero}); err != nil {
		t.Fatal(err)
	}
	running, _ := q.submit([]string{"running"})
	q.claim()
	if err := q.requestCancel(running.ID); err != nil {
		t.Fatal(err)
	}
	queued, _ := q.submit([]string{"queued"})
	remove := func(ids []string) batchResult {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"ids": ids})
		response := httptest.NewRecorder()
		q.routes().ServeHTTP(response, httptest.NewRequest("POST", "/jobs/remove", strings.NewReader(string(body))))
		var result batchResult
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != 200 {
			t.Fatalf("response: %d %s (%v)", response.Code, response.Body.String(), err)
		}
		return result
	}
	result := remove([]string{finished.ID, running.ID, "missing", queued.ID, finished.ID})
	if !reflect.DeepEqual(result.Succeeded, []string{finished.ID, queued.ID}) || len(result.Failed) != 2 || result.Failed[0].ID != running.ID || result.Failed[1].ID != "missing" {
		t.Fatalf("result: %+v", result)
	}
	jobs := queueJobs(t, q)
	if len(jobs) != 1 || jobs[0].ID != running.ID || jobs[0].Status != "running" || jobs[0].CancelRequested != 1 {
		t.Fatalf("jobs: %+v", jobs)
	}
	if err := q.Finish(context.Background(), running.ID, Result{ExitCode: &zero}); err != nil {
		t.Fatal(err)
	}
	result = remove([]string{running.ID})
	if len(result.Succeeded) != 1 || len(result.Failed) != 0 {
		t.Fatalf("result: %+v", result)
	}
	next, err := q.submit([]string{"next"})
	if err != nil || next.ID != "local-4" {
		t.Fatalf("ID reset: %+v %v", next, err)
	}
}

func TestRemovePreservesIDsAfterEmptyQueueAndRestart(t *testing.T) {
	dir := t.TempDir()
	q, err := openLocalQueue(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		job, err := q.submit([]string{"true"})
		if err != nil {
			q.Close()
			t.Fatal(err)
		}
		if err := q.remove(job.ID); err != nil {
			q.Close()
			t.Fatal(err)
		}
	}
	q.Close()
	q, err = openLocalQueue(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	next, err := q.submit([]string{"true"})
	if err != nil || next.ID != "local-4" {
		t.Fatalf("ID reset: %+v, %v", next, err)
	}
}
