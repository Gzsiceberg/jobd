package main

import (
	"context"
	"testing"
)

func TestRetryFailedJobs(t *testing.T) {
	q := testLocalQueue(t)
	a, _ := q.submit([]string{"false"})
	q.claim()
	code := 1
	if err := q.Finish(context.Background(), a.ID, Result{ExitCode: &code, Error: "failed"}); err != nil {
		t.Fatal(err)
	}
	b, _ := q.submit([]string{"true"})
	if _, err := q.retry(b.ID); err == nil {
		t.Fatal("retried queued job")
	}
	if _, err := q.retry("local-999"); err == nil {
		t.Fatal("retried missing job")
	}
	result, err := q.retry(a.ID)
	if err != nil || result.Retried != 1 {
		t.Fatalf("retry: %+v %v", result, err)
	}
	reset, _ := q.get(a.ID)
	if reset.Status != "queued" || reset.StartedAt != nil || reset.FinishedAt != nil || reset.ExitCode != nil || reset.Error != "" || reset.WorkerID != "" || reset.OutputPath != "" || reset.CancelRequested != 0 {
		t.Fatalf("reset: %+v", reset)
	}
	next, _ := q.claim()
	if next.ID != b.ID {
		t.Fatal("retry jumped ahead of queued work")
	}
	q.Finish(context.Background(), b.ID, Result{ExitCode: &code, Error: "failed"})
	result, err = q.retry("")
	if err != nil || result.Retried != 1 {
		t.Fatalf("retry all: %+v %v", result, err)
	}
	result, err = q.retry("")
	if err != nil || result.Retried != 0 {
		t.Fatalf("empty retry all: %+v %v", result, err)
	}
	next, _ = q.claim()
	if next.ID != a.ID {
		t.Fatal("retry order changed")
	}
}
