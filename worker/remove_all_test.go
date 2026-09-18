package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveAllKeepsRunningJobsAndLogs(t *testing.T) {
	q := testLocalQueue(t)
	ctx := context.Background()
	log := filepath.Join(t.TempDir(), "keep.log")
	if err := os.WriteFile(log, []byte("output"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, code := range []int{0, 1} {
		job, err := q.submit([]string{"true"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.claim(); err != nil {
			t.Fatal(err)
		}
		if err := q.Output(ctx, job.ID, log); err != nil {
			t.Fatal(err)
		}
		if err := q.Finish(ctx, job.ID, Result{ExitCode: &code}); err != nil {
			t.Fatal(err)
		}
	}
	running, _ := q.submit([]string{"sleep", "60"})
	if _, err := q.claim(); err != nil {
		t.Fatal(err)
	}
	if err := q.requestCancel(running.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 150; i++ {
		if _, err := q.submit([]string{"true"}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := q.removeAll()
	if err != nil || result.Removed != 152 || result.KeptRunning != 1 {
		t.Fatalf("result: %+v, %v", result, err)
	}
	jobs := queueJobs(t, q)
	if len(jobs) != 1 || jobs[0].ID != running.ID || jobs[0].Status != "running" || jobs[0].CancelRequested != 1 {
		t.Fatalf("running job changed: %+v", jobs)
	}
	if data, err := os.ReadFile(log); err != nil || string(data) != "output" {
		t.Fatal("output file removed")
	}
	result, err = q.removeAll()
	if err != nil || result.Removed != 0 || result.KeptRunning != 1 {
		t.Fatalf("repeat: %+v, %v", result, err)
	}
	zero := 0
	if err := q.Finish(ctx, running.ID, Result{ExitCode: &zero}); err != nil {
		t.Fatal(err)
	}
	result, err = q.removeAll()
	if err != nil || result.Removed != 1 || result.KeptRunning != 0 {
		t.Fatalf("finished: %+v, %v", result, err)
	}
	result, err = q.removeAll()
	if err != nil || result.Removed != 0 || result.KeptRunning != 0 {
		t.Fatalf("empty: %+v, %v", result, err)
	}
	next, err := q.submit([]string{"next"})
	if err != nil || next.ID != "local-154" {
		t.Fatalf("ID reused: %+v, %v", next, err)
	}
}
