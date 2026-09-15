package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"
)

func TestProgressReporterBufferIsMonotonicAndCoalesced(t *testing.T) {
	p := &progressReporter{changed: make(chan struct{}, 1)}
	var wg sync.WaitGroup
	for i := 0; i <= 100; i++ {
		wg.Add(1)
		go func(value float64) {
			defer wg.Done()
			if err := p.buffer(context.Background(), value); err != nil {
				t.Error(err)
			}
		}(float64(i) / 100)
	}
	wg.Wait()
	if p.snapshot() != 1 {
		t.Fatal("highest update lost")
	}
	if len(p.changed) != 1 {
		t.Fatal("notifications not coalesced")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.buffer(cancelled, 1); err == nil {
		t.Fatal("accepted update after cancellation")
	}
}

func TestProgressReporterCloseJoinsUploadAndIsolatesNextJob(t *testing.T) {
	started := make(chan struct{})
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	})
	p, err := openProgressReporter(context.Background(), client, "1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	idle, err := net.Dial("unix", p.SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Close()
	if err := p.buffer(context.Background(), 0.5); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upload not started")
	}
	closed := make(chan float64, 1)
	go func() { closed <- p.Close() }()
	select {
	case value := <-closed:
		if value != 0.5 {
			t.Fatalf("final progress %v", value)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel/join upload")
	}
	if _, err := os.Stat(p.SocketPath()); !os.IsNotExist(err) {
		t.Fatal("socket remains after Close")
	}
	if p.Close() != 0.5 {
		t.Fatal("Close not idempotent")
	}
	next, err := openProgressReporter(context.Background(), client, "2")
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if next.snapshot() != 0 || next.SocketPath() == p.SocketPath() {
		t.Fatal("next job inherited progress/socket")
	}
	if _, err := net.Dial("unix", p.SocketPath()); err == nil {
		t.Fatal("old job socket still accessible")
	}
}
