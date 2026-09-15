package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type Worker struct {
	client            *ControllerClient
	hostname          string
	pollInterval      time.Duration
	heartbeatInterval time.Duration
	shutdownTimeout   time.Duration
	processGrace      time.Duration
}

func (w *Worker) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	record, err := w.client.Register(ctx, w.hostname)
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}

	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		w.heartbeatLoop(ctx)
	}()
	defer func() {
		cancel()
		<-heartbeatDone
	}()

	if err := w.recoverPreviousJob(ctx, record); err != nil {
		return err
	}
	for ctx.Err() == nil {
		job, err := w.client.Claim(ctx)
		if err != nil {
			return fmt.Errorf("claim: %w", err)
		}
		if job == nil {
			if err := wait(ctx, w.pollInterval); err != nil {
				return err
			}
			continue
		}
		if err := w.runJob(ctx, *job); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (w *Worker) recoverPreviousJob(ctx context.Context, record WorkerRecord) error {
	if record.CurrentJobID == nil {
		return nil
	}
	// Never replay a command whose outcome a previous daemon could not report.
	return w.reportResult(ctx, *record.CurrentJobID, Result{
		Error: "Worker restarted; previous outcome unknown",
	})
}

func (w *Worker) runJob(ctx context.Context, job Job) error {
	slog.Info("Executing job", "job", job.ID, "command", job.Command)
	if err := w.client.Progress(ctx, job.ID, 0); err != nil {
		if ctx.Err() != nil {
			return w.reportResult(ctx, job.ID, Result{Error: "Worker shut down before execution"})
		}
		return fmt.Errorf("initial progress for %s: %w", job.ID, err)
	}
	result := Execute(ctx, job.Command, w.processGrace)
	// Do not claim another job until this result is accepted.
	if err := w.reportResult(ctx, job.ID, result); err != nil {
		return err
	}
	slog.Info("Job finished", "job", job.ID, "exit_code", result.ExitCode, "error", result.Error, "output", result.OutputPath)
	return nil
}

func (w *Worker) reportResult(ctx context.Context, jobID string, result Result) error {
	err := w.client.Finish(ctx, jobID, result)
	if err != nil && ctx.Err() != nil {
		// Execution is cancelled, but a final report still gets a bounded chance.
		reportCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.shutdownTimeout)
		defer cancel()
		err = w.client.Finish(reportCtx, jobID, result)
	}
	if err != nil {
		return fmt.Errorf("report result for %s: %w", jobID, err)
	}
	return nil
}

func (w *Worker) heartbeatLoop(ctx context.Context) {
	for ctx.Err() == nil {
		if err := w.client.Heartbeat(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("Heartbeat failed", "error", err)
		}
		if err := wait(ctx, w.heartbeatInterval); err != nil {
			return
		}
	}
}
