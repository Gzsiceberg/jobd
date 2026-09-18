package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDispatchPreservesSubmittedArgv(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "auth")
	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/queues/batch/jobs" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		var body struct {
			Command []string `json:"command"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		received = body.Command
		io.WriteString(w, `{"id":"42"}`)
	}))
	defer server.Close()
	t.Setenv("JOBD_CONTROLLER", server.URL)
	t.Setenv("JOBD_QUEUE", "batch")
	for _, tc := range []struct{ args, want []string }{
		{[]string{"echo", "--help", "--queue", "other", ""}, []string{"echo", "--help", "--queue", "other", ""}},
		{[]string{"--", "env", "list"}, []string{"env", "list"}},
		{[]string{"--", "worker", "restart"}, []string{"worker", "restart"}},
		{[]string{"--", "job", "list"}, []string{"job", "list"}},
		{[]string{"--", "help"}, []string{"help"}},
		{[]string{"--", "completion", "bash"}, []string{"completion", "bash"}},
		{[]string{"--", "-executable"}, []string{"-executable"}},
		{[]string{"job", "submit", "--", "env", "--help", ""}, []string{"env", "--help", ""}},
		{[]string{"job", "submit", "echo", "--help", "--queue", "other"}, []string{"echo", "--help", "--queue", "other"}},
		{[]string{"job", "submit", "--", "worker"}, []string{"worker"}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			var out bytes.Buffer
			if err := run(tc.args, strings.NewReader(""), &out, io.Discard); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(received, tc.want) {
				t.Fatalf("got %q; want %q", received, tc.want)
			}
			if out.String() != "42\n" {
				t.Fatal(out.String())
			}
		})
	}
}

func TestInvalidManagementNeverSubmits(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "auth")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; io.WriteString(w, `{}`) }))
	defer server.Close()
	t.Setenv("JOBD_CONTROLLER", server.URL)
	t.Setenv("JOBD_STATE_DIR", filepath.Join(t.TempDir(), "must-not-exist"))
	for _, args := range [][]string{
		{"env", "typo"}, {"worker", "typo"}, {"job", "typo"}, {"completion", "typo"},
		{"env", "list", "extra"}, {"env", "set", "KEY"}, {"env", "set", "KEY", "secret", "--stdin"},
		{"env", "delete"}, {"env", "list", "--bad"}, {"worker", "stop", "extra"},
		{"--local", "worker", "restart"}, {"job", "submit"}, {"job", "swap", "1"},
		{"job", "cancel", "1", "2"}, {"job", "list", "extra"}, {"--local", "env", "list"},
	} {
		if err := run(args, strings.NewReader(""), io.Discard, io.Discard); err == nil {
			t.Errorf("accepted %q", args)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid management made %d requests", calls)
	}
}

func TestControllerAndQueueFlagsAreRejected(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "")
	t.Setenv("PATH", t.TempDir())
	for _, args := range [][]string{
		{"--controller", "https://example.test", "echo"},
		{"--queue=batch", "echo"},
		{"job", "list", "--queue", "batch"},
		{"env", "list", "--controller=https://example.test"},
		{"worker", "restart", "--queue", "batch"},
	} {
		err := run(args, strings.NewReader(""), io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("%q: expected unknown flag/action error, got %v", args, err)
		}
	}
	var out bytes.Buffer
	if err := run([]string{"--help"}, strings.NewReader(""), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "--controller") || strings.Contains(out.String(), "--queue") {
		t.Fatal("help advertises removed flags")
	}
}

func TestManagementHelpAndCompletion(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "")
	t.Setenv("PATH", t.TempDir())
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"env"}, {"env", "set", "--help"}, {"worker"},
		{"job"}, {"help", "env"}, {"completion", "bash"}, {"__complete", "env", ""},
	} {
		var out bytes.Buffer
		if err := run(args, strings.NewReader(""), &out, io.Discard); err != nil {
			t.Fatalf("%q: %v", args, err)
		}
		if out.Len() == 0 {
			t.Fatalf("%q: no help/completion output", args)
		}
	}
}

func TestExplicitJobActions(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "auth")
	t.Setenv("JOBD_STATE_DIR", t.TempDir())
	for _, tc := range []struct {
		args         []string
		method, path string
	}{
		{[]string{"clear"}, "POST", "/jobs/clear"},
		{[]string{"remove", "1"}, "DELETE", "/jobs/1"},
		{[]string{"cancel", "1"}, "POST", "/jobs/1/cancel"},
		{[]string{"urgent", "1"}, "POST", "/jobs/1/urgent"},
		{[]string{"swap", "1", "2"}, "POST", "/jobs/swap"},
		{[]string{"output", "1"}, "GET", "/jobs/1"},
		{[]string{"list"}, "GET", "/jobs"},
	} {
		t.Run(tc.args[0], func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.Path != "/queues/batch"+tc.path {
					t.Errorf("%s %s", r.Method, r.URL)
				}
				io.WriteString(w, `{"jobs":[],"output_path":"/tmp/job.log"}`)
			}))
			defer server.Close()
			t.Setenv("JOBD_CONTROLLER", server.URL)
			t.Setenv("JOBD_QUEUE", "batch")
			args := append([]string{"job"}, tc.args...)
			if err := run(args, strings.NewReader(""), io.Discard, io.Discard); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal(calls)
			}
		})
	}
}

func TestEnvManagementEndToEnd(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "auth")
	values := map[string]string{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer auth" {
			t.Error("missing auth")
		}
		switch r.Method {
		case "PUT":
			var body struct {
				Value string `json:"value"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			values[r.URL.Path] = body.Value
		case "DELETE":
			delete(values, r.URL.Path)
		}
		io.WriteString(w, `{"names":["API_KEY"]}`)
	}))
	defer server.Close()
	transport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = transport })
	t.Setenv("JOBD_CONTROLLER", server.URL)
	t.Setenv("JOBD_QUEUE", "batch")
	var out bytes.Buffer
	if err := run([]string{"env", "set", "API_KEY", "--stdin"}, strings.NewReader("secret\n"), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if values["/queues/batch/env/API_KEY"] != "secret\n" || out.Len() != 0 {
		t.Fatal("incorrect secret upload")
	}
	if err := run([]string{"env", "list"}, strings.NewReader(""), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if out.String() != "API_KEY\n" {
		t.Fatal(out.String())
	}
	if err := run([]string{"env", "delete", "API_KEY"}, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(values) != 0 {
		t.Fatal("delete not performed")
	}
}

func TestExplicitLocalSubmissionAndReservedNameEscape(t *testing.T) {
	var command []string
	dir := fakeLocalWorker(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/jobs" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		var body struct {
			Command []string `json:"command"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		command = body.Command
		io.WriteString(w, `{"id":"local-1"}`)
	})
	t.Setenv("JOBD_STATE_DIR", dir)
	t.Setenv("JOBD_CONTROLLER", "invalid-no-http")
	t.Setenv("JOBD_API_KEY", "not-for-local")
	for _, args := range [][]string{
		{"--local", "--", "env", "--help"},
		{"job", "submit", "--local", "--", "env", "--help"},
	} {
		if err := run(args, strings.NewReader(""), io.Discard, io.Discard); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(command, []string{"env", "--help"}) {
			t.Fatal(command)
		}
	}
}

func TestWorkerManagementDispatch(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	if err := os.WriteFile(filepath.Join(dir, "jobd-worker"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CALL_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("CALL_LOG", log)
	t.Setenv("JOBD_CONTROLLER", "https://example.test")
	t.Setenv("JOBD_QUEUE", "batch")
	for _, action := range []string{"start", "restart", "stop"} {
		if err := run([]string{"worker", action}, strings.NewReader(""), io.Discard, io.Discard); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != fmt.Sprintf("--%s\n--controller\nhttps://example.test\n--queue\nbatch\n", action) {
			t.Fatal(string(data))
		}
	}
}
