package main

import (
	"context"
	"fmt"
)

type jobBackend interface {
	Output(context.Context, string, string) error
	Progress(context.Context, string, float64) error
	Finish(context.Context, string, Result) error
}

// executeJob owns execution resources, but never sends the terminal report.
// Its caller reports using the worker context, not the cancellable job context.
func (w *Worker) executeJob(ctx context.Context, job Job, backend jobBackend) (result Result) {
	if ctx.Err() != nil {
		return Result{Error: executionCancellation(ctx, "Worker shut down before execution")}
	}
	progress, err := openProgressReporter(ctx, backend, job.ID)
	if err != nil {
		return Result{Error: fmt.Sprintf("Create progress socket: %v", err)}
	}
	defer func() {
		finalProgress := progress.Close()
		result.Progress = &finalProgress
	}()
	environment := make([]string, 0, len(job.queueEnv)+1)
	for name, value := range job.queueEnv {
		environment = append(environment, name+"="+value)
	}
	environment = append(environment, "JOBD_PROGRESS_SOCKET="+progress.SocketPath())
	return execute(ctx, job.Command, w.processGrace, environment, func(path string) error {
		return backend.Output(ctx, job.ID, path)
	})
}
