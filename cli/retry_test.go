package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestJobRetry(t *testing.T) {
	t.Setenv("JOBD_MASTER_KEY", "auth")
	t.Setenv("JOBD_WORKER_TOKEN", "")
	t.Setenv("JOBD_QUEUE", "batch")
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method: %s", r.Method)
		}
		paths = append(paths, r.URL.Path)
		io.WriteString(w, `{"retried":1}`)
	}))
	defer server.Close()
	t.Setenv("JOBD_CONTROLLER", server.URL)
	for _, args := range [][]string{{"job", "retry"}, {"job", "retry", "1", "--all"}, {"job", "retry", "1", "2", "--all"}} {
		if err := run(args, nil, io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if len(paths) != 0 {
		t.Fatal("invalid args made requests")
	}
	for _, arg := range []string{"123", "--all"} {
		var out bytes.Buffer
		if err := run([]string{"job", "retry", arg}, nil, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
		if out.String() != "Requeued 1 failed job(s).\n" {
			t.Fatal(out.String())
		}
	}
	var out bytes.Buffer
	if err := run([]string{"job", "retry", "42", "43", "44"}, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if out.String() != "Requeued 3 failed job(s).\n" {
		t.Fatal(out.String())
	}
	if !reflect.DeepEqual(paths, []string{"/queues/batch/jobs/123/retry", "/queues/batch/jobs/retry-all", "/queues/batch/jobs/42/retry", "/queues/batch/jobs/43/retry", "/queues/batch/jobs/44/retry"}) {
		t.Fatalf("paths: %v", paths)
	}
}

func TestJobRetryStopsOnFailure(t *testing.T) {
	t.Setenv("JOBD_MASTER_KEY", "auth")
	t.Setenv("JOBD_WORKER_TOKEN", "")
	t.Setenv("JOBD_QUEUE", "batch")
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/43/retry") {
			http.Error(w, "Only failed jobs can be retried", http.StatusConflict)
			return
		}
		io.WriteString(w, `{"retried":1}`)
	}))
	defer server.Close()
	t.Setenv("JOBD_CONTROLLER", server.URL)
	err := run([]string{"job", "retry", "42", "43", "44"}, nil, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "retry job 43 (requeued 1 earlier job(s))") {
		t.Fatalf("error: %v", err)
	}
	if !reflect.DeepEqual(paths, []string{"/queues/batch/jobs/42/retry", "/queues/batch/jobs/43/retry"}) {
		t.Fatalf("paths: %v", paths)
	}
}
