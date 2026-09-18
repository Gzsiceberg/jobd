package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerCommands(t *testing.T) {
	for _, action := range []string{"--start", "--stop", "--restart"} {
		t.Run(action, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "args")
			script := `#!/bin/sh
[ "$JOBD_API_KEY" = 'test-secret' ] || exit 90
[ "$JOBD_STATE_DIR" = '/custom/state' ] || exit 91
[ "$JOBD_LOCAL_PERSIST" = true ] || exit 92
printf '%s\n' "$@" > "$CALL_LOG"
printf 'worker output\n'
printf 'worker diagnostic\n' >&2
exit "${WORKER_EXIT:-0}"
`
			if err := os.WriteFile(filepath.Join(dir, "jobd-worker"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			t.Setenv("CALL_LOG", log)
			t.Setenv("JOBD_API_KEY", "test-secret")
			t.Setenv("JOBD_STATE_DIR", "/custom/state")
			t.Setenv("JOBD_LOCAL_PERSIST", "true")
			var out, diagnostic bytes.Buffer
			if err := manageWorker(action, "https://controller.example", "batch", &out, &diagnostic); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != action+"\n--controller\nhttps://controller.example\n--queue\nbatch\n" {
				t.Fatalf("args: %s", data)
			}
			if out.String() != "worker output\n" || diagnostic.String() != "worker diagnostic\n" {
				t.Fatal("worker output not forwarded")
			}
			t.Setenv("WORKER_EXIT", "1")
			if err := manageWorker(action, "https://controller.example", "batch", &out, &diagnostic); err == nil {
				t.Fatal("worker failure ignored")
			}
		})
	}
}

func TestRemoteDoesNotStartWorker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"jobs":[]}`)
	}))
	defer server.Close()
	dir := filepath.Join(t.TempDir(), "absent")
	t.Setenv("JOBD_STATE_DIR", dir)
	t.Setenv("JOBD_API_KEY", "test-key")
	t.Setenv("JOBD_CONTROLLER", server.URL)
	t.Setenv("PATH", t.TempDir())
	var out bytes.Buffer
	if err := run([]string{"-l"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("remote command touched worker state")
	}
}

func TestWorkerControlRejectsArguments(t *testing.T) {
	for _, args := range [][]string{{"--restart", "extra"}, {"--stop", "extra"}, {"--local", "--stop"}} {
		var out bytes.Buffer
		if err := run(args, &out, &out); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestMissingWorkerBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := workerBinary(); err == nil || !strings.Contains(err.Error(), "jobd-worker not found") {
		t.Fatalf("missing binary: %v", err)
	}
}
