package main

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestMissingKeySelectsLocalMode(t *testing.T) {
	for _, key := range []string{"", "   "} {
		t.Run("key="+key, func(t *testing.T) {
			dir := fakeLocalWorker(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "GET /jobs":
					fmt.Fprint(w, `{"jobs":[]}`)
				case "POST /jobs":
					fmt.Fprint(w, `{"id":"local-1"}`)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
			})
			t.Setenv("JOBD_WORKER_TOKEN", key)
			t.Setenv("JOBD_STATE_DIR", dir)
			t.Setenv("JOBD_CONTROLLER", "invalid")
			for _, args := range [][]string{nil, {"-l"}, {"--local", "-l"}, {"echo", "hello"}} {
				var out, diagnostic bytes.Buffer
				if err := run(args, strings.NewReader(""), &out, &diagnostic); err != nil {
					t.Fatal(err)
				}
				if len(args) > 0 && args[0] == "echo" {
					if out.String() != "local-1\n" || diagnostic.Len() != 0 {
						t.Fatalf("submission: %q %q", out.String(), diagnostic.String())
					}
				} else {
					if !strings.Contains(out.String(), "ID") || strings.Contains(out.String(), "Warning") {
						t.Fatalf("listing: %q", out.String())
					}
					if !strings.Contains(diagnostic.String(), "JOBD_WORKER_TOKEN") || !strings.Contains(diagnostic.String(), "jobd worker restart") {
						t.Fatalf("warning: %q", diagnostic.String())
					}
				}
			}
		})
	}
}
