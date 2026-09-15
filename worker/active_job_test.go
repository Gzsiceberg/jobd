package main

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestActiveJobCancellationBeforeActivation(t *testing.T) {
	for _, fromClaim := range []bool{false, true} {
		var active activeJobState
		ctx, cancel := context.WithCancelCause(context.Background())
		if !fromClaim {
			active.RequestCancellation("1")
		}
		active.Activate("1", cancel, fromClaim)
		if !errors.Is(context.Cause(ctx), errRemoteCancellation) {
			t.Fatal("lost pre-activation cancellation")
		}
		active.Deactivate("1")
		cancel(nil)
	}
}

func TestActiveJobCancellationIsolation(t *testing.T) {
	var active activeJobState
	first, cancelFirst := context.WithCancelCause(context.Background())
	defer cancelFirst(nil)
	next, cancelNext := context.WithCancelCause(context.Background())
	defer cancelNext(nil)
	active.Activate("1", cancelFirst, false)
	active.RequestCancellation("unrelated")
	if first.Err() != nil {
		t.Fatal("unrelated request cancelled job")
	}
	active.RequestCancellation("1")
	if !errors.Is(context.Cause(first), errRemoteCancellation) {
		t.Fatal("active job not cancelled")
	}
	active.Deactivate("1")
	active.Activate("2", cancelNext, false)
	active.RequestCancellation("1")
	active.Deactivate("1") // A stale cleanup must not detach job 2.
	if next.Err() != nil {
		t.Fatal("late request cancelled next job")
	}
	active.RequestCancellation("2")
	if !errors.Is(context.Cause(next), errRemoteCancellation) {
		t.Fatal("stale cleanup detached next job")
	}
}

func TestActivationRacingCancellation(t *testing.T) {
	for range 100 {
		var active activeJobState
		ctx, cancel := context.WithCancelCause(context.Background())
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); active.Activate("1", cancel, false) }()
		go func() { defer wg.Done(); active.RequestCancellation("1") }()
		wg.Wait()
		if !errors.Is(context.Cause(ctx), errRemoteCancellation) {
			t.Fatal("racing request lost")
		}
		active.Deactivate("1")
		cancel(nil)
	}
}
