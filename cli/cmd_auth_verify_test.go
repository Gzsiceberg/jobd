package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVerifyWorkerToken(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		wantError string
	}{
		{"valid", 200, "", ""},
		{"unauthorized", 401, `{"error":"worker-secret"}`, "HTTP 401"},
		{"wrong queue", 403, `{"error":"worker-secret"}`, "HTTP 403"},
		{"unconfigured", 503, `{}`, "HTTP 503"},
		{"invalid flag", 200, `{"valid":false,"queue":"batch","expires_at":"2030-01-01T00:00:00Z"}`, "invalid worker token verification response"},
		{"invalid expiry", 200, `{"valid":true,"queue":"batch","expires_at":"worker-secret"}`, "invalid worker token verification response"},
		{"mismatched queue", 200, `{"valid":true,"queue":"other","expires_at":"2030-01-01T00:00:00Z"}`, "invalid worker token verification response"},
		{"missing fields", 200, `{}`, "invalid worker token verification response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("JOBD_MASTER_KEY", "admin-secret")
			t.Setenv("JOBD_WORKER_TOKEN", " worker-secret ")
			t.Setenv("JOBD_QUEUE", "batch")
			expires := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/queues/batch/auth/worker-token/verify" || r.Header.Get("Authorization") != "Bearer worker-secret" {
					t.Errorf("unexpected verification request: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.status)
				if tc.body != "" {
					w.Write([]byte(tc.body))
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"valid": true, "queue": "batch", "expires_at": expires.Format(time.RFC3339)})
			}))
			defer server.Close()
			old := http.DefaultTransport
			http.DefaultTransport = server.Client().Transport
			t.Cleanup(func() { http.DefaultTransport = old })
			t.Setenv("JOBD_CONTROLLER", server.URL)
			var out, diagnostic bytes.Buffer
			err := run([]string{"auth", "verify-worker-token"}, strings.NewReader(""), &out, &diagnostic)
			if calls != 1 {
				t.Fatalf("expected one request, got %d", calls)
			}
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("unexpected error: %v", err)
				}
				if out.Len() != 0 || strings.Contains(err.Error(), "worker-secret") {
					t.Fatalf("unexpected output or leaked token: %q %v", out.String(), err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			prefix := "Worker token valid for queue \"batch\"\nExpires at: " + expires.Format(time.RFC3339) + "\nRemaining: "
			if !strings.HasPrefix(out.String(), prefix) {
				t.Fatalf("unexpected output: %q", out.String())
			}
			remaining, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(out.String(), prefix)))
			if err != nil || remaining < 59*time.Minute || remaining > time.Hour {
				t.Fatalf("unexpected remaining lifetime: %s (%v)", remaining, err)
			}
			if diagnostic.Len() != 0 {
				t.Fatalf("unexpected diagnostic: %q", diagnostic.String())
			}
		})
	}
}

func TestVerifyWorkerTokenRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, token, address string
		args                 []string
		want                 string
	}{
		{"missing token", "", "https://example.com", nil, "JOBD_WORKER_TOKEN is required"},
		{"blank token", "  ", "https://example.com", nil, "JOBD_WORKER_TOKEN is required"},
		{"HTTP", "worker-secret", "http://localhost:8787", nil, "requires HTTPS"},
		{"local", "worker-secret", "https://example.com", []string{"--local", "auth", "verify-worker-token"}, "not supported in local mode"},
		{"extra argument", "worker-secret", "https://example.com", []string{"auth", "verify-worker-token", "extra"}, "unknown command"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("JOBD_MASTER_KEY", "admin-secret")
			t.Setenv("JOBD_WORKER_TOKEN", tc.token)
			t.Setenv("JOBD_CONTROLLER", tc.address)
			t.Setenv("JOBD_QUEUE", "batch")
			args := tc.args
			if args == nil {
				args = []string{"auth", "verify-worker-token"}
			}
			var out, diagnostic bytes.Buffer
			err := run(args, strings.NewReader(""), &out, &diagnostic)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Len() != 0 {
				t.Fatalf("unexpected output: %q", out.String())
			}
		})
	}
}
