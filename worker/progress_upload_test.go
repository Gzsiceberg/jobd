package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestProgressThreshold(t *testing.T) {
	for _, tc := range []struct {
		current, previous float64
		want              bool
	}{
		{0.05, 0, false}, {0.05001, 0, true}, {0.25, 0.2, false}, {0.251, 0.2, true}, {0.1, 0.5, false},
	} {
		if got := progressAdvanced(tc.current, tc.previous); got != tc.want {
			t.Errorf("%v - %v: %v", tc.current, tc.previous, got)
		}
	}
}

func TestPeriodicUploadAndEarlyThreshold(t *testing.T) {
	uploads := make(chan float64, 10)
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/progress") {
			t.Errorf("wrong endpoint: %s", r.URL.Path)
		}
		var body struct {
			Progress float64 `json:"progress"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		uploads <- body.Progress
		fmt.Fprint(w, `{}`)
	})
	reporter := &progressReporter{client: client, jobID: "1", changed: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	if err := reporter.buffer(ctx, 0.05); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); reporter.uploadLoop(ctx, 200*time.Millisecond) }()
	defer func() { cancel(); <-done }()
	select {
	case <-uploads:
		t.Fatal("exactly 5 points uploaded early")
	case <-time.After(30 * time.Millisecond):
	}
	select {
	case v := <-uploads:
		if v != 0.05 {
			t.Fatal(v)
		}
	case <-time.After(time.Second):
		t.Fatal("no periodic upload")
	}
	if err := reporter.buffer(ctx, 0.11); err != nil {
		t.Fatal(err)
	}
	select {
	case v := <-uploads:
		if v != 0.11 {
			t.Fatal(v)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("threshold did not upload promptly")
	}
}

func TestHeartbeatHasNoProgress(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if len(body) != 0 {
			t.Errorf("heartbeat payload: %v", body)
		}
		fmt.Fprint(w, `{}`)
	})
	if _, err := client.Heartbeat(context.Background()); err != nil {
		t.Fatal(err)
	}
}
