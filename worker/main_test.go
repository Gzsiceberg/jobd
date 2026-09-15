package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStableIdentityAndLock(t *testing.T) {
	dir := t.TempDir()
	first, err := OpenWorkerIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := OpenWorkerIdentity(dir); err == nil {
		second.Close()
		t.Error("allowed two daemons with the same identity")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := OpenWorkerIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if first.ID != second.ID || len(first.ID) != 36 {
		t.Fatalf("identity changed: %q %q", first.ID, second.ID)
	}
}

func TestExistingIdentity(t *testing.T) {
	dir := t.TempDir()
	id := "de6cfb12-0000-4000-8000-000000000001"
	if err := os.WriteFile(filepath.Join(dir, "worker-id"), []byte(id+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	identity, err := OpenWorkerIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer identity.Close()
	if identity.ID != id {
		t.Fatalf("identity changed: %s", identity.ID)
	}
}

func TestInvalidIdentityIsNotReplaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "worker-id")
	if err := os.WriteFile(path, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		identity, err := OpenWorkerIdentity(dir)
		if err == nil {
			identity.Close()
			t.Fatal("accepted invalid identity")
		}
		if !strings.Contains(err.Error(), "invalid worker identity") {
			t.Fatalf("lock leaked: %v", err)
		}
	}
	data, _ := os.ReadFile(path)
	if string(data) != "invalid" {
		t.Fatal("overwrote invalid identity")
	}
}

func TestConfigCompatibility(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("JOBD_CONTROLLER", "https://controller")
	t.Setenv("JOBD_QUEUE", "batch")
	t.Setenv("JOBD_STATE_DIR", "~/worker-state")
	config, err := parseConfig([]string{"--poll-interval", "0.5", "--heartbeat-interval", "3"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if config.Controller != "https://controller" || config.Queue != "batch" || config.StateDir != filepath.Join(os.Getenv("HOME"), "worker-state") || config.PollInterval != 500*time.Millisecond || config.HeartbeatInterval != 3*time.Second {
		t.Fatalf("config: %+v", config)
	}
	config, err = parseConfig([]string{"--queue", "override"}, io.Discard)
	if err != nil || config.Queue != "override" {
		t.Fatalf("flag override: %+v %v", config, err)
	}
}

func TestInvalidIntervals(t *testing.T) {
	for _, value := range []string{"0", "-1", "NaN", "Inf", "1e100", "1e-100"} {
		for _, flag := range []string{"--poll-interval", "--heartbeat-interval"} {
			if _, err := parseConfig([]string{flag, value}, io.Discard); err == nil {
				t.Errorf("accepted %s=%s", flag, value)
			}
		}
	}
}
