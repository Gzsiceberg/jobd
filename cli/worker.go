package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Remote scheduling controls never start or stop the local daemon.
func setWorkersPaused(c *client, ids []string, paused bool, out io.Writer) error {
	if strings.TrimSpace(os.Getenv("JOBD_MASTER_KEY")) == "" {
		return fmt.Errorf("JOBD_MASTER_KEY is required to pause or resume workers")
	}
	action, state := "resume", "enabled"
	if paused {
		action, state = "pause", "paused"
	}
	var result batchResult[struct {
		ID       string `json:"worker_id"`
		Hostname string `json:"hostname"`
	}]
	if err := c.request(http.MethodPost, "/workers/"+action, map[string]any{"ids": ids}, &result); err != nil {
		return err
	}
	for _, worker := range result.Succeeded {
		if _, err := fmt.Fprintf(out, "Worker %s (%q): new remote assignments %s.\n", worker.ID, worker.Hostname, state); err != nil {
			return err
		}
	}
	return result.err(action)
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
