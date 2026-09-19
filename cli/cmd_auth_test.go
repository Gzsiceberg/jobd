package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateWorkerToken(t *testing.T) {
	t.Setenv("JOBD_MASTER_KEY", "admin-secret")
	t.Setenv("JOBD_WORKER_TOKEN", "worker-token")
	t.Setenv("JOBD_QUEUE", "batch")
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/queues/batch/auth/worker-token" || r.Header.Get("Authorization") != "Bearer admin-secret" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]int64
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["duration_seconds"] != 86400 {
			t.Errorf("invalid body: %v %v", body, err)
		}
		io.WriteString(w, `{"token":"generated-token","expires_at":"2030-01-01T00:00:00Z"}`)
	}))
	defer server.Close()
	old := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = old })
	t.Setenv("JOBD_CONTROLLER", server.URL)
	var out, diagnostic bytes.Buffer
	if err := run([]string{"auth", "create-worker-token", "--duration", "24h"}, strings.NewReader(""), &out, &diagnostic); err != nil {
		t.Fatal(err)
	}
	if out.String() != "generated-token\n" || !strings.Contains(diagnostic.String(), "2030-01-01") || strings.Contains(diagnostic.String(), "generated-token") {
		t.Fatalf("unexpected output: %q %q", out.String(), diagnostic.String())
	}
	for _, args := range [][]string{
		{"auth", "create-worker-key", "--duration", "24h"},
		{"auth", "create-worker-token"},
		{"auth", "create-worker-token", "--duration", "0s"},
		{"auth", "create-worker-token", "--duration", "1.5s"},
		{"auth", "create-worker-token", "--duration", "721h"},
		{"auth", "create-worker-token", "--duration", "garbage"},
		{"--local", "auth", "create-worker-token", "--duration", "24h"},
	} {
		if err := run(args, strings.NewReader(""), io.Discard, io.Discard); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
	t.Setenv("JOBD_CONTROLLER", "http://localhost:8787")
	if err := run([]string{"auth", "create-worker-token", "--duration", "24h"}, strings.NewReader(""), io.Discard, io.Discard); err == nil {
		t.Fatal("accepted HTTP")
	}
	t.Setenv("JOBD_CONTROLLER", server.URL)
	t.Setenv("JOBD_MASTER_KEY", "")
	if err := run([]string{"auth", "create-worker-token", "--duration", "24h"}, strings.NewReader(""), io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "JOBD_MASTER_KEY") {
		t.Fatalf("worker credential used to generate token: %v", err)
	}
	if calls != 1 {
		t.Fatalf("unexpected requests: %d", calls)
	}
}

func TestAdminKeyEnablesRemoteJobCommands(t *testing.T) {
	t.Setenv("JOBD_MASTER_KEY", "admin")
	t.Setenv("JOBD_WORKER_TOKEN", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer admin" || r.URL.Path != "/queues/default/jobs" {
			t.Error("admin authentication missing")
		}
		io.WriteString(w, `{"id":"1"}`)
	}))
	defer server.Close()
	t.Setenv("JOBD_CONTROLLER", server.URL)
	t.Setenv("JOBD_QUEUE", "default")
	var out bytes.Buffer
	if err := run([]string{"echo", "hello"}, strings.NewReader(""), &out, io.Discard); err != nil || out.String() != "1\n" {
		t.Fatalf("admin submission failed: %v %q", err, out.String())
	}
}
