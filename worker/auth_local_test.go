package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOutputAuthErrorDoesNotPreventClaimedJobExecution(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			})
			ctx := context.Background()
			if err := client.Output(ctx, "remote", "/tmp/test.log"); !errors.Is(err, errControllerAuth) {
				t.Fatalf("Output must preserve the authentication error: %v", err)
			}
			worker := testWorker(t, client)
			result := worker.executeJob(ctx, Job{ID: "remote", Command: []string{"true"}}, client)
			if result.OutputPath != "" {
				t.Cleanup(func() { os.Remove(result.OutputPath) })
			}
			if result.ExitCode == nil || *result.ExitCode != 0 || result.Error != "" {
				t.Fatalf("claimed job did not execute: %+v", result)
			}
		})
	}
}

func TestAuthenticationRejectionContinuesLocalWork(t *testing.T) {
	for _, endpoint := range []string{"register", "claim", "output", "complete", "fail", "heartbeat", "recovery"} {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
			t.Run(fmt.Sprintf("%s/%d", endpoint, status), func(t *testing.T) {
				q := testLocalQueue(t)
				for i := 0; i < 2; i++ {
					if _, err := q.submit([]string{"true"}); err != nil {
						t.Fatal(err)
					}
				}
				client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					path := r.URL.Path
					if strings.HasSuffix(path, "/"+endpoint) || (endpoint == "recovery" && strings.HasSuffix(path, "/fail")) {
						w.WriteHeader(status)
						return
					}
					if endpoint == "recovery" && strings.HasSuffix(path, "/register") {
						fmt.Fprint(w, `{"current_job_id":"previous"}`)
						return
					}
					if strings.HasSuffix(path, "/claim") {
						command := "true"
						if endpoint == "fail" {
							command = "false"
						}
						json.NewEncoder(w).Encode(map[string]any{"job": Job{ID: "remote", Command: []string{command}}})
						return
					}
					fmt.Fprint(w, `{}`)
				})
				worker := testWorker(t, client)
				worker.localQueue = q
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				done := make(chan struct{})
				go func() {
					defer close(done)
					ticker := time.NewTicker(time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-ctx.Done():
							return
						case <-ticker.C:
							job, err := q.get("local-2")
							if err == nil && job.Status == "succeeded" && client.authRejected.Load() {
								cancel()
								return
							}
						}
					}
				}()
				err := worker.Run(ctx)
				cancel()
				<-done
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("Run: %v", err)
				}
				if !client.authRejected.Load() {
					t.Fatal("remote authentication not disabled")
				}
				for _, job := range queueJobs(t, q) {
					if job.Status != "succeeded" {
						t.Errorf("local job: %+v", job)
					}
					if job.OutputPath != "" {
						os.Remove(job.OutputPath)
					}
				}
			})
		}
	}
}
