package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestDetachedWorkerLifecycle(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "jobd-worker")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	dir := t.TempDir()
	t.Setenv("JOBD_STATE_DIR", dir)
	t.Setenv("JOBD_API_KEY", "")
	t.Setenv("JOBD_LOCAL_PERSIST", "true")
	call := func(action string) error {
		cmd := exec.Command(binary, action, "--poll-interval", "0.05")
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %w: %s", action, err, output)
		}
		return nil
	}
	t.Cleanup(func() {
		if err := call("--stop"); err != nil {
			t.Error(err)
		}
	})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := call("--start"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	log, err := os.ReadFile(filepath.Join(dir, "worker.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(log), "Worker started") != 1 {
		t.Fatalf("multiple workers: %s", log)
	}
	if _, err := os.Stat(filepath.Join(dir, "control.lock")); !os.IsNotExist(err) {
		t.Fatal("unexpected control lock")
	}

	conn, err := net.Dial("unix", filepath.Join(dir, "local/control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := conn.(*net.UnixConn).SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var cred *syscall.Ucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		cred, socketErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if socketErr != nil {
		t.Fatal(socketErr)
	}
	group, err := syscall.Getpgid(int(cred.Pid))
	if err != nil || group != int(cred.Pid) {
		t.Fatalf("worker not detached: %d %v", group, err)
	}

	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "local/control.sock"))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	res, err := client.Post("http://local/jobs", "application/json", strings.NewReader(`{"command":["sleep","60"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var submitted Job
	err = json.NewDecoder(res.Body).Decode(&submitted)
	res.Body.Close()
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("submit: %v %s", err, res.Status)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var job Job
		if err := daemonRequest(dir, "GET", "/jobs/"+submitted.ID, time.Second, &job); err != nil {
			t.Fatal(err)
		}
		if job.Status == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job did not start")
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := call("--restart"); err != nil {
		t.Fatal(err)
	}
	var stopped Job
	if err := daemonRequest(dir, "GET", "/jobs/"+submitted.ID, time.Second, &stopped); err != nil {
		t.Fatal(err)
	}
	if stopped.OutputPath != "" {
		t.Cleanup(func() { os.Remove(stopped.OutputPath) })
	}
	if stopped.Status != "failed" {
		t.Fatalf("active work not cancelled: %+v", stopped)
	}
	if ready, err := workerAvailable(dir); err != nil || !ready {
		t.Fatalf("restart: %t %v", ready, err)
	}
	for range 2 {
		if err := call("--stop"); err != nil {
			t.Fatal(err)
		}
	}
	if ready, err := workerAvailable(dir); err != nil || ready {
		t.Fatalf("stop: %t %v", ready, err)
	}

	stale, err := net.ListenUnix("unix", &net.UnixAddr{Net: "unix", Name: filepath.Join(dir, "local/control.sock")})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	stale.Close()
	if err := call("--start"); err != nil {
		t.Fatal(err)
	}
	if err := call("--stop"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JOBD_LOCAL_PERSIST", "invalid")
	if err := call("--start"); err == nil {
		t.Fatal("invalid configuration accepted")
	}
	t.Setenv("JOBD_LOCAL_PERSIST", "true")
}

func TestLifecycleFlags(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart"} {
		config, err := parseConfig([]string{"--" + action}, io.Discard)
		if err != nil || config.Action != action {
			t.Fatalf("%s: %+v %v", action, config, err)
		}
	}
	for _, args := range [][]string{{"--start", "--stop"}, {"--restart", "--start"}, {"--stop", "--restart"}} {
		if _, err := parseConfig(args, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestStopAbsentWorker(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	if err := stopWorker(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("stop created state")
	}
}
