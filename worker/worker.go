package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

type Worker struct {
	localQueue        *localQueue
	client            *ControllerClient
	hostname          string
	pollInterval      time.Duration
	heartbeatInterval time.Duration
	shutdownTimeout   time.Duration
	processGrace      time.Duration
	active            activeJobState
}

func (w *Worker) Run(ctx context.Context) error {
	if w.localQueue == nil {
		panic("worker: local queue must be initialized before Run")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := w.localQueue.recover(); err != nil {
		return fmt.Errorf("recover local queue: %w", err)
	}
	if w.client != nil {
		record, err := w.client.Register(ctx, w.hostname)
		if err != nil && !errors.Is(err, errRemoteDisabled) {
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
	}
	for ctx.Err() == nil {
		var job *Job
		var err error
		if w.client != nil && !w.client.remoteDisabled.Load() {
			job, err = w.client.Claim(ctx)
			if err != nil && !errors.Is(err, errRemoteDisabled) {
				return fmt.Errorf("claim: %w", err)
			}
		}
		// Local work can also run while remote claims are paused after a failure.
		var backend jobBackend = w.client
		if job == nil && ctx.Err() == nil {
			job, err = w.localQueue.claim()
			if err != nil {
				return fmt.Errorf("claim local job: %w", err)
			}
			backend = w.localQueue
		}

		if job == nil {
			if err := wait(ctx, w.pollInterval); err != nil {
				return err
			}
			continue
		}
		if err := w.runJob(ctx, *job, backend); err != nil {
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
	}, w.client)
}

func (w *Worker) runJob(ctx context.Context, job Job, backend jobBackend) error {
	slog.Info("Executing job", "job", job.ID, "command", job.Command)
	jobCtx, cancel := context.WithCancelCause(ctx)
	w.active.Activate(job.ID, cancel, job.CancelRequested != 0)
	defer func() {
		w.active.Deactivate(job.ID)
		cancel(nil)
	}()
	result := w.executeJob(jobCtx, job, backend)
	// Wait for acceptance, unless remote work is disabled.
	if err := w.reportResult(ctx, job.ID, result, backend); err != nil {
		return err
	}
	if w.client != nil && backend == w.client && (result.ExitCode == nil || *result.ExitCode != 0 || result.Error != "") {
		w.client.disableRemote(fmt.Errorf("remote job %s failed", job.ID))
	}
	slog.Info("Job finished", "job", job.ID, "exit_code", result.ExitCode, "error", result.Error, "output", result.OutputPath)
	return nil
}

func (w *Worker) reportResult(ctx context.Context, jobID string, result Result, backend jobBackend) error {
	err := backend.Finish(ctx, jobID, result)
	if err != nil && ctx.Err() != nil {
		// Execution is cancelled, but a final report still gets a bounded chance.
		reportCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.shutdownTimeout)
		defer cancel()
		err = backend.Finish(reportCtx, jobID, result)
	}
	if backend == w.client && errors.Is(err, errRemoteDisabled) {
		slog.Error("Remote result not accepted; continuing local work only", "job", jobID, "error", err)
		return nil
	}
	if err != nil {
		return fmt.Errorf("report result for %s: %w", jobID, err)
	}
	return nil
}

func (w *Worker) heartbeatLoop(ctx context.Context) {
	for ctx.Err() == nil && !w.client.remoteDisabled.Load() {
		record, err := w.client.Heartbeat(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Warn("Heartbeat failed", "error", err)
		} else if err == nil && record.CancelJobID != nil {
			w.active.RequestCancellation(*record.CancelJobID)
		}
		if errors.Is(err, errRemoteDisabled) {
			return
		}
		if err := wait(ctx, w.heartbeatInterval); err != nil {
			return
		}
	}
}
