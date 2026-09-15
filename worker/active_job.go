package main

import (
	"context"
	"errors"
	"sync"
)

var errRemoteCancellation = errors.New("Job cancelled by user")

// activeJobState is shared by the sequential job loop and heartbeat goroutine.
// Keep the ID and cancellation function together so a request cannot cancel
// the next job. Remember requests arriving just before activation as well.
type activeJobState struct {
	mu          sync.Mutex
	id          string
	cancel      context.CancelCauseFunc
	requestedID string
}

func (a *activeJobState) Activate(id string, cancel context.CancelCauseFunc, requested bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.id, a.cancel = id, cancel
	if requested || a.requestedID == id {
		cancel(errRemoteCancellation)
	}
}

func (a *activeJobState) Deactivate(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.id == id {
		a.id, a.cancel = "", nil
	}
}

func (a *activeJobState) RequestCancellation(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requestedID = id
	if a.id == id && a.cancel != nil {
		a.cancel(errRemoteCancellation)
	}
}
