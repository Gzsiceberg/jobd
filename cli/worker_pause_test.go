package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestWorkerPauseResume(t *testing.T) {
	t.Setenv("JOBD_MASTER_KEY", "admin")
	t.Setenv("JOBD_QUEUE", "batch")
	var paths []string
	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer admin" {
			t.Errorf("unexpected request: %s %v", r.Method, r.Header)
		}
		paths = append(paths, r.URL.Path)
		var body struct {
			IDs []string `json:"ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		received = body.IDs
		succeeded := []map[string]string{}
		failed := []map[string]string{}
		for _, id := range body.IDs {
			if id == "ambiguous" {
				failed = append(failed, map[string]string{"id": id, "error": "Ambiguous worker prefix: abc-1, abc-2"})
			} else {
				succeeded = append(succeeded, map[string]string{"worker_id": id + "-full", "hostname": "host"})
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"succeeded": succeeded, "failed": failed})
	}))
	defer server.Close()
	t.Setenv("JOBD_CONTROLLER", server.URL)
	for _, action := range []string{"pause", "resume"} {
		for _, ids := range [][]string{{"abcdef"}, {"first", "second"}, {"first", "ambiguous", "last"}, {"ambiguous"}} {
			start := len(paths)
			var out bytes.Buffer
			err := run(append([]string{"worker", action}, ids...), nil, &out, io.Discard)
			count := len(ids)
			if ids[0] == "ambiguous" {
				count = 0
				if err == nil || !strings.Contains(err.Error(), "0 succeeded, 1 failed: ambiguous:") {
					t.Fatalf("all-failed error: %v", err)
				}
			} else if len(ids) == 3 {
				count--
				if err == nil || !strings.Contains(err.Error(), "2 succeeded, 1 failed: ambiguous: Ambiguous worker prefix: abc-1, abc-2") {
					t.Fatalf("error: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(received, ids) || len(paths) != start+1 || paths[start] != "/queues/batch/workers/"+action {
				t.Fatalf("requests: %v, IDs: %v", paths[start:], received)
			}
			if strings.Count(out.String(), "new remote assignments") != count || (count > 0 && !strings.Contains(out.String(), ids[0]+`-full ("host")`)) {
				t.Fatal(out.String())
			}
		}
	}
	count := len(paths)
	for _, args := range [][]string{{"worker", "pause"}, {"worker", "resume"}, {"--local", "worker", "pause", "a"}} {
		if err := run(args, nil, io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	t.Setenv("JOBD_MASTER_KEY", "")
	t.Setenv("JOBD_WORKER_TOKEN", "worker-token")
	if err := run([]string{"worker", "pause", "abcdef"}, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "JOBD_MASTER_KEY") {
		t.Fatalf("error: %v", err)
	}
	if len(paths) != count {
		t.Fatal("invalid invocation made requests")
	}
}
