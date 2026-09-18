package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalSocket(t *testing.T) {
	q := testLocalQueue(t)
	path := filepath.Join(q.dir, "control.sock")
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	stale.Close()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	socket, err := openLocalSocket(ctx, q, stop)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v %v", info, err)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second * 5}
	request := func(method, route, body string, status int) map[string]any {
		t.Helper()
		req, err := http.NewRequest(method, "http://local"+route, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != status {
			t.Fatalf("%s %s: %d %s", method, route, res.StatusCode, data)
		}
		var result map[string]any
		if status == 200 {
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	if result := request("GET", "/health", "", 200); result["status"] != "ok" {
		t.Fatalf("health: %+v", result)
	}
	request("POST", "/health", "", 405)
	if result := request("POST", "/jobs", `{"command":["echo","hello"]}`, 200); result["id"] != "local-1" {
		t.Fatalf("submit: %+v", result)
	}
	if result := request("GET", "/jobs", "", 200); len(result["jobs"].([]any)) != 1 {
		t.Fatalf("list: %+v", result)
	}
	request("GET", "/jobs/local-1", "", 200)
	request("GET", "/jobs/latest?kind=added", "", 200)
	request("GET", "/jobs/latest?kind=run", "", 404)
	for _, body := range []string{`{`, `{"command":[]}`, `{"command":["echo"]} {}`, `{"command":["echo"],"directory":"/tmp"}`} {
		request("POST", "/jobs", body, 400)
	}
	for _, query := range []string{"limit=no", "limit=0", "limit=101", "offset=-1", "offset=no"} {
		request("GET", "/jobs?"+query, "", 400)
	}
	request("PUT", "/jobs", "", 405)
	request("GET", "/unknown", "", 404)
	request("GET", "/jobs/local-999", "", 404)
	request("POST", "/jobs/local-1/urgent", "{}", 200)
	request("POST", "/jobs", `{"command":["true"]}`, 200)
	request("POST", "/jobs/swap", `{"first":"local-1"}`, 400)
	request("POST", "/jobs/swap", `{"first":"local-1","second":"local-2"}`, 200)
	claimed, err := q.claim()
	if err != nil || claimed == nil || claimed.ID != "local-2" {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	request("POST", "/jobs/local-2/cancel", "{}", 200)
	job, err := q.get("local-2")
	if err != nil || job.CancelRequested != 1 {
		t.Fatalf("cancel: %+v %v", job, err)
	}
	request("DELETE", "/jobs/local-1", "", 200)
	request("POST", "/jobs/clear", "{}", 200)
	// Largest accepted commands still fit when each byte expands to six JSON bytes.
	body, err := json.Marshal(map[string]any{"command": []string{"echo", strings.Repeat("\x01", (256<<10)-4)}})
	if err != nil {
		t.Fatal(err)
	}
	request("POST", "/jobs", string(body), 200)
	if result := request("POST", "/jobs/remove-all", "{}", 200); result["removed"] != float64(1) || result["kept_running"] != float64(1) {
		t.Fatalf("remove all: %+v", result)
	}
	request("POST", "/jobs", `{"command":["`+strings.Repeat("x", 2<<20)+`"]}`, 400)
	request("GET", "/daemon/stop", "", 405)
	if ctx.Err() != nil {
		t.Fatal("ordinary requests stopped the worker")
	}
	for range 2 {
		if result := request("POST", "/daemon/stop", "", 200); result["ok"] != true {
			t.Fatalf("stop acknowledgement: %+v", result)
		}
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("stop request did not cancel the worker context")
	}
	request("GET", "/health", "", 503)
	socket.Close() // Idempotent, closes idle HTTP clients and removes the socket.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("socket leaked")
	}
}

func TestLocalSocketPreservesFile(t *testing.T) {
	q := testLocalQueue(t)
	path := filepath.Join(q.dir, "control.sock")
	os.WriteFile(path, []byte("keep"), 0600)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	if socket, err := openLocalSocket(ctx, q, stop); err == nil {
		socket.Close()
		t.Fatal("overwrote regular file")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "keep" {
		t.Fatal("file was changed")
	}
}
