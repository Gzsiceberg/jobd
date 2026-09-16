package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestMissingAPIKeyRunsLocalJobs(t *testing.T) {
	for _, key := range []string{"", "   "} {
		t.Run("key="+key, func(t *testing.T) {
			t.Setenv("JOBD_API_KEY", key)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
			defer server.Close()
			stateDir := filepath.Join(t.TempDir(), "state")
			marker := filepath.Join(t.TempDir(), "executed")
			q, err := openLocalQueue(stateDir, true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := q.submit([]string{"touch", marker}); err != nil {
				t.Fatal(err)
			}
			q.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- runWithContext(ctx, Config{Controller: server.URL, Queue: "default", StateDir: stateDir, LocalPersist: true, PollInterval: time.Millisecond})
			}()
			transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(stateDir, "local", "control.sock"))
			}}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: time.Second}
			for {
				res, err := client.Get("http://local/jobs/local-1")
				if err == nil {
					var job Job
					decodeErr := json.NewDecoder(res.Body).Decode(&job)
					res.Body.Close()
					if decodeErr == nil && job.FinishedAt != nil {
						if job.Status != "succeeded" {
							t.Fatalf("local job: %+v", job)
						}
						break
					}
				}
				select {
				case err := <-done:
					t.Fatalf("worker stopped before executing local job: %v", err)
				case <-time.After(time.Millisecond):
				}
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatal(err)
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("did not stop")
			}
			if requests.Load() != 0 {
				t.Fatal("sent unauthenticated requests")
			}
			q, err = openLocalQueue(stateDir, true)
			if err != nil {
				t.Fatal(err)
			}
			defer q.Close()
			job, err := q.get("local-1")
			if err != nil || job.FinishedAt == nil {
				t.Fatalf("missing terminal result: %+v %v", job, err)
			}
			if job.OutputPath != "" {
				os.Remove(job.OutputPath)
			}
		})
	}
}

func TestMissingAPIKeyDoesNotInitializeClient(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Local-only startup ignores controller configuration, but initializes local state.
	if err := runWithContext(ctx, Config{Controller: "invalid", StateDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
}

func TestConfiguredAPIKeyProceedsWithStartup(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "secret")
	if err := runWithContext(context.Background(), Config{Controller: "invalid", Queue: "default"}); err == nil {
		t.Fatal("expected client initialization")
	}
}

func TestClientRejectsMissingAPIKey(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "")
	client, err := NewControllerClient("http://localhost:1", "worker", "default", time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Register(context.Background(), "host"); err == nil || err.Error() != "JOBD_API_KEY is required" {
		t.Fatalf("unexpected error: %v", err)
	}
}
