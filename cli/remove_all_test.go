package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveAllRequiresMatchingQueueConfirmation(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "auth")
	t.Setenv("JOBD_QUEUE", "batch")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/queues/batch/jobs/remove-all" || r.Header.Get("Authorization") != "Bearer auth" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		io.WriteString(w, `{"removed":153,"kept_running":2}`)
	}))
	defer server.Close()
	t.Setenv("JOBD_CONTROLLER", server.URL)
	for _, answer := range []string{"", "batch", "\n", "yes\n", "other\n", " batch\n", strings.Repeat("x", 300) + "\n"} {
		var out, diagnostic bytes.Buffer
		err := run([]string{"job", "remove", "--all"}, strings.NewReader(answer), &out, &diagnostic)
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("%q: %v", answer, err)
		}
		if calls != 0 || out.Len() != 0 {
			t.Fatal("unconfirmed removal performed an operation")
		}
		if !strings.Contains(diagnostic.String(), `queue "batch"`) || !strings.Contains(diagnostic.String(), server.URL) || !strings.Contains(diagnostic.String(), `Type "batch"`) {
			t.Fatal(diagnostic.String())
		}
	}
	var out, diagnostic bytes.Buffer
	if err := run([]string{"job", "remove", "--all"}, strings.NewReader("batch\n"), &out, &diagnostic); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || out.String() != "Removed 153 job(s); kept 2 running job(s).\n" {
		t.Fatalf("calls=%d output=%q", calls, out.String())
	}
}

func TestRemoveAllRejectsJobIDAndNeverRetries(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "auth")
	t.Setenv("JOBD_QUEUE", "batch")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "unavailable", 503)
	}))
	defer server.Close()
	t.Setenv("JOBD_CONTROLLER", server.URL)
	var diagnostic bytes.Buffer
	if err := run([]string{"job", "remove", "123", "--all"}, strings.NewReader("batch\n"), io.Discard, &diagnostic); err == nil {
		t.Fatal("accepted ID with --all")
	}
	if calls != 0 || diagnostic.Len() != 0 {
		t.Fatal("invalid arguments prompted or sent a request")
	}
	if err := run([]string{"job", "remove", "--all"}, strings.NewReader("batch\n"), io.Discard, io.Discard); err == nil {
		t.Fatal("server failure ignored")
	}
	if calls != 1 {
		t.Fatal("mutation retried")
	}
}

func TestRemoveAllLocalConfirmation(t *testing.T) {
	for _, explicit := range []bool{true, false} {
		t.Run(map[bool]string{true: "explicit", false: "no-key-fallback"}[explicit], func(t *testing.T) {
			calls := 0
			dir := fakeLocalWorker(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.Path != "/jobs/remove-all" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				io.WriteString(w, `{"removed":3,"kept_running":1}`)
			})
			t.Setenv("JOBD_STATE_DIR", dir)
			t.Setenv("JOBD_CONTROLLER", "invalid-unused-controller")
			t.Setenv("JOBD_QUEUE", "remote-batch")
			t.Setenv("JOBD_API_KEY", "")
			args := []string{"job", "remove", "--all"}
			if explicit {
				t.Setenv("JOBD_API_KEY", "auth")
				args = append([]string{"--local"}, args...)
			}
			var out, diagnostic bytes.Buffer
			if err := run(args, strings.NewReader("remote-batch\n"), &out, &diagnostic); err == nil {
				t.Fatal("confirmed wrong target")
			}
			if calls != 0 {
				t.Fatal("unconfirmed local removal")
			}
			if !strings.Contains(diagnostic.String(), "local queue") || !strings.Contains(diagnostic.String(), dir) || !strings.Contains(diagnostic.String(), `Type "local"`) {
				t.Fatal(diagnostic.String())
			}
			if err := run(args, strings.NewReader("local\r\n"), &out, &diagnostic); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || out.String() != "Removed 3 job(s); kept 1 running job(s).\n" {
				t.Fatalf("calls=%d output=%q", calls, out.String())
			}
		})
	}
}

func TestRemoveAllCancellationDoesNotStartLocalWorker(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("JOBD_STATE_DIR", filepath.Join(t.TempDir(), "absent"))
	err := run([]string{"job", "remove", "--all"}, strings.NewReader("no\n"), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("worker startup attempted before confirmation: %v", err)
	}
}
