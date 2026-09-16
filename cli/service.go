package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Import named variables, not secret values in argv. The user manager retains
// these values in memory; no credential file is written.
func restartWorker(address, queue string, out, diagnostic io.Writer) error {
	if _, err := newClient(address, queue); err != nil {
		return err
	}
	settings := []string{
		"JOBD_API_KEY=" + strings.TrimSpace(os.Getenv("JOBD_API_KEY")),
		"JOBD_CONTROLLER=" + address,
		"JOBD_QUEUE=" + queue,
		"JOBD_STATE_DIR=" + env("JOBD_STATE_DIR", "~/.local/state/jobd-worker"),
		"JOBD_LOCAL_PERSIST=" + env("JOBD_LOCAL_PERSIST", "false"),
	}
	names := []string{"JOBD_API_KEY", "JOBD_CONTROLLER", "JOBD_QUEUE", "JOBD_STATE_DIR", "JOBD_LOCAL_PERSIST"}
	environment := make([]string, 0, len(os.Environ())+len(settings))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		replace := false
		for _, managed := range names {
			if name == managed {
				replace = true
				break
			}
		}
		if !replace {
			environment = append(environment, entry)
		}
	}
	environment = append(environment, settings...)
	runSystemctl := func(args ...string) error {
		command := exec.Command("systemctl", append([]string{"--user"}, args...)...)
		command.Env = environment
		command.Stdout = out
		command.Stderr = diagnostic
		return command.Run()
	}
	if err := runSystemctl(append([]string{"import-environment"}, names...)...); err != nil {
		return fmt.Errorf("import worker environment into systemd: %w", err)
	}
	if err := runSystemctl("restart", "jobd-worker.service"); err != nil {
		return fmt.Errorf("restart jobd-worker.service (install the user service first): %w", err)
	}
	return nil
}
