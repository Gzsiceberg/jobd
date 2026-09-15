package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testWorker(client *ControllerClient) *Worker {
	return &Worker{
		client: client, hostname: "vm", pollInterval: time.Millisecond,
		heartbeatInterval: time.Millisecond, shutdownTimeout: 100 * time.Millisecond,
		processGrace: 10 * time.Millisecond,
	}
}

func TestAlreadyRequestedCancellationDoesNotLaunch(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "should-not-exist")
	var reported bool
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/queues/batch-1/jobs/job/fail" {
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
		var body struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Error != "Job cancelled by user" {
			t.Errorf("error: %s", body.Error)
		}
		reported = true
		fmt.Fprint(w, `{}`)
	})
	if err := testWorker(client).runJob(context.Background(), Job{ID: "job", Command: []string{"touch", marker}, CancelRequested: 1}); err != nil {
		t.Fatal(err)
	}
	if !reported {
		t.Fatal("missing cancellation report")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("cancelled job launched")
	}
}

func TestWorkerRecoveryExecutionAndReportingOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var mu sync.Mutex
	var events []string
	claims, reports := 0, 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		path := strings.TrimPrefix(r.URL.Path, "/queues/batch-1")
		if strings.HasSuffix(path, "/heartbeat") {
			fmt.Fprint(w, `{}`)
			return
		}
		events = append(events, path)
		switch path {
		case "/workers/register":
			fmt.Fprint(w, `{"current_job_id":"old"}`)
		case "/jobs/old/fail":
			var body struct {
				Error    string `json:"error"`
				ExitCode *int   `json:"exit_code"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.ExitCode != nil || !strings.Contains(body.Error, "previous outcome unknown") {
				t.Errorf("recovery: %+v", body)
			}
			fmt.Fprint(w, `{}`)
		case "/workers/worker/claim":
			claims++
			if claims == 1 {
				fmt.Fprint(w, `{"job":{"id":"new","command":["sh","-c","exit 0"]}}`)
			} else {
				fmt.Fprint(w, `{"job":null}`)
				cancel()
			}
		case "/jobs/new/complete":
			reports++
			if reports < 3 {
				w.WriteHeader(503)
				return
			}
			fmt.Fprint(w, `{}`)
		default:
			fmt.Fprint(w, `{}`)
		}
	})
	err := testWorker(client).Run(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{
		"/workers/register", "/jobs/old/fail", "/workers/worker/claim",
		"/jobs/new/output", "/jobs/new/complete", "/jobs/new/complete",
		"/jobs/new/complete", "/workers/worker/claim",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events: %v", events)
	}
}

func TestHeartbeatContinuesDuringExecutionAndShutdownReports(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	marker := filepath.Join(t.TempDir(), "started")
	var beats atomic.Int32
	var finished atomic.Bool
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/register"):
			fmt.Fprint(w, `{"current_job_id":null}`)
		case strings.HasSuffix(r.URL.Path, "/claim"):
			json.NewEncoder(w).Encode(map[string]any{"job": Job{ID: "job", Command: []string{"sh", "-c", `echo ready > "$1"; exec sleep 60`, "sh", marker}}})
		case strings.HasSuffix(r.URL.Path, "/heartbeat"):
			if _, err := os.Stat(marker); err == nil && beats.Add(1) >= 3 {
				cancel()
			}
			fmt.Fprint(w, `{}`)
		case strings.HasSuffix(r.URL.Path, "/fail"):
			var body struct {
				Error    string `json:"error"`
				ExitCode *int   `json:"exit_code"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Error == "" || (body.ExitCode != nil && *body.ExitCode == 0) {
				t.Errorf("shutdown result: %+v", body)
			}
			finished.Store(true)
			fmt.Fprint(w, `{}`)
		default:
			fmt.Fprint(w, `{}`)
		}
	})
	err := testWorker(client).Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run: %v", err)
	}
	if beats.Load() < 3 || !finished.Load() {
		t.Fatalf("heartbeats=%d reported=%v", beats.Load(), finished.Load())
	}
}

func TestShutdownReportIsBounded(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	worker := testWorker(client)
	worker.shutdownTimeout = 20 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- worker.reportResult(ctx, "job", Result{Error: "stopped"}) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("report: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown report retried indefinitely")
	}
}

func TestCancellationBeforeLaunchReportsFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var reported atomic.Bool
	marker := filepath.Join(t.TempDir(), "should-not-exist")
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/output") {
			cancel()
			w.WriteHeader(503)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/fail") {
			reported.Store(true)
		}
		fmt.Fprint(w, `{}`)
	})
	err := testWorker(client).runJob(ctx, Job{ID: "job", Command: []string{"touch", marker}})
	if err != nil || !reported.Load() {
		t.Fatalf("reported=%v err=%v", reported.Load(), err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("launched after stop")
	}
}
