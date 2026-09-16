package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSocketBuffersWithoutHTTPAndFinalFailureFlushes(t *testing.T) {
	dir := t.TempDir()
	socketFile, gate := filepath.Join(dir, "socket"), filepath.Join(dir, "finish")
	reports := make(chan float64, 1)
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/output"):
			var body struct {
				OutputPath string `json:"output_path"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			t.Cleanup(func() { os.Remove(body.OutputPath) })
		case strings.HasSuffix(r.URL.Path, "/fail"):
			var body struct {
				Progress float64 `json:"progress"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			reports <- body.Progress
		default:
			t.Errorf("socket update triggered HTTP: %s", r.URL.Path)
		}
		fmt.Fprint(w, `{}`)
	})
	worker := testWorker(t, client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- worker.runJob(ctx, Job{ID: "job", Command: []string{"sh", "-c", `printf '%s' "$JOBD_PROGRESS_SOCKET" > "$1"; while [ ! -f "$2" ]; do sleep 0.01; done; exit 7`, "sh", socketFile, gate}}, client)
	}()
	var path []byte
	for len(path) == 0 && ctx.Err() == nil {
		path, _ = os.ReadFile(socketFile)
		time.Sleep(time.Millisecond)
	}
	conn, err := net.Dial("unix", string(path))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	scanner := bufio.NewScanner(conn)
	for _, value := range []float64{0.01, 0.025, 0.03, 0.05, 0.02} {
		fmt.Fprintf(conn, "{\"progress\":%g}\n", value)
		if !scanner.Scan() || !strings.Contains(scanner.Text(), `"ok":true`) {
			t.Fatalf("missing local ack: %s %v", scanner.Text(), scanner.Err())
		}
	}
	os.WriteFile(gate, nil, 0600)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case value := <-reports:
		if value != 0.05 {
			t.Fatalf("final progress: %v", value)
		}
	default:
		t.Fatal("missing final flush")
	}
}

func TestProgressRetriesSnapshotThenUploadsNewerBuffer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	snapshots := make(chan float64, 100)
	attempt := 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Progress float64 `json:"progress"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		snapshots <- body.Progress
		attempt++
		if attempt == 1 {
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, `{}`)
	})
	reporter := &progressReporter{client: client, jobID: "job", changed: make(chan struct{}, 1)}
	if err := reporter.buffer(ctx, 0.5); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { reporter.uploadLoop(ctx, time.Hour); close(done) }()
	defer func() { cancel(); <-done }()
	select {
	case first := <-snapshots:
		if first != 0.5 {
			t.Fatal(first)
		}
	case <-ctx.Done():
		t.Fatal("no progress upload")
	}
	if err := reporter.buffer(ctx, 0.9); err != nil {
		t.Fatal(err)
	}
	select {
	case retry := <-snapshots:
		if retry != 0.5 {
			t.Fatalf("retry mutated: %+v", retry)
		}
	case <-ctx.Done():
		t.Fatal("no retry")
	}
	for {
		select {
		case next := <-snapshots:
			if next == 0.9 {
				return
			}
		case <-ctx.Done():
			t.Fatal("newer buffered update lost")
			return
		}
	}
}
