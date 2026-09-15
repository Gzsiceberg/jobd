package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestProgressSocketProtocolAndCleanup(t *testing.T) {
	var values []float64
	p, err := OpenProgressSocket(context.Background(), func(ctx context.Context, v float64) error { values = append(values, v); return nil })
	if err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{p.Path: 0600, filepath.Dir(p.Path): 0700} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("permissions %s: %v %v", path, info, err)
		}
	}
	conn, err := net.Dial("unix", p.Path)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	scanner := bufio.NewScanner(conn)
	for _, tc := range []struct {
		line string
		ok   bool
	}{
		{`{"progress":0.25}`, true}, {`{"progress":0.5}`, true},
		{`{}`, false}, {`{"progress":null}`, false}, {`{"progress":-1}`, false},
		{`{"progress":1.1}`, false}, {`{"progress":"0.5"}`, false}, {`{"progress":NaN}`, false},
		{`{"progress":0.5,"job_id":"other"}`, false}, {`{"progress":0.5} {}`, false},
		{`{"progress":1}`, true},
	} {
		fmt.Fprintln(conn, tc.line)
		if !scanner.Scan() {
			t.Fatalf("missing ack: %v", scanner.Err())
		}
		var response struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.OK != tc.ok || (!tc.ok && response.Error == "") {
			t.Fatalf("%s: %+v", tc.line, response)
		}
	}
	conn.Close()
	p.Close()
	if !reflect.DeepEqual(values, []float64{0.25, 0.5, 1}) {
		t.Fatal(values)
	}
	if _, err := os.Stat(filepath.Dir(p.Path)); !os.IsNotExist(err) {
		t.Fatal("socket directory retained", err)
	}
}

func TestProgressCloseCancelsReportAndIdleConnections(t *testing.T) {
	started := make(chan struct{})
	p, err := OpenProgressSocket(context.Background(), func(ctx context.Context, v float64) error { close(started); <-ctx.Done(); return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	idle, err := net.Dial("unix", p.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Close()
	conn, err := net.Dial("unix", p.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintln(conn, `{"progress":0.5}`)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("report not started")
	}
	done := make(chan struct{})
	go func() { p.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("close hung")
	}
}

func TestProgressOversizedLineRejected(t *testing.T) {
	p, err := OpenProgressSocket(context.Background(), func(context.Context, float64) error { t.Error("oversized input forwarded"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	conn, err := net.Dial("unix", p.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	fmt.Fprintln(conn, strings.Repeat("x", 8192))
	if bufio.NewScanner(conn).Scan() {
		t.Fatal("oversized line accepted")
	}
}
