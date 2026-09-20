package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWorkerList(t *testing.T) {
	t.Setenv("JOBD_MASTER_KEY", "admin")
	t.Setenv("JOBD_QUEUE", "batch")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" || r.URL.Path != "/queues/batch/workers" || r.Header.Get("Authorization") != "Bearer admin" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		io.WriteString(w, `{"workers":[{"worker_id":"worker-123456789","hostname":"host","token_expired_time":"2000-01-01T00:00:00Z"},{"worker_id":"worker-987654321","hostname":"legacy","token_expired_time":null}]}`)
	}))
	defer server.Close()
	t.Setenv("JOBD_CONTROLLER", server.URL)
	var out bytes.Buffer
	if err := run([]string{"worker", "list"}, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"WORKER_ID", "WORKER_NAME", "TOKEN_EXPIRED_TIME", "worker-1", "worker-9", `"host"`, "expired", `"legacy"`, "unknown"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, &out)
		}
	}
	for _, fullID := range []string{"worker-123456789", "worker-987654321"} {
		if strings.Contains(out.String(), fullID) {
			t.Fatalf("printed full worker ID: %s", fullID)
		}
	}
	if strings.Contains(out.String(), "2000-01-01") {
		t.Fatal("printed absolute expiry")
	}
	if err := run([]string{"--local", "worker", "list"}, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("accepted local listing")
	}
	t.Setenv("JOBD_MASTER_KEY", "")
	if err := run([]string{"worker", "list"}, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("accepted non-admin listing")
	}
	if requests != 1 {
		t.Fatalf("unexpected requests: %d", requests)
	}
}

func TestTokenTimeRemaining(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		offset time.Duration
		want   string
	}{
		{time.Hour + 2*time.Minute, "1h2m0s remaining"},
		{-time.Minute, "expired 1m0s ago"},
		{0, "expired 0s ago"},
		{time.Millisecond, "<1s remaining"},
	} {
		expiry := now.Add(tc.offset).Format(time.RFC3339Nano)
		if got := tokenTimeRemaining(&expiry, now); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
	if got := tokenTimeRemaining(nil, now); got != "unknown" {
		t.Fatal(got)
	}
	invalid := "invalid"
	if got := tokenTimeRemaining(&invalid, now); got != "unknown" {
		t.Fatal(got)
	}
}
