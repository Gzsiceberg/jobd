package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type Result struct {
	ExitCode   *int
	Error      string
	OutputPath string
}

// Execute runs argv directly and saves combined stdout/stderr in a retained /tmp log.
func Execute(ctx context.Context, command []string, grace time.Duration, onOutput ...func(string) error) Result {
	return execute(ctx, command, grace, nil, onOutput...)
}

func executionCancellation(ctx context.Context, fallback string) string {
	if errors.Is(context.Cause(ctx), errRemoteCancellation) {
		return errRemoteCancellation.Error()
	}
	return fallback
}

func execute(ctx context.Context, command []string, grace time.Duration, env []string, onOutput ...func(string) error) (result Result) {
	if ctx.Err() != nil {
		return Result{Error: executionCancellation(ctx, "Worker shutting down")}
	}
	if len(command) == 0 {
		return Result{Error: "Empty command"}
	}
	output, err := os.CreateTemp("/tmp", "jobd-*.log")
	if err != nil {
		return Result{Error: truncateError(fmt.Sprintf("Create command output file: %v", err))}
	}
	defer func() {
		result.OutputPath = output.Name()
		if err := output.Close(); err != nil {
			slog.Warn("Closing command output file failed", "output", output.Name(), "error", err)
		}
	}()
	slog.Info("Command output redirected", "output", output.Name())
	for _, notify := range onOutput {
		if err := notify(output.Name()); err != nil {
			return Result{Error: executionCancellation(ctx, truncateError(fmt.Sprintf("Report output path: %v", err)))}
		}
	}
	if ctx.Err() != nil {
		return Result{Error: executionCancellation(ctx, "Worker shut down before execution")}
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env = jobEnvironment(append(os.Environ(), env...))
	cmd.Stdout = output
	cmd.Stderr = output
	// A new session lets shutdown target the group, including child processes.
	// Nil stdin is /dev/null. CommandContext alone would only kill the leader.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return Result{Error: truncateError(err.Error())}
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return processResult(cmd, err)
	case <-ctx.Done():
		// Prefer a result already available when cancellation arrived.
		select {
		case err := <-done:
			return processResult(cmd, err)
		default:
		}
	}

	signalGroup(cmd.Process.Pid, syscall.SIGTERM)
	// Give the whole group time to exit, even if its leader exits immediately.
	time.Sleep(grace)
	signalGroup(cmd.Process.Pid, syscall.SIGKILL)
	<-done
	code := cmd.ProcessState.ExitCode()
	if code == 0 {
		// A command trapping TERM may exit zero, but was still interrupted.
		return Result{Error: executionCancellation(ctx, "Worker shut down during execution")}
	}
	return Result{ExitCode: &code, Error: executionCancellation(ctx, "Worker shut down during execution")}
}

// Defense in depth only: jobs run as the worker user and are not sandboxed.
func jobEnvironment(environment []string) []string {
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		if !strings.HasPrefix(entry, "JOBD_WORKER_TOKEN=") && !strings.HasPrefix(entry, "JOBD_MASTER_KEY=") {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func processResult(cmd *exec.Cmd, err error) Result {
	code := cmd.ProcessState.ExitCode()
	if err == nil {
		return Result{ExitCode: &code}
	}
	return Result{ExitCode: &code, Error: fmt.Sprintf("Process exited with code %d: %s", code, truncateError(err.Error()))}
}

func signalGroup(pid int, signal syscall.Signal) {
	if err := syscall.Kill(-pid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
		// The daemon owns these processes; signal errors are unexpected.
		fmt.Fprintf(os.Stderr, "signal process group %d: %v\n", pid, err)
	}
}

func truncateError(message string) string {
	characters := []rune(message)
	if len(characters) > 4096 {
		return string(characters[:4096])
	}
	return message
}
