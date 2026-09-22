package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestSubmitPreservesArguments(t *testing.T) {
	t.Setenv("JOBD_WORKER_TOKEN", "test-key")
	var command []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/queues/batch/jobs" {
			t.Errorf("request: %s %s", r.Method, r.URL)
		}
		var body struct {
			Command []string `json:"command"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		command = body.Command
		fmt.Fprint(w, `{"id":"new-id"}`)
	}))
	defer server.Close()
	var out, diag bytes.Buffer
	t.Setenv("JOBD_CONTROLLER", server.URL)
	t.Setenv("JOBD_QUEUE", "batch")
	args := []string{"sh", "-c", "echo 'hello world'"}
	if err := run(args, strings.NewReader(""), &out, &diag); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(command, args) {
		t.Fatalf("argv: %q", command)
	}
	if out.String() != "new-id\n" {
		t.Fatal(out.String())
	}
}

func TestActions(t *testing.T) {
	t.Setenv("JOBD_WORKER_TOKEN", "test-key")
	for _, tc := range []struct {
		args         []string
		method, path string
		body         map[string]string
	}{
		{[]string{"-C"}, "POST", "/jobs/clear", nil},
		{[]string{"-r", "abc"}, "DELETE", "/jobs/abc", nil},
		{[]string{"-k", "abc"}, "POST", "/jobs/abc/cancel", nil},
		{[]string{"-u", "abc"}, "POST", "/jobs/urgent", nil},
		{[]string{"-U", "abc", "def"}, "POST", "/jobs/swap", map[string]string{"first": "abc", "second": "def"}},
	} {
		t.Run(tc.args[0], func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.Path != "/queues/default"+tc.path {
					t.Errorf("request: %s %s", r.Method, r.URL)
				}
				if tc.body != nil {
					var body map[string]string
					json.NewDecoder(r.Body).Decode(&body)
					if !reflect.DeepEqual(body, tc.body) {
						t.Errorf("body: %v", body)
					}
				}
				fmt.Fprint(w, `{"ok":true}`)
			}))
			defer server.Close()
			t.Setenv("JOBD_CONTROLLER", server.URL)
			t.Setenv("JOBD_QUEUE", "default")
			var out bytes.Buffer
			if err := run(tc.args, strings.NewReader(""), &out, &out); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal(calls)
			}
		})
	}
}

func TestDefaultsAndOutput(t *testing.T) {
	t.Setenv("JOBD_WORKER_TOKEN", "test-key")
	for _, action := range []string{"-o", "-r", "-u", "-k"} {
		t.Run(action, func(t *testing.T) {
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.RequestURI())
				fmt.Fprint(w, `{"id":"abc","hostname":"remote","worker_id":"worker","output_path":"/tmp/log"}`)
			}))
			defer server.Close()
			t.Setenv("JOBD_CONTROLLER", server.URL)
			t.Setenv("JOBD_QUEUE", "default")
			var out, diag bytes.Buffer
			if err := run([]string{action}, strings.NewReader(""), &out, &diag); err != nil {
				t.Fatal(err)
			}
			kind := "added"
			if action == "-o" || action == "-k" {
				kind = "run"
			}
			if paths[0] != "/queues/default/jobs/latest?kind="+kind {
				t.Fatal(paths)
			}
			if action == "-o" && (out.String() != "/tmp/log\n" || !strings.Contains(diag.String(), "remote")) {
				t.Fatalf("%s %s", &out, &diag)
			}
			if action != "-o" && len(paths) != 2 {
				t.Fatal(paths)
			}
		})
	}
}

func TestListCancellation(t *testing.T) {
	t.Setenv("JOBD_STATE_DIR", t.TempDir())
	t.Setenv("JOBD_WORKER_TOKEN", "test-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"jobs":[{"id":"job","status":"running","cancel_requested":1,"command":["sleep","60"]}]}`)
	}))
	defer server.Close()
	t.Setenv("JOBD_CONTROLLER", server.URL)
	t.Setenv("JOBD_QUEUE", "default")
	var out bytes.Buffer
	if err := run([]string{"-l"}, strings.NewReader(""), &out, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out.String(), "\n")
	if strings.Fields(lines[0])[3] != "HOST" {
		t.Fatal(out.String())
	}
	fields := strings.Fields(lines[1])
	if fields[1] != "cancelling" {
		t.Fatal(out.String())
	}
}

func TestListPagination(t *testing.T) {
	t.Setenv("JOBD_STATE_DIR", t.TempDir())
	t.Setenv("JOBD_WORKER_TOKEN", "test-key")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := fmt.Sprintf("/queues/default/jobs?limit=100&offset=%d", calls*100)
		if r.URL.RequestURI() != expected {
			t.Errorf("URL: %s", r.URL)
		}
		calls++
		jobs := []job{}
		if calls == 1 {
			for i := 0; i < 100; i++ {
				jobs = append(jobs, job{ID: fmt.Sprint(i), Status: "queued", Command: []string{"echo", "a\nb"}})
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"jobs": jobs})
	}))
	defer server.Close()
	t.Setenv("JOBD_CONTROLLER", server.URL)
	t.Setenv("JOBD_QUEUE", "default")
	var out bytes.Buffer
	if err := run(nil, strings.NewReader(""), &out, &out); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || strings.Count(out.String(), "\n") != 101 {
		t.Fatalf("calls=%d output=%s", calls, &out)
	}
}

func TestErrorsAndNoMutationRetry(t *testing.T) {
	t.Setenv("JOBD_WORKER_TOKEN", "test-key")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, `{"error":"unavailable"}`, 503) }))
	defer server.Close()
	t.Setenv("JOBD_CONTROLLER", server.URL)
	t.Setenv("JOBD_QUEUE", "default")
	var out bytes.Buffer
	if err := run([]string{"echo", "hello"}, strings.NewReader(""), &out, &out); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	for _, args := range [][]string{{"-U", "a"}, {"-C", "a"}, {"-k", "a", "b"}, {"--"}, {"-bad"}, {"--controller"}} {
		if err := run(args, strings.NewReader(""), &out, &out); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
	if calls != 1 {
		t.Fatal("invalid arguments made requests")
	}
}
