package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOutputReportedBeforeLaunch(t *testing.T) {
	for _, reject := range []bool{false, true} {
		marker := filepath.Join(t.TempDir(), "started")
		var reported string
		result := Execute(context.Background(), []string{"touch", marker}, time.Millisecond, func(path string) error {
			reported = path
			if _, err := os.Stat(path); err != nil {
				t.Error(err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Error("command started before reporting")
			}
			if reject {
				return fmt.Errorf("controller rejected output")
			}
			return nil
		})
		os.Remove(result.OutputPath)
		if reported == "" || reported != result.OutputPath {
			t.Fatalf("output: %+v", result)
		}
		if reject {
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("executed despite reporting failure")
			}
			if !strings.Contains(result.Error, "controller rejected output") {
				t.Fatal(result.Error)
			}
		} else if result.ExitCode == nil || *result.ExitCode != 0 {
			t.Fatalf("result: %+v", result)
		}
	}
}

func TestExecuteCombinedOutput(t *testing.T) {
	// Output must go specifically to /tmp, even if TMPDIR is configured.
	t.Setenv("TMPDIR", t.TempDir())
	paths := make(map[string]bool)
	for _, code := range []int{0, 3} {
		result := Execute(context.Background(), []string{"sh", "-c", fmt.Sprintf("printf 'stdout\\n'; printf 'stderr\\n' >&2; exit %d", code)}, time.Millisecond)
		t.Cleanup(func() { os.Remove(result.OutputPath) })
		if result.ExitCode == nil || *result.ExitCode != code {
			t.Fatalf("result: %+v", result)
		}
		if filepath.Dir(result.OutputPath) != "/tmp" || !strings.HasPrefix(filepath.Base(result.OutputPath), "jobd-") || !strings.HasSuffix(result.OutputPath, ".log") {
			t.Fatalf("unexpected output path: %q", result.OutputPath)
		}
		if paths[result.OutputPath] {
			t.Fatal("commands reused an output file")
		}
		paths[result.OutputPath] = true
		data, err := os.ReadFile(result.OutputPath)
		if err != nil || string(data) != "stdout\nstderr\n" {
			t.Fatalf("combined output: %q, error: %v", data, err)
		}
		info, err := os.Stat(result.OutputPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Fatalf("output accessible by other users: %v", info.Mode())
		}
	}
}

func TestExecuteExitCodes(t *testing.T) {
	for _, code := range []int{0, 3} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			result := Execute(context.Background(), []string{"sh", "-c", fmt.Sprintf("exit %d", code)}, time.Millisecond)
			if result.ExitCode == nil || *result.ExitCode != code || (result.Error == "") != (code == 0) {
				t.Fatalf("result: %+v", result)
			}
		})
	}
}

func TestExecuteLaunchFailure(t *testing.T) {
	for _, command := range [][]string{nil, {"/nonexistent/jobd-command"}} {
		result := Execute(context.Background(), command, time.Millisecond)
		if result.ExitCode != nil || result.Error == "" {
			t.Fatalf("result: %+v", result)
		}
	}
}

func TestExecuteCancelledBeforeLaunch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	marker := filepath.Join(t.TempDir(), "launched")
	result := Execute(ctx, []string{"touch", marker}, time.Millisecond)
	if result.ExitCode != nil || result.Error == "" {
		t.Fatalf("result: %+v", result)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("launched after cancellation")
	}
}

func awaitFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return strings.TrimSpace(string(data))
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
	return ""
}

func TestExecuteShutdownKillsGroupAfterLeaderExitsZero(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	childFile := filepath.Join(dir, "child")
	leaderFile := filepath.Join(dir, "leader")
	// The child ignores TERM; the leader catches TERM and exits successfully.
	command := []string{"sh", "-c", `
trap 'exit 0' TERM
printf 'before shutdown\n'
sh -c 'trap "" TERM; echo $$ > "$1"; while :; do sleep 1; done' sh "$1" &
echo $$ > "$2"
wait
`, "sh", childFile, leaderFile}
	done := make(chan Result, 1)
	go func() { done <- Execute(ctx, command, 30*time.Millisecond) }()
	leader, err := strconv.Atoi(awaitFile(t, leaderFile))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(-leader, syscall.SIGKILL)
	child, err := strconv.Atoi(awaitFile(t, childFile))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case result := <-done:
		t.Cleanup(func() { os.Remove(result.OutputPath) })
		data, err := os.ReadFile(result.OutputPath)
		if err != nil || !strings.Contains(string(data), "before shutdown\n") {
			t.Fatalf("shutdown output: %q, error: %v", data, err)
		}
		if result.Error == "" || result.ExitCode != nil {
			t.Fatalf("interruption: %+v", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown timed out")
	}
	// An orphan may remain a zombie until the host's init reaps it.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", child))
		if os.IsNotExist(err) {
			return
		}
		if err == nil {
			_, state, ok := strings.Cut(string(data), ") ")
			if ok && strings.HasPrefix(state, "Z ") {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("child survived process-group shutdown")
}

func TestExecuteArgvAndStdin(t *testing.T) {
	result := Execute(context.Background(), []string{"sh", "-c", `test "$1" = 'a; echo injected' && ! read line`, "sh", "a; echo injected"}, time.Millisecond)
	if result.Error != "" {
		t.Fatalf("argv or /dev/null stdin: %+v", result)
	}
}
