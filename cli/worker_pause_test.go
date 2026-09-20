package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkerPauseResume(t *testing.T) {
	t.Setenv("JOBD_MASTER_KEY", "admin")
	t.Setenv("JOBD_QUEUE", "batch")
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer admin" {
			t.Errorf("unexpected request: %s %v", r.Method, r.Header)
		}
		paths = append(paths, r.URL.Path)
		if strings.Contains(r.URL.Path, "/ambiguous/") {
			http.Error(w, "Ambiguous worker prefix: abc-1, abc-2", 409)
			return
		}
		io.WriteString(w, `{"worker_id":"abcdef-1234","hostname":"host"}`)
	}))
	defer server.Close()
	t.Setenv("JOBD_CONTROLLER", server.URL)
	for _, action := range []string{"pause", "resume"} {
		var out bytes.Buffer
		if err := run([]string{"worker", action, "abcdef"}, nil, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), `abcdef-1234 ("host")`) {
			t.Fatal(out.String())
		}
		if paths[len(paths)-1] != "/queues/batch/workers/abcdef/"+action {
			t.Fatal(paths)
		}
	}
	for _, action := range []string{"pause", "resume"} {
		start := len(paths)
		var out bytes.Buffer
		if err := run([]string{"worker", action, "first", "second"}, nil, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
		want := "/queues/batch/workers/first/" + action + ",/queues/batch/workers/second/" + action
		if strings.Join(paths[start:], ",") != want || strings.Count(out.String(), "new remote assignments") != 2 {
			t.Fatalf("paths: %v, output: %s", paths[start:], out.String())
		}
		start = len(paths)
		out.Reset()
		err := run([]string{"worker", action, "first", "ambiguous", "never"}, nil, &out, io.Discard)
		if err == nil || !strings.Contains(err.Error(), action+" worker ambiguous (1 earlier request(s) succeeded)") {
			t.Fatalf("error: %v", err)
		}
		want = "/queues/batch/workers/first/" + action + ",/queues/batch/workers/ambiguous/" + action
		if strings.Join(paths[start:], ",") != want || strings.Count(out.String(), "new remote assignments") != 1 {
			t.Fatalf("paths: %v, output: %s", paths[start:], out.String())
		}
	}
	err := run([]string{"worker", "pause", "ambiguous"}, nil, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "abc-1, abc-2") {
		t.Fatalf("error: %v", err)
	}
	count := len(paths)
	for _, args := range [][]string{{"worker", "pause"}, {"worker", "resume"}, {"--local", "worker", "pause", "a"}} {
		if err := run(args, nil, io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	t.Setenv("JOBD_MASTER_KEY", "")
	t.Setenv("JOBD_WORKER_TOKEN", "worker-token")
	if err := run([]string{"worker", "pause", "abcdef"}, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "JOBD_MASTER_KEY") {
		t.Fatalf("error: %v", err)
	}
	if len(paths) != count {
		t.Fatal("invalid invocation made requests")
	}
}
