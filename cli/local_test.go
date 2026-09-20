package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fakeLocalWorker(t *testing.T, handle http.HandlerFunc) string {
	t.Helper()
	dir := t.TempDir()
	// Queue tests use a fake lifecycle command; worker tests own startup behavior.
	if err := os.WriteFile(filepath.Join(dir, "jobd-worker"), []byte("#!/bin/sh\n[ \"$1\" = --start ]\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.Mkdir(filepath.Join(dir, "local"), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(dir, "local/control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("API key sent to local worker")
		}
		handle(w, r)
	})}
	done := make(chan struct{})
	go func() { defer close(done); server.Serve(listener) }()
	t.Cleanup(func() { server.Close(); <-done })
	return dir
}

func TestLocalCLI(t *testing.T) {
	now := time.Now().Format(time.RFC3339Nano)
	path := "/tmp/local.log"
	record := job{ID: "local-1", Status: "running", StartedAt: &now, OutputPath: &path, CancelRequested: 1, Command: []string{"echo", "a b", ""}}
	var submits atomic.Int32
	dir := fakeLocalWorker(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /jobs":
			submits.Add(1)
			var body struct {
				Command []string `json:"command"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Command) != 3 || body.Command[1] != "a b" || body.Command[2] != "" {
				t.Errorf("payload: %+v %v", body, err)
			}
			json.NewEncoder(w).Encode(record)
		case "GET /jobs":
			if r.URL.RawQuery != "limit=100&offset=0" {
				t.Errorf("pagination: %s", r.URL)
			}
			json.NewEncoder(w).Encode(map[string]any{"jobs": []job{record}})
		case "GET /jobs/local-1", "GET /jobs/latest":
			json.NewEncoder(w).Encode(record)
		case "POST /jobs/swap":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["first"] != "local-1" || body["second"] != "local-2" {
				t.Errorf("swap: %+v %v", body, err)
			}
		case "POST /jobs/urgent":
			fmt.Fprint(w, `{"succeeded":["local-1"],"failed":[]}`)
		case "DELETE /jobs/local-1", "POST /jobs/local-1/cancel", "POST /jobs/clear":
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "bad request", 400)
		}
	})
	t.Setenv("JOBD_STATE_DIR", dir)
	t.Setenv("JOBD_WORKER_TOKEN", "must-not-be-sent")
	t.Setenv("JOBD_CONTROLLER", "invalid-no-http")
	var out, diagnostic bytes.Buffer
	call := func(args ...string) error {
		out.Reset()
		diagnostic.Reset()
		return run(args, strings.NewReader(""), &out, &diagnostic)
	}
	if err := call("--local", "echo", "a b", ""); err != nil || out.String() != "local-1\n" {
		t.Fatalf("submit: %s %v", out.String(), err)
	}
	if err := call("--local", "-l"); err != nil || !strings.Contains(out.String(), "cancelling") || !strings.Contains(out.String(), "ELAPSED") {
		t.Fatalf("list: %s %v", out.String(), err)
	}
	for _, args := range [][]string{{"--local", "-o"}, {"--local", "-o", "local-1"}} {
		if err := call(args...); err != nil || out.String() != "/tmp/local.log\n" {
			t.Fatalf("output: %s %v", out.String(), err)
		}
	}
	for _, args := range [][]string{{"--local", "-r", "local-1"}, {"--local", "-C"}, {"--local", "-k"}, {"--local", "-k", "local-1"}, {"--local", "-r"}, {"--local", "-u"}, {"--local", "-u", "local-1"}, {"--local", "-U", "local-1", "local-2"}} {
		if err := call(args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	for _, args := range [][]string{{"--local", "--restart"}, {"--local", "--"}, {"--local", "-U", "local-1"}} {
		if err := call(args...); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	t.Setenv("JOBD_WORKER_TOKEN", "")
	if err := call("--local", "-l"); err != nil {
		t.Fatal(err)
	}
	if submits.Load() != 1 {
		t.Fatal("unexpected submissions")
	}
}

func TestLocalRequiresWorkerBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := filepath.Join(t.TempDir(), "not-created")
	var out bytes.Buffer
	t.Setenv("JOBD_STATE_DIR", dir)
	err := run([]string{"--local", "echo", "hello"}, strings.NewReader(""), &out, &out)
	if err == nil || !strings.Contains(err.Error(), "jobd-worker not found") {
		t.Fatalf("error: %v", err)
	}
}

func TestLocalPagination(t *testing.T) {
	for _, apiKey := range []string{"", "must-not-be-sent"} {
		t.Run(fmt.Sprintf("apiKeySet=%t", apiKey != ""), func(t *testing.T) {
			t.Setenv("JOBD_WORKER_TOKEN", apiKey)
			var pages atomic.Int32
			dir := fakeLocalWorker(t, func(w http.ResponseWriter, r *http.Request) {
				page := int(pages.Add(1)) - 1
				if r.Method != "GET" || r.URL.Path != "/jobs" || r.URL.RawQuery != fmt.Sprintf("limit=100&offset=%d", page*100) {
					t.Errorf("pagination: %s %s", r.Method, r.URL)
				}
				count := 100
				if page == 1 {
					count = 1
				}
				jobs := make([]job, count)
				for i := range jobs {
					jobs[i] = job{ID: fmt.Sprintf("local-%d", page*100+i+1), Status: "queued", Command: []string{"true"}}
				}
				json.NewEncoder(w).Encode(map[string]any{"jobs": jobs})
			})
			var out, diagnostic bytes.Buffer
			t.Setenv("JOBD_STATE_DIR", dir)
			if err := run([]string{"--local", "-l"}, strings.NewReader(""), &out, &diagnostic); err != nil {
				t.Fatal(err)
			}
			lines := len(strings.Split(strings.TrimSpace(out.String()), "\n"))
			if pages.Load() != 2 || lines != 102 {
				t.Fatalf("local listing did not paginate: got %d pages and %d stdout lines; stderr: %s", pages.Load(), lines, diagnostic.String())
			}
		})
	}
}

func TestLocalWorkerError(t *testing.T) {
	dir := fakeLocalWorker(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "cannot remove a running local job", 400) })
	c, err := newLocalClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = c.request("DELETE", "/jobs/local-1", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "running") {
		t.Fatalf("error: %v", err)
	}
}

func TestLocalLargePage(t *testing.T) {
	// More than the old 16 MiB limit, including worst-case JSON escaping.
	jobs := make([]job, 100)
	for i := range jobs {
		jobs[i] = job{ID: fmt.Sprintf("local-%d", i+1), Command: []string{"echo", strings.Repeat("\x01", 32<<10)}}
	}
	dir := fakeLocalWorker(t, func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(map[string]any{"jobs": jobs}) })
	c, err := newLocalClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Jobs []job `json:"jobs"`
	}
	if err := c.request("GET", "/jobs?limit=100", nil, &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Jobs) != 100 || page.Jobs[99].Command[1] != jobs[99].Command[1] {
		t.Fatal("truncated page")
	}
}
