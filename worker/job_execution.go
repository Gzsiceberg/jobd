package main

import "context"

type jobBackend interface {
	Output(context.Context, string, string) error
	Finish(context.Context, string, Result) error
}

// executeJob owns execution resources, but never sends the terminal report.
// Its caller reports using the worker context, not the cancellable job context.
func (w *Worker) executeJob(ctx context.Context, job Job, backend jobBackend) Result {
	if ctx.Err() != nil {
		return Result{Error: executionCancellation(ctx, "Worker shut down before execution")}
	}
	environment := make([]string, 0, len(job.queueEnv))
	for name, value := range job.queueEnv {
		environment = append(environment, name+"="+value)
	}
	return execute(ctx, job.Command, w.processGrace, environment, func(path string) error {
		return backend.Output(ctx, job.ID, path)
	})
}
