package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func testLocalQueue(t *testing.T) *localQueue {
	t.Helper()
	q, err := openLocalQueue(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Close() })
	return q
}
func queueJobs(t *testing.T, q *localQueue) []Job {
	t.Helper()
	jobs, err := q.list(1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	return jobs
}

func TestLocalQueuePersistence(t *testing.T) {
	dir := t.TempDir()
	q, err := openLocalQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := q.submit([]string{"echo", "a b", ""})
	b, _ := q.submit([]string{"false"})
	claimed, err := q.claim()
	if err != nil || claimed.ID != a.ID {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	if q.remove(a.ID) == nil {
		t.Fatal("removed running job")
	}
	q.Output(context.Background(), a.ID, "/tmp/local.log")
	q.Close()
	q, err = openLocalQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if err := q.recover(); err != nil {
		t.Fatal(err)
	}
	recovered, _ := q.get(a.ID)
	if recovered.Status != "failed" || recovered.Error == "" || recovered.OutputPath != "/tmp/local.log" {
		t.Fatalf("recovery: %+v", recovered)
	}
	claimed, err = q.claim()
	if err != nil || claimed.ID != b.ID {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	zero := 0
	if err := q.Finish(context.Background(), b.ID, Result{ExitCode: &zero}); err != nil {
		t.Fatal(err)
	}
	if err := q.clear(); err != nil {
		t.Fatal(err)
	}
	if len(queueJobs(t, q)) != 0 {
		t.Fatal("clear failed")
	}
	next, err := q.submit([]string{"echo"})
	if err != nil || next.ID != "local-3" {
		t.Fatalf("sequence: %+v %v", next, err)
	}
	info, err := os.Stat(q.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v %v", info, err)
	}
}

func TestLocalQueueActions(t *testing.T) {
	q := testLocalQueue(t)
	a, _ := q.submit([]string{"echo", "a"})
	b, _ := q.submit([]string{"echo", "b"})
	c, _ := q.submit([]string{"echo", "c"})
	if err := q.reorder(c.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := q.reorder(c.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	jobs := queueJobs(t, q)
	if jobs[0].ID != b.ID || jobs[1].ID != a.ID || jobs[2].ID != c.ID {
		t.Fatalf("order: %+v", jobs)
	}
	latest, _ := q.latest("added")
	if latest.ID != c.ID {
		t.Fatal("reorder changed latest added")
	}
	page, err := q.list(1, 1)
	if err != nil || len(page) != 1 || page[0].ID != a.ID {
		t.Fatalf("pagination: %v %v", page, err)
	}
	claimed, _ := q.claim()
	if claimed.ID != b.ID {
		t.Fatal("claim ignored order")
	}
	latest, _ = q.latest("run")
	if latest.ID != b.ID {
		t.Fatal("latest run")
	}
	if q.reorder(b.ID, a.ID) == nil || q.remove(b.ID) == nil || q.requestCancel(a.ID) == nil {
		t.Fatal("running/queued safeguards")
	}
	if q.reorder(a.ID, "local-999") == nil || queueJobs(t, q)[1].ID != a.ID {
		t.Fatal("invalid swap changed order")
	}
	q.Progress(context.Background(), b.ID, 0.5)
	q.Progress(context.Background(), b.ID, 0.2)
	var cancelled string
	q.cancel = func(id string) { cancelled = id }
	if err := q.requestCancel(b.ID); err != nil {
		t.Fatal(err)
	}
	job, _ := q.get(b.ID)
	if cancelled != b.ID || job.CancelRequested != 1 || job.Progress != 0.5 {
		t.Fatalf("cancel/progress: %+v", job)
	}
	code, progress := 7, 0.75
	if err := q.Finish(context.Background(), b.ID, Result{ExitCode: &code, Progress: &progress}); err != nil {
		t.Fatal(err)
	}
	job, _ = q.get(b.ID)
	if job.Status != "failed" || job.Progress != 0.75 {
		t.Fatalf("finish: %+v", job)
	}
	q.clear()
	if len(queueJobs(t, q)) != 2 {
		t.Fatal("clear removed pending work")
	}
}

func TestLocalConcurrentSubmit(t *testing.T) {
	q := testLocalQueue(t)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := q.submit([]string{"echo"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	jobs := queueJobs(t, q)
	if len(jobs) != 30 {
		t.Fatalf("jobs: %d", len(jobs))
	}
	seen := map[string]bool{}
	for _, job := range jobs {
		if seen[job.ID] {
			t.Fatal("duplicate ID")
		}
		seen[job.ID] = true
	}
}

func TestLocalQueueValidation(t *testing.T) {
	q := testLocalQueue(t)
	for _, command := range [][]string{nil, {""}, {"echo", "a\x00b"}} {
		if _, err := q.submit(command); err == nil {
			t.Fatal("invalid command accepted")
		}
	}
	for _, id := range []string{"", "local-0", "local--1", "local-01", "1 OR 1=1"} {
		if _, err := q.get(id); err == nil {
			t.Fatal("invalid ID accepted")
		}
	}
	q.submit([]string{"true"})
	q.claim()
	q.submit([]string{"true"})
	q.clear()
	if len(queueJobs(t, q)) != 2 {
		t.Fatal("clear removed unfinished jobs")
	}
	q.Close()
	if err := os.WriteFile(q.path, []byte("not a database"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := openLocalQueue(filepath.Dir(filepath.Dir(q.path))); err == nil {
		t.Fatal("accepted corrupt database")
	}
}

func TestLocalSwapRollback(t *testing.T) {
	q := testLocalQueue(t)
	a, _ := q.submit([]string{"true"})
	b, _ := q.submit([]string{"true"})
	_, err := q.db.Exec(`CREATE TRIGGER reject_swap BEFORE UPDATE OF queue_position ON jobs WHEN OLD.sequence=2 BEGIN SELECT RAISE(ABORT,'test failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if q.reorder(a.ID, b.ID) == nil {
		t.Fatal("expected write failure")
	}
	jobs := queueJobs(t, q)
	if jobs[0].ID != a.ID || jobs[1].ID != b.ID {
		t.Fatal("failed swap partially committed")
	}
}
