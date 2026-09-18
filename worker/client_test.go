package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, handler http.HandlerFunc) *ControllerClient {
	t.Helper()
	t.Setenv("JOBD_API_KEY", "test-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing API key")
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	client, err := NewControllerClient(server.URL, "worker", "batch-1", time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.http.CloseIdleConnections)
	return client
}

func TestQueueScopedRequests(t *testing.T) {
	var paths []string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Error("expected JSON POST")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		switch r.URL.Path {
		case "/queues/batch-1/workers/register":
			if body["worker_id"] != "worker" || body["hostname"] != "vm" {
				t.Errorf("registration: %v", body)
			}
		case "/queues/batch-1/jobs/job/output":
			if body["worker_id"] != "worker" || body["output_path"] != "/tmp/jobd-test.log" {
				t.Errorf("output: %v", body)
			}
		case "/queues/batch-1/jobs/job/fail":
			if body["exit_code"] != nil || body["error"] != "launch failed" {
				t.Errorf("failure: %v", body)
			}
		}
		fmt.Fprint(w, `{"job":null,"current_job_id":null}`)
	})
	ctx := context.Background()
	if _, err := client.Register(ctx, "vm"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Heartbeat(ctx); err != nil {
		t.Fatal(err)
	}
	if job, err := client.Claim(ctx); err != nil || job != nil {
		t.Fatalf("claim: %v %v", job, err)
	}
	if err := client.Output(ctx, "job", "/tmp/jobd-test.log"); err != nil {
		t.Fatal(err)
	}
	zero := 0
	if err := client.Finish(ctx, "job", Result{ExitCode: &zero}); err != nil {
		t.Fatal(err)
	}
	if err := client.Finish(ctx, "job", Result{Error: "launch failed"}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/queues/batch-1/workers/register", "/queues/batch-1/workers/worker/heartbeat",
		"/queues/batch-1/workers/worker/claim",
		"/queues/batch-1/jobs/job/output", "/queues/batch-1/jobs/job/complete", "/queues/batch-1/jobs/job/fail",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths: %v", paths)
	}
}

func TestClientValidation(t *testing.T) {
	for _, queue := range []string{"", "UPPER", "../other", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		if _, err := NewControllerClient("http://controller", "worker", queue, time.Second); err == nil {
			t.Errorf("accepted queue %q", queue)
		}
	}
	for _, address := range []string{"", "://bad", "ftp://controller", "http://", "http://controller?x=1", "http://controller#fragment"} {
		if _, err := NewControllerClient(address, "worker", "default", time.Second); err == nil {
			t.Errorf("accepted URL %q", address)
		}
	}
}

func TestHTTPRetryPolicy(t *testing.T) {
	for _, status := range []int{429, 500, 503, 400, 401, 404, 409, 302} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			var payloads []string
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				payloads = append(payloads, string(data))
				if calls.Add(1) == 1 {
					w.WriteHeader(status)
					return
				}
				fmt.Fprint(w, `{}`)
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := client.Finish(ctx, "job", Result{Error: "failed"})
			retryable := status == 429 || status >= 500
			if retryable {
				if err != nil || calls.Load() != 2 {
					t.Fatalf("calls=%d err=%v", calls.Load(), err)
				}
				if payloads[0] != payloads[1] {
					t.Fatal("retry changed payload")
				}
			} else if err == nil || calls.Load() != 1 {
				t.Fatalf("permanent error: calls=%d err=%v", calls.Load(), err)
			}
		})
	}
}

func TestRetryWaitIsCancellable(t *testing.T) {
	requested := make(chan struct{}, 1)
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		requested <- struct{}{}
	})
	client.retryInterval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := client.Heartbeat(ctx); done <- err }()
	<-requested
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("retry ignored cancellation")
	}
}

func TestNetworkErrorIsRetried(t *testing.T) {
	var calls atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		fmt.Fprint(w, `{"job":{"id":"job","command":["echo","hello"]}}`)
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	job, err := client.Claim(ctx)
	if err != nil || job == nil || job.ID != "job" || calls.Load() != 2 {
		t.Fatalf("claim: job=%v calls=%d err=%v", job, calls.Load(), err)
	}
}

func TestInvalidJSONIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `not JSON`)
	})
	if _, err := client.Claim(context.Background()); err == nil || calls.Load() != 1 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}
}
