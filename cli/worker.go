package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

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
	command.Stdout, command.Stderr = out, diagnostic
	if err := command.Run(); err != nil {
		return fmt.Errorf("jobd-worker %s: %w", action, err)
	}
	return nil
}
