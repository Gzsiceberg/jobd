package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Process in order, retaining earlier successes when a later request fails.
func setWorkersPaused(c *client, ids []string, paused bool, out io.Writer) error {
	action := "resume"
	if paused {
		action = "pause"
	}
	for i, id := range ids {
		if err := setWorkerPaused(c, id, paused, out); err != nil {
			return fmt.Errorf("%s worker %s (%d earlier request(s) succeeded): %w", action, id, i, err)
		}
	}
	return nil
}

// Remote scheduling controls never start or stop the local daemon.
func setWorkerPaused(c *client, id string, paused bool, out io.Writer) error {
	if strings.TrimSpace(os.Getenv("JOBD_MASTER_KEY")) == "" {
		return fmt.Errorf("JOBD_MASTER_KEY is required to pause or resume workers")
	}
	action, state := "resume", "enabled"
	if paused {
		action, state = "pause", "paused"
	}
	var worker struct {
		ID       string `json:"worker_id"`
		Hostname string `json:"hostname"`
	}
	if err := c.request(http.MethodPost, "/workers/"+url.PathEscape(id)+"/"+action, nil, &worker); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "Worker %s (%q): new remote assignments %s.\n", worker.ID, worker.Hostname, state)
	return err
}

func workerBinary() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(filepath.Dir(self), "jobd-worker")
	if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
		return candidate, nil
	}
	binary, err := exec.LookPath("jobd-worker")
	if err != nil {
		return "", fmt.Errorf("jobd-worker not found beside jobd or in PATH; install both binaries: %w", err)
	}
	return binary, nil
}

// The worker owns detachment, readiness, logs and shutdown. The CLI only passes
// configuration through its command interface; credentials stay in the environment.
func manageWorker(action, address, queue string, out, diagnostic io.Writer) error {
	binary, err := workerBinary()
	if err != nil {
		return err
	}
	command := exec.Command(binary, action, "--controller", address, "--queue", queue)
	// The encryption/admin credential must never reach the worker daemon.
	command.Env = []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "JOBD_MASTER_KEY=") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Stdout, command.Stderr = out, diagnostic
	if err := command.Run(); err != nil {
		return fmt.Errorf("jobd-worker %s: %w", action, err)
	}
	return nil
}
