package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
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
	for _, args := range [][]string{{"job", "retry"}, {"job", "retry", "1", "--all"}, {"job", "retry", "1", "2"}} {
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
	if len(paths) != 2 || paths[0] != "/queues/batch/jobs/123/retry" || paths[1] != "/queues/batch/jobs/retry-all" {
		t.Fatalf("paths: %v", paths)
	}
}
