package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
)

type jobBackend interface {
	Output(context.Context, string, string) error
	Finish(context.Context, string, Result) error
}

// executeJob owns execution resources, but never sends the terminal report.
// Its caller reports using the worker context, not the cancellable job context.
func (w *Worker) executeJob(ctx context.Context, job Job, backend jobBackend) (result Result) {
	if ctx.Err() != nil {
		return Result{Error: executionCancellation(ctx, "Worker shut down before execution")}
	}
	if len(job.Command) == 0 {
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
	// Disabling remote requests must not prevent an already claimed job from executing.
	if err := backend.Output(ctx, job.ID, output.Name()); err != nil && !(backend == w.client && errors.Is(err, errRemoteDisabled)) {
		return Result{Error: executionCancellation(ctx, truncateError(fmt.Sprintf("Report output path: %v", err)))}
	}
	environment := make([]string, 0, len(job.queueEnv))
	for name, value := range job.queueEnv {
		environment = append(environment, name+"="+value)
	}
	return execute(ctx, job.Command, w.processGrace, environment, output)
}
