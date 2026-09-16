package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func newLocalClient(stateDir string) (*client, error) {
	if stateDir == "" {
		return nil, fmt.Errorf("state directory must not be empty")
	}
	if stateDir == "~" || strings.HasPrefix(stateDir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		stateDir = filepath.Join(home, strings.TrimPrefix(stateDir, "~"))
	}
	path := filepath.Join(stateDir, "local", "control.sock")
	transport := &http.Transport{
		// Each CLI invocation makes only a few requests. Avoid retaining sockets.
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", path)
			if err != nil {
				return nil, fmt.Errorf("connect to local worker: %w; ensure it is running with the same state directory", err)
			}
			return conn, nil
		},
	}
	return &client{
		base: "http://local", local: true,
		http: &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}
