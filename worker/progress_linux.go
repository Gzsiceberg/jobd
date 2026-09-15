package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Each job gets a private directory/socket, so late updates cannot reach the next job.
// ACK means the update is buffered in worker memory, not uploaded to the controller.
type ProgressSocket struct {
	Path        string
	dir         string
	listener    *net.UnixListener
	cancel      context.CancelFunc
	done        chan struct{}
	mu          sync.Mutex
	connections map[net.Conn]struct{}
	closing     bool
	handlers    sync.WaitGroup
}

func OpenProgressSocket(parent context.Context, report func(context.Context, float64) error) (*ProgressSocket, error) {
	dir, err := os.MkdirTemp("/tmp", "jobd-progress-")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "progress.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		os.RemoveAll(dir)
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	p := &ProgressSocket{Path: path, dir: dir, listener: listener, cancel: cancel, done: make(chan struct{}), connections: make(map[net.Conn]struct{})}
	go func() {
		defer close(p.done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			if p.closing || len(p.connections) >= 16 {
				p.mu.Unlock()
				conn.Close()
				continue
			}
			p.connections[conn] = struct{}{}
			p.handlers.Add(1)
			p.mu.Unlock()
			go func() {
				defer p.handlers.Done()
				defer func() { conn.Close(); p.mu.Lock(); delete(p.connections, conn); p.mu.Unlock() }()
				scanner := bufio.NewScanner(conn)
				scanner.Buffer(make([]byte, 1024), 4096)
				for {
					conn.SetReadDeadline(time.Now().Add(30 * time.Second))
					if !scanner.Scan() {
						return
					}
					value, err := parseProgress(scanner.Bytes())
					if err == nil {
						err = report(ctx, value)
					}
					response := map[string]any{"ok": err == nil}
					if err != nil {
						response["error"] = err.Error()
					}
					conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
					if json.NewEncoder(conn).Encode(response) != nil {
						return
					}
				}
			}()
		}
	}()
	return p, nil
}

func parseProgress(line []byte) (float64, error) {
	var message struct {
		Progress *float64 `json:"progress"`
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&message); err != nil {
		return 0, fmt.Errorf("invalid progress JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return 0, fmt.Errorf("expected one JSON object per line")
	}
	if message.Progress == nil || math.IsNaN(*message.Progress) || math.IsInf(*message.Progress, 0) || *message.Progress < 0 || *message.Progress > 1 {
		return 0, fmt.Errorf("progress must be a finite number from 0 to 1")
	}
	return *message.Progress, nil
}

// Close cancels callbacks and closes idle clients before joining handlers.
// It must complete before the terminal result is reported.
func (p *ProgressSocket) Close() {
	p.cancel()
	p.mu.Lock()
	p.closing = true
	p.listener.Close()
	for conn := range p.connections {
		conn.Close()
	}
	p.mu.Unlock()
	<-p.done
	p.handlers.Wait()
	os.RemoveAll(p.dir)
}
