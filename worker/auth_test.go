package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestMissingAPIKeyWaitsBeforeStartup(t *testing.T) {
	for _, key := range []string{"", "   "} {
		t.Run("key="+key, func(t *testing.T) {
			t.Setenv("JOBD_API_KEY", key)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
			defer server.Close()
			stateDir := filepath.Join(t.TempDir(), "state")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- runWithContext(ctx, Config{Controller: server.URL, Queue: "default", StateDir: stateDir, PollInterval: time.Millisecond})
			}()
			select {
			case err := <-done:
				t.Fatalf("returned instead of waiting: %v", err)
			case <-time.After(30 * time.Millisecond):
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
			if _, err := os.Stat(stateDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("created worker state before key was supplied: %v", err)
			}
		})
	}
}

func TestMissingAPIKeyDoesNotInitializeClient(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Invalid client configuration would fail if startup moved past the key gate.
	if err := runWithContext(ctx, Config{Controller: "invalid"}); err != nil {
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
