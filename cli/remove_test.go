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

func TestRemoveMultiple(t *testing.T) {
	for _, mode := range []string{"remote", "local", "fallback"} {
		for _, explicit := range []bool{false, true} {
			for _, partial := range []bool{false, true} {
				t.Run(mode+map[bool]string{false: "/short", true: "/explicit"}[explicit]+map[bool]string{false: "/success", true: "/partial"}[partial], func(t *testing.T) {
					t.Setenv("JOBD_MASTER_KEY", "")
					t.Setenv("JOBD_WORKER_TOKEN", "auth")
					t.Setenv("JOBD_QUEUE", "batch")
					prefix, idPrefix := "/queues/batch", ""
					if mode != "remote" {
						prefix, idPrefix = "", "local-"
					}
					var calls []string
					handler := func(w http.ResponseWriter, r *http.Request) {
						if r.Method != "POST" {
							t.Errorf("unexpected method %s", r.Method)
						}
						calls = append(calls, r.URL.Path)
						var body struct {
							IDs []string `json:"ids"`
						}
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Fatal(err)
						}
						wantIDs := []string{idPrefix + "1", idPrefix + "2", idPrefix + "3", idPrefix + "4"}
						if !reflect.DeepEqual(body.IDs, wantIDs) {
							t.Errorf("ids=%v", body.IDs)
						}
						if partial {
							json.NewEncoder(w).Encode(map[string]any{"succeeded": []string{wantIDs[0], wantIDs[3]}, "failed": []map[string]string{
								{"id": wantIDs[1], "error": "cannot remove a running job"},
								{"id": wantIDs[2], "error": "job not found"},
							}})
						} else {
							json.NewEncoder(w).Encode(map[string]any{"succeeded": body.IDs, "failed": []any{}})
						}
					}
					if mode == "remote" {
						server := httptest.NewServer(http.HandlerFunc(handler))
						defer server.Close()
						t.Setenv("JOBD_CONTROLLER", server.URL)
					} else {
						t.Setenv("JOBD_STATE_DIR", fakeLocalWorker(t, handler))
						if mode == "fallback" {
							t.Setenv("JOBD_WORKER_TOKEN", "")
						}
					}
					args := []string{"-r"}
					if explicit {
						args = []string{"job", "remove"}
					}
					if mode == "local" {
						args = append([]string{"--local"}, args...)
					}
					want := []string{prefix + "/jobs/remove"}
					for _, id := range []string{"1", "2", "3", "4"} {
						args = append(args, idPrefix+id)
					}
					var out bytes.Buffer
					err := run(args, strings.NewReader(""), &out, io.Discard)
					if partial {
						if err == nil || !strings.Contains(err.Error(), "2 succeeded, 2 failed") || !strings.Contains(err.Error(), idPrefix+"2:") || !strings.Contains(err.Error(), idPrefix+"3:") {
							t.Fatalf("error: %v", err)
						}
						if out.String() != "Removed 2 job(s).\n" {
							t.Fatal(out.String())
						}
					} else if err != nil || out.String() != "Removed 4 job(s).\n" {
						t.Fatalf("output=%q error=%v", out.String(), err)
					}
					if !reflect.DeepEqual(calls, want) {
						t.Fatalf("calls=%v want=%v", calls, want)
					}
				})
			}
		}
	}
}
