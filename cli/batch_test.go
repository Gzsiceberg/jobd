package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestJobBatches(t *testing.T) {
	for _, local := range []bool{false, true} {
		for _, command := range [][]string{{"-u"}, {"job", "urgent"}, {"job", "retry"}} {
			for _, rejected := range []int{0, 1, 3} {
				t.Run(fmt.Sprintf("local=%t/%s/rejected=%d", local, strings.Join(command, "-"), rejected), func(t *testing.T) {
					t.Setenv("JOBD_MASTER_KEY", "admin")
					t.Setenv("JOBD_QUEUE", "default")
					action := "urgent"
					if command[len(command)-1] == "retry" {
						action = "retry"
					}
					calls := 0
					handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						if r.Method != "POST" || strings.TrimPrefix(r.URL.Path, "/queues/default") != "/jobs/"+action {
							t.Errorf("request: %s %s", r.Method, r.URL)
						}
						var body struct {
							IDs []string `json:"ids"`
						}
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !reflect.DeepEqual(body.IDs, []string{"3", "5", "2"}) {
							t.Errorf("body: %+v %v", body, err)
						}
						if rejected == 3 {
							fmt.Fprint(w, `{"succeeded":[],"failed":[{"id":"3","error":"invalid state"},{"id":"5","error":"invalid state"},{"id":"2","error":"invalid state"}]}`)
						} else if rejected == 1 {
							fmt.Fprint(w, `{"succeeded":["3","2"],"failed":[{"id":"5","error":"invalid state"}]}`)
						} else {
							fmt.Fprint(w, `{"succeeded":["3","5","2"],"failed":[]}`)
						}
					})
					var args []string
					if local {
						t.Setenv("JOBD_STATE_DIR", fakeLocalWorker(t, handler))
						args = append(args, "--local")
					} else {
						server := httptest.NewServer(handler)
						defer server.Close()
						t.Setenv("JOBD_CONTROLLER", server.URL)
					}
					args = append(args, command...)
					args = append(args, "3", "5", "2")
					var out bytes.Buffer
					err := run(args, nil, &out, &out)
					if rejected == 3 {
						if err == nil || !strings.Contains(err.Error(), "0 succeeded, 3 failed: 3: invalid state; 5: invalid state; 2: invalid state") {
							t.Fatalf("all-failed result: %v", err)
						}
						if !strings.Contains(out.String(), "0 ") {
							t.Fatalf("missing zero-success summary: %s", out.String())
						}
					} else if rejected == 1 {
						if err == nil || !strings.Contains(err.Error(), "2 succeeded, 1 failed: 5: invalid state") {
							t.Fatalf("partial result: %v", err)
						}
					} else if err != nil {
						t.Fatal(err)
					}
					if calls != 1 {
						t.Fatalf("requests: %d", calls)
					}
				})
			}
		}
	}
}
