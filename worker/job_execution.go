package main

import (
	"context"
	"fmt"
)

// executeJob owns execution resources, but never sends the terminal report.
// Its caller reports using the worker context, not the cancellable job context.
func (w *Worker) executeJob(ctx context.Context, job Job) (result Result) {
	if ctx.Err() != nil {
		return Result{Error: executionCancellation(ctx, "Worker shut down before execution")}
	}
	progress, err := openProgressReporter(ctx, w.client, job.ID)
	if err != nil {
		return Result{Error: fmt.Sprintf("Create progress socket: %v", err)}
	}
	defer func() {
		finalProgress := progress.Close()
		result.Progress = &finalProgress
	}()
	return execute(ctx, job.Command, w.processGrace, []string{"JOBD_PROGRESS_SOCKET=" + progress.SocketPath()}, func(path string) error {
		return w.client.Output(ctx, job.ID, path)
	})
}
