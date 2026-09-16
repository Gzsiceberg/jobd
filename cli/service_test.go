package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestartWorker(t *testing.T) {
	for _, tc := range []struct {
		name, key, failure string
		wantCalls          int
	}{
		{"with key", "secret", "", 2},
		{"without key clears old environment", "", "", 2},
		{"import fails", "secret", "import-environment", 1},
		{"restart fails", "secret", "restart", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			logPath := filepath.Join(dir, "calls")
			script := `#!/bin/sh
printf '%s\n' "$*" >> "$CALL_LOG"
[ "$JOBD_API_KEY" = "$EXPECTED_KEY" ] || exit 90
[ "$JOBD_CONTROLLER" = 'https://controller.example' ] || exit 91
[ "$JOBD_QUEUE" = 'batch' ] || exit 92
[ "$JOBD_STATE_DIR" = '/tmp/custom-worker' ] || exit 93
[ "$JOBD_LOCAL_PERSIST" = "$EXPECTED_PERSIST" ] || exit 94
[ "$2" != "$FAIL_ACTION" ] || exit 1
`
			if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			t.Setenv("CALL_LOG", logPath)
			t.Setenv("JOBD_API_KEY", tc.key)
			t.Setenv("EXPECTED_KEY", tc.key)
			t.Setenv("FAIL_ACTION", tc.failure)
			t.Setenv("JOBD_STATE_DIR", "/tmp/custom-worker")
			t.Setenv("JOBD_LOCAL_PERSIST", "true")
			t.Setenv("EXPECTED_PERSIST", "true")
			if tc.key == "" {
				os.Unsetenv("JOBD_LOCAL_PERSIST")
				t.Setenv("EXPECTED_PERSIST", "false")
			}
			var out bytes.Buffer
			err := run([]string{"--controller", "https://controller.example", "--queue", "batch", "--restart"}, &out, &out)
			if (err != nil) != (tc.failure != "") {
				t.Fatalf("unexpected result: %v", err)
			}
			data, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			calls := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(calls) != tc.wantCalls {
				t.Fatalf("calls: %q", calls)
			}
			if calls[0] != "--user import-environment JOBD_API_KEY JOBD_CONTROLLER JOBD_QUEUE JOBD_STATE_DIR JOBD_LOCAL_PERSIST" {
				t.Fatalf("import: %q", calls[0])
			}
			if len(calls) == 2 && calls[1] != "--user restart jobd-worker.service" {
				t.Fatalf("restart: %q", calls[1])
			}
			if strings.Contains(string(data), "secret") {
				t.Fatal("key leaked into arguments")
			}
		})
	}
}

func TestRestartWorkerRejectsArguments(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"--restart", "extra"}, &out, &out); err == nil {
		t.Fatal("expected argument error")
	}
}
