package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRemoteFailureReportsThenDisablesRemoteButRunsLocalJobs(t *testing.T) {
	for _, command := range [][]string{{"false"}, {"/nonexistent-jobd-command"}} {
		t.Run(command[0], func(t *testing.T) {
			q := testLocalQueue(t)
			q.submit([]string{"false"})
			q.submit([]string{"true"})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var claims, reports atomic.Int32
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/claim"):
					if claims.Add(1) > 1 {
						t.Error("claimed remote work after failure")
						cancel()
					}
					json.NewEncoder(w).Encode(map[string]any{"job": Job{ID: "remote", Command: command}})
				case strings.HasSuffix(r.URL.Path, "/fail"):
					reports.Add(1)
					fmt.Fprint(w, `{}`)
				default:
					fmt.Fprint(w, `{}`)
				}
			})
			worker := testWorker(t, client)
			worker.localQueue = q
			done := make(chan struct{})
			go func() {
				defer close(done)
				for ctx.Err() == nil {
					job, _ := q.get("local-2")
					if job.Status == "succeeded" {
						cancel()
						return
					}
					if err := wait(ctx, time.Millisecond); err != nil {
						return
					}
				}
			}()
			defer func() { cancel(); <-done }()
			if err := worker.Run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("run: %v", err)
			}
			if claims.Load() != 1 || reports.Load() != 1 {
				t.Fatalf("claims=%d reports=%d", claims.Load(), reports.Load())
			}
			first, _ := q.get("local-1")
			second, _ := q.get("local-2")
			if first.Status != "failed" || second.Status != "succeeded" {
				t.Fatalf("local statuses: %s, %s", first.Status, second.Status)
			}
			if !client.remoteDisabled.Load() {
				t.Fatal("remote work not disabled after failure report")
			}
			if _, err := client.Heartbeat(context.Background()); !errors.Is(err, errRemoteDisabled) {
				t.Fatalf("heartbeat after failure: %v", err)
			}
		})
	}
}

func TestLocalFailureDoesNotPauseRemoteClaims(t *testing.T) {
	q := testLocalQueue(t)
	q.submit([]string{"false"})
	job, err := q.claim()
	if err != nil {
		t.Fatal(err)
	}
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected remote request") })
	worker := testWorker(t, client)
	if err := worker.runJob(context.Background(), *job, q); err != nil {
		t.Fatal(err)
	}
	if client.remoteDisabled.Load() {
		t.Fatal("local failure paused remote claims")
	}
}
