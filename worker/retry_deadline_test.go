package main

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryDeadlineDisablesRemote(t *testing.T) {
	for _, slowRequest := range []bool{false, true} {
		t.Run(map[bool]string{false: "retry wait", true: "in-flight request"}[slowRequest], func(t *testing.T) {
			var calls atomic.Int32
			release := make(chan struct{})
			defer close(release)
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if slowRequest {
					select {
					case <-r.Context().Done():
					case <-release:
					}
					return
				}
				w.WriteHeader(http.StatusServiceUnavailable)
			})
			if client.retryTimeout != 10*time.Minute {
				t.Fatalf("default retry deadline: %s", client.retryTimeout)
			}
			client.retryTimeout = 30 * time.Millisecond
			client.retryInterval = time.Hour
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if _, err := client.Claim(ctx); !errors.Is(err, errRemoteDisabled) {
				t.Fatalf("claim: %v", err)
			}
			if !client.remoteDisabled.Load() {
				t.Fatal("remote work not disabled")
			}
			before := calls.Load()
			if _, err := client.Heartbeat(ctx); !errors.Is(err, errRemoteDisabled) {
				t.Fatalf("heartbeat: %v", err)
			}
			if calls.Load() != before {
				t.Fatal("sent request after remote work was disabled")
			}
		})
	}
}

func TestCallerCancellationDoesNotDisableRemote(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	for _, deadline := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		if !deadline {
			cancel()
		}
		_, err := client.Claim(ctx)
		cancel()
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("claim: %v", err)
		}
		if client.remoteDisabled.Load() {
			t.Fatal("caller cancellation disabled remote work")
		}
	}
}
