package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunRequiresLocalQueue(t *testing.T) {
	defer func() {
		if got := recover(); got != "worker: local queue must be initialized before Run" {
			t.Fatalf("unexpected assertion: %v", got)
		}
	}()
	_ = (&Worker{}).Run(context.Background())
}

func TestControllerPriorityAndNonPreemptiveLocalJobs(t *testing.T) {
	dir := t.TempDir()
	q, err := openLocalQueue(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	q.submit([]string{"sh", "-c", `echo local-first >> "$1/order"; touch "$1/local-started"; sleep 0.05; touch "$1/local-finished"`, "sh", dir})
	q.submit([]string{"sh", "-c", `test -f "$1/remote-finished" && echo local-second >> "$1/order"`, "sh", dir})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var claims atomic.Int32
	var remoteReady atomic.Bool
	var remoteSent atomic.Bool
	var beatsDuringLocal atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/claim"):
			if claims.Add(1) == 1 {
				json.NewEncoder(w).Encode(map[string]any{"job": Job{ID: "remote-first", Command: []string{"sh", "-c", `echo remote-first >> "$1"`, "sh", filepath.Join(dir, "order")}}})
				return
			}
			if remoteReady.Load() && !remoteSent.Swap(true) {
				if _, err := os.Stat(filepath.Join(dir, "local-finished")); err != nil {
					t.Error("controller claim preempted local job")
				}
				json.NewEncoder(w).Encode(map[string]any{"job": Job{ID: "remote-second", Command: []string{"sh", "-c", `echo remote-second >> "$1/order"; touch "$1/remote-finished"`, "sh", dir}}})
				return
			}
			last, _ := q.get("local-2")
			if last.FinishedAt != nil {
				cancel()
			}
			fmt.Fprint(w, `{"job":null}`)
		case strings.HasSuffix(r.URL.Path, "/heartbeat"):
			if _, err := os.Stat(filepath.Join(dir, "local-started")); err == nil {
				if _, err := os.Stat(filepath.Join(dir, "local-finished")); os.IsNotExist(err) {
					beatsDuringLocal.Add(1)
				}
				remoteReady.Store(true)
			}
			fmt.Fprint(w, `{}`)
		default:
			if strings.Contains(r.URL.Path, "/jobs/local-") {
				t.Error("local job leaked to controller")
			}
			fmt.Fprint(w, `{}`)
		}
	})
	worker := testWorker(t, client)
	worker.localQueue = q
	err = worker.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run: %v", err)
	}
	jobs := queueJobs(t, q)
	for _, job := range jobs {
		if job.Status != "succeeded" || job.OutputPath == "" {
			t.Fatalf("local result: %+v", job)
		}
		os.Remove(job.OutputPath)
	}
	order, err := os.ReadFile(filepath.Join(dir, "order"))
	if err != nil || string(order) != "remote-first\nlocal-first\nremote-second\nlocal-second\n" {
		t.Fatalf("order: %q %v", order, err)
	}
	if beatsDuringLocal.Load() == 0 {
		t.Fatal("heartbeats stopped during local execution")
	}
}

func TestLocalShutdownPersistsFailure(t *testing.T) {
	q := testLocalQueue(t)
	q.submit([]string{"sleep", "60"})
	job, _ := q.claim()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	worker := &Worker{localQueue: q, processGrace: time.Millisecond, shutdownTimeout: time.Second}
	if err := worker.runJob(ctx, *job, q); err != nil {
		t.Fatal(err)
	}
	jobs := queueJobs(t, q)
	if jobs[0].Status != "failed" || jobs[0].Error == "" {
		t.Fatalf("result: %+v", jobs[0])
	}
}

func TestLocalRunsAfterFirstSuccessfulEmptyClaim(t *testing.T) {
	q := testLocalQueue(t)
	q.submit([]string{"true"})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var claims atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/claim") {
			job, _ := q.get("local-1")
			switch claims.Add(1) {
			case 1:
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			case 2:
				if job.Status != "queued" {
					t.Errorf("local job ran before successful empty claim: %s", job.Status)
				}
			default:
				if job.Status != "succeeded" {
					t.Errorf("local job did not run after first empty claim: %s", job.Status)
				}
				cancel()
			}
			fmt.Fprint(w, `{"job":null}`)
			return
		}
		fmt.Fprint(w, `{}`)
	})
	worker := testWorker(t, client)
	worker.localQueue = q
	if err := worker.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("run: %v", err)
	}
	job, _ := q.get("local-1")
	if job.OutputPath != "" {
		os.Remove(job.OutputPath)
	}
}

func TestClaimRetriesUntilSuccessfulEmptyResponse(t *testing.T) {
	var calls atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, `{"job":null}`)
	})
	job, err := client.Claim(context.Background())
	if err != nil || job != nil || calls.Load() != 2 {
		t.Fatalf("job=%v calls=%v err=%v", job, calls.Load(), err)
	}
}
