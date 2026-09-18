package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fakeLocalServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "local"), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(dir, "local/control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	done := make(chan struct{})
	go func() { defer close(done); server.Serve(listener) }()
	t.Cleanup(func() { server.Shutdown(context.Background()); <-done })
	return dir
}

func TestWorkerHealth(t *testing.T) {
	for _, tc := range []struct {
		name      string
		code      int
		body      string
		wantReady bool
	}{
		{"healthy", 200, `{"status":"ok"}`, true},
		{"stopping", 503, "worker is stopping", false},
		{"missing route", 404, "not found", false},
		{"malformed response", 200, "not JSON", false},
		{"wrong status", 200, `{"status":"unhealthy"}`, false},
		{"missing status", 200, `{}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := fakeLocalServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/health" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				w.WriteHeader(tc.code)
				io.WriteString(w, tc.body)
			})
			ready, err := workerAvailable(dir)
			if ready != tc.wantReady || (err == nil) != tc.wantReady {
				t.Fatalf("ready=%t err=%v", ready, err)
			}
		})
	}
}

func TestWorkerHealthUnavailable(t *testing.T) {
	dir := t.TempDir()
	if ready, err := workerAvailable(dir); ready || err != nil {
		t.Fatalf("missing socket: %t %v", ready, err)
	}
	// A listener alone is not proof that the worker's HTTP handler is healthy.
	dir = fakeLocalServer(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	start := time.Now()
	if ready, err := workerAvailable(dir); ready || err == nil {
		t.Fatalf("unresponsive worker: %t %v", ready, err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("health check did not time out promptly")
	}
}

func TestWorkerHealthStaleSocket(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "local"), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Net: "unix", Name: filepath.Join(dir, "local/control.sock")})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	listener.Close()
	if ready, err := workerAvailable(dir); ready || err != nil {
		t.Fatalf("stale socket: %t %v", ready, err)
	}
}
