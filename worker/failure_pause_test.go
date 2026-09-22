package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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

func TestCancellationContinuesRemoteClaims(t *testing.T) {
	for _, preCancelled := range []bool{true, false} {
		t.Run(fmt.Sprintf("before-start=%v", preCancelled), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			marker := filepath.Join(t.TempDir(), "started")
			var claims, reports atomic.Int32
			var completed atomic.Bool
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/claim"):
					switch claims.Add(1) {
					case 1:
						job := Job{ID: "cancelled", Command: []string{"sh", "-c", `echo ready > "$1"; exec sleep 60`, "sh", marker}}
						if preCancelled {
							job.CancelRequested = 1
						}
						json.NewEncoder(w).Encode(map[string]any{"job": job})
					case 2:
						if reports.Load() != 1 {
							t.Error("claimed before cancellation report")
						}
						json.NewEncoder(w).Encode(map[string]any{"job": Job{ID: "next", Command: []string{"true"}}})
					default:
						fmt.Fprint(w, `{"job":null}`)
						cancel()
					}
				case strings.HasSuffix(r.URL.Path, "/heartbeat"):
					if _, err := os.Stat(marker); err == nil && reports.Load() == 0 {
						fmt.Fprint(w, `{"cancel_job_id":"cancelled"}`)
					} else {
						fmt.Fprint(w, `{}`)
					}
				case strings.HasSuffix(r.URL.Path, "/fail"):
					var body struct {
						Error string `json:"error"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body.Error != errRemoteCancellation.Error() {
						t.Errorf("report: %+v", body)
					}
					reports.Add(1)
					fmt.Fprint(w, `{}`)
				case strings.HasSuffix(r.URL.Path, "/complete"):
					completed.Store(true)
					fmt.Fprint(w, `{}`)
				default:
					fmt.Fprint(w, `{}`)
				}
			})
			if err := testWorker(t, client).Run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("run: %v", err)
			}
			if claims.Load() != 3 || reports.Load() != 1 || !completed.Load() || client.remoteDisabled.Load() {
				t.Fatalf("claims=%d reports=%d completed=%v disabled=%v", claims.Load(), reports.Load(), completed.Load(), client.remoteDisabled.Load())
			}
			if preCancelled {
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatal("cancelled job launched")
				}
			}
		})
	}
}

func TestLateCancellationDoesNotMaskFailure(t *testing.T) {
	var worker *Worker
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/fail") {
			worker.active.RequestCancellation("failed")
		}
		fmt.Fprint(w, `{}`)
	})
	worker = testWorker(t, client)
	if err := worker.runJob(context.Background(), Job{ID: "failed", Command: []string{"false"}}, client); err != nil {
		t.Fatal(err)
	}
	if !client.remoteDisabled.Load() {
		t.Fatal("late cancellation masked execution failure")
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
