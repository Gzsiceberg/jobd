package main

import (
	"context"
	"sync"
	"time"
)

// progressReporter belongs to one job. Socket callbacks only update its buffer;
// the uploader is the only goroutine that sends intermediate progress over HTTP.
type progressReporter struct {
	client      *ControllerClient
	jobID       string
	mu          sync.Mutex
	highest     float64
	changed     chan struct{}
	socket      *ProgressSocket
	stopUploads context.CancelFunc
	uploadDone  chan struct{}
	closeOnce   sync.Once
}

func openProgressReporter(ctx context.Context, client *ControllerClient, jobID string) (*progressReporter, error) {
	p := &progressReporter{client: client, jobID: jobID, changed: make(chan struct{}, 1), uploadDone: make(chan struct{})}
	var err error
	p.socket, err = OpenProgressSocket(ctx, p.buffer)
	if err != nil {
		return nil, err
	}
	uploadCtx, cancel := context.WithCancel(ctx)
	p.stopUploads = cancel
	go func() { defer close(p.uploadDone); p.uploadLoop(uploadCtx, 10*time.Second) }()
	return p, nil
}

func (p *progressReporter) SocketPath() string { return p.socket.Path }

func (p *progressReporter) buffer(ctx context.Context, value float64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if value > p.highest {
		p.highest = value
		select {
		case p.changed <- struct{}{}:
		default:
		}
	}
	return nil
}

func (p *progressReporter) snapshot() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.highest
}

// Close returns the final buffered value only after all producers and uploads
// have stopped. Call this before reporting the terminal result or starting a new job.
func (p *progressReporter) Close() float64 {
	p.closeOnce.Do(func() {
		p.socket.Close()
		p.stopUploads()
		<-p.uploadDone
	})
	return p.snapshot()
}
