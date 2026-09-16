package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDisplayCommand(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"echo", "hello"}, `echo hello`},
		{[]string{"echo", "hello world", ""}, `echo 'hello world' ''`},
		{[]string{"sh", "-c", `echo "$HOME"`}, `sh -c 'echo "$HOME"'`},
		{[]string{"echo", "it's fine"}, `echo 'it'\''s fine'`},
		{[]string{"echo", "a\nb\tc"}, `echo $'a\nb\tc'`},
		{[]string{"echo", "$HOME", "$(printf injected)", "*.go", "~", "a;b"}, `echo '$HOME' '$(printf injected)' '*.go' '~' 'a;b'`},
		{[]string{"if", "a=b"}, `'if' a=b`},
		{[]string{"A=B"}, `'A=B'`},
	} {
		if got := displayCommand(tc.argv); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.argv, got, tc.want)
		}
	}
}

func TestDisplayCommandShellRoundTrip(t *testing.T) {
	argv := []string{"echo", "", "hello world", "it's fine", "$HOME", "$(printf injected)", "`printf injected`", "*.go", "~", "a;b", "a\nb", "a\tb", "a\rb", "\\path\\", "quote'\\\n", "\x1b[31m", "café", "\u0085", "\u202e", "\u200d", "a=b"}
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			path, err := exec.LookPath(shell)
			if err != nil {
				t.Skip(shell + " not installed")
			}
			out, err := exec.Command(path, "-c", "printf '%s\\0' "+displayCommand(argv)).Output()
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
			if !reflect.DeepEqual(got, argv) {
				t.Fatalf("round trip: got %q, want %q", got, argv)
			}
		})
	}
}

func TestElapsed(t *testing.T) {
	start := "2026-01-01T00:00:00.250Z"
	finish := "2026-01-01T00:01:05.999Z"
	invalid := "invalid"
	now := time.Date(2026, 1, 1, 2, 3, 4, 750000000, time.UTC)
	for _, tc := range []struct {
		name string
		job  job
		want string
	}{
		{"running", job{Status: "running", StartedAt: &start}, "2h3m4s"},
		{"success", job{Status: "succeeded", StartedAt: &start, FinishedAt: &finish}, "1m5s"},
		{"failure", job{Status: "failed", StartedAt: &start, FinishedAt: &finish}, "1m5s"},
		{"queued", job{Status: "queued"}, "-"},
		{"missing start", job{Status: "running"}, "-"},
		{"invalid start", job{Status: "running", StartedAt: &invalid}, "-"},
		{"missing finish", job{Status: "succeeded", StartedAt: &start}, "-"},
		{"invalid finish", job{Status: "failed", StartedAt: &start, FinishedAt: &invalid}, "-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := elapsed(tc.job, now); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	if got := elapsed(job{Status: "running", StartedAt: &start}, now.Add(-24*time.Hour)); got != "0s" {
		t.Fatalf("clock skew: %q", got)
	}
}

func TestSubmitPreservesArguments(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "test-key")
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
	args := []string{"--controller", server.URL, "--queue=batch", "sh", "-c", "echo 'hello world'"}
	if err := run(args, &out, &diag); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(command, args[3:]) {
		t.Fatalf("argv: %q", command)
	}
	if out.String() != "new-id\n" {
		t.Fatal(out.String())
	}
}

func TestActions(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "test-key")
	for _, tc := range []struct {
		args         []string
		method, path string
		body         map[string]string
	}{
		{[]string{"-C"}, "POST", "/jobs/clear", nil},
		{[]string{"-r", "abc"}, "DELETE", "/jobs/abc", nil},
		{[]string{"-k", "abc"}, "POST", "/jobs/abc/cancel", nil},
		{[]string{"-u", "abc"}, "POST", "/jobs/abc/urgent", nil},
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
			if err := run(tc.args, &out, &out); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal(calls)
			}
		})
	}
}

func TestDefaultsAndOutput(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "test-key")
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
			if err := run([]string{action}, &out, &diag); err != nil {
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

func TestListProgressAndCancellation(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "test-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"jobs":[{"id":"job","status":"running","progress":0.5,"cancel_requested":1,"command":["sleep","60"]}]}`)
	}))
	defer server.Close()
	t.Setenv("JOBD_CONTROLLER", server.URL)
	t.Setenv("JOBD_QUEUE", "default")
	var out bytes.Buffer
	if err := run([]string{"-l"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out.String(), "\n")
	if strings.Fields(lines[0])[3] != "PROGRESS" {
		t.Fatal(out.String())
	}
	fields := strings.Fields(lines[1])
	if fields[1] != "cancelling" || fields[3] != "50.0%" {
		t.Fatal(out.String())
	}
}

func TestListPagination(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "test-key")
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
	if err := run(nil, &out, &out); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || strings.Count(out.String(), "\n") != 101 {
		t.Fatalf("calls=%d output=%s", calls, &out)
	}
}

func TestErrorsAndNoMutationRetry(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "test-key")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, `{"error":"unavailable"}`, 503) }))
	defer server.Close()
	t.Setenv("JOBD_CONTROLLER", server.URL)
	t.Setenv("JOBD_QUEUE", "default")
	var out bytes.Buffer
	if err := run([]string{"echo", "hello"}, &out, &out); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	for _, args := range [][]string{{"-U", "a"}, {"-C", "a"}, {"-r", "a", "b"}, {"--"}, {"-bad"}, {"--controller"}} {
		if err := run(args, &out, &out); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
	if calls != 1 {
		t.Fatal("invalid arguments made requests")
	}
}
