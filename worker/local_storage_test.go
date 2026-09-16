package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalPersistConfig(t *testing.T) {
	for _, tc := range []struct {
		value         string
		want, invalid bool
	}{
		{"unset", false, false}, {"false", false, false}, {"0", false, false},
		{"true", true, false}, {"1", true, false}, {"", false, true}, {"yes", false, true},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("JOBD_LOCAL_PERSIST", tc.value)
			if tc.value == "unset" {
				os.Unsetenv("JOBD_LOCAL_PERSIST")
			}
			config, err := parseConfig(nil, io.Discard)
			if (err != nil) != tc.invalid || config.LocalPersist != tc.want {
				t.Fatalf("persist=%v err=%v", config.LocalPersist, err)
			}
		})
	}
}

func TestMemoryQueueDoesNotPersist(t *testing.T) {
	dir := t.TempDir()
	q, err := openLocalQueue(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.submit([]string{"true"}); err != nil {
		t.Fatal(err)
	}
	// Sequential operations must reuse the same connection/database.
	if len(queueJobs(t, q)) != 1 {
		t.Fatal("memory database lost between operations")
	}
	if _, err := os.Stat(filepath.Join(q.dir, "queue.db")); !os.IsNotExist(err) {
		t.Fatalf("created database file: %v", err)
	}
	q.Close()
	q, err = openLocalQueue(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if len(queueJobs(t, q)) != 0 {
		t.Fatal("memory queue survived reopen")
	}
	other, err := openLocalQueue(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	q.submit([]string{"true"})
	if len(queueJobs(t, other)) != 0 {
		t.Fatal("memory queues share a database")
	}
}

func TestMemoryQueueLeavesPersistentDatabaseUntouched(t *testing.T) {
	dir := t.TempDir()
	q, err := openLocalQueue(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.submit([]string{"echo", "saved"}); err != nil {
		t.Fatal(err)
	}
	q.Close()
	path := filepath.Join(dir, "local", "queue.db")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	q, err = openLocalQueue(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(queueJobs(t, q)) != 0 {
		t.Fatal("memory mode loaded persisted jobs")
	}
	q.submit([]string{"echo", "temporary"})
	q.Close()
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("memory mode changed existing database")
	}
	q, err = openLocalQueue(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	jobs := queueJobs(t, q)
	if len(jobs) != 1 || jobs[0].Command[1] != "saved" {
		t.Fatalf("persisted queue: %+v", jobs)
	}
}
