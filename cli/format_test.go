package main

import (
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
