package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Lifecycle commands use the same private HTTP socket as local jobs.
func daemonRequest(dir, method, path string, timeout time.Duration, result any) error {
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "local", "control.sock"))
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequest(method, "http://local"+path, nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4097))
	if err != nil {
		return err
	}
	if len(data) > 4096 {
		return fmt.Errorf("worker response exceeds 4 KiB")
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s: %s: %s", method, path, res.Status, strings.TrimSpace(string(data)))
	}
	if result != nil {
		return json.Unmarshal(data, result)
	}
	return nil
}

func workerAvailable(dir string) (bool, error) {
	var health struct {
		Status string `json:"status"`
	}
	if err := daemonRequest(dir, "GET", "/health", time.Second, &health); err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return false, nil
		}
		return false, fmt.Errorf("worker health check failed: %w", err)
	}
	if health.Status != "ok" {
		return false, fmt.Errorf("worker health check returned status %q", health.Status)
	}
	return true, nil
}

func startWorker(config Config) error {
	dir := config.StateDir
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := filepath.Join(dir, "worker.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	command := exec.Command(binary, "--state-dir", dir, "--controller", config.Controller, "--queue", config.Queue,
		"--poll-interval", fmt.Sprint(config.PollInterval.Seconds()),
		"--heartbeat-interval", fmt.Sprint(config.HeartbeatInterval.Seconds()))
	// Start a new session without a controlling terminal. Stdin is /dev/null;
	// stdout/stderr never hold the invoking shell's pipes open. Secrets stay in env.
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		return err
	}
	started := false
	defer func() {
		if !started {
			_ = command.Process.Signal(syscall.SIGTERM)
		}
	}()
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	var exitErr error
	for {
		select {
		case exitErr = <-done:
			// Another launch may have won the daemon lock. Wait for its socket
			// rather than treating this child's exit as a failed CLI command.
			done = nil
		default:
		}
		ready, err := workerAvailable(dir)
		if err != nil {
			return err
		}
		if ready {
			started = true
			return nil
		}
		if time.Now().After(deadline) {
			if done == nil {
				return fmt.Errorf("worker exited during startup (%v); no worker became ready; see %s", exitErr, logPath)
			}
			return fmt.Errorf("worker startup timed out; see %s", logPath)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func ensureWorker(config Config) error {
	ready, err := workerAvailable(config.StateDir)
	if err != nil || ready {
		return err
	}
	return startWorker(config)
}

func stopWorker(dir string) error {
	// Hold the daemon lock once shutdown is complete. Never signal a stored PID.
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EWOULDBLOCK) {
		return err
	}
	if err := daemonRequest(dir, "POST", "/daemon/stop", 15*time.Second, nil); err != nil {
		return fmt.Errorf("stop worker through its socket (older workers must be stopped manually): %w", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("worker did not stop within 30 seconds; no replacement started")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func manageWorker(config Config, out io.Writer) error {
	switch config.Action {
	case "start":
		return ensureWorker(config)
	case "restart":
		// Reject invalid controller settings before stopping a healthy worker.
		if strings.TrimSpace(os.Getenv("JOBD_WORKER_TOKEN")) != "" {
			client, err := NewControllerClient(config.Controller, "", config.Queue, config.PollInterval)
			if err != nil {
				return err
			}
			client.http.CloseIdleConnections()
		}
		if err := stopWorker(config.StateDir); err != nil {
			return err
		}
		if err := startWorker(config); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "Started jobd-worker. Logs:", filepath.Join(config.StateDir, "worker.log"))
		return err
	case "stop":
		if err := stopWorker(config.StateDir); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "Stopped jobd-worker.")
		return err
	default:
		return fmt.Errorf("unknown worker action %q", config.Action)
	}
}
