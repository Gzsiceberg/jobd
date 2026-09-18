package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCombinedListPagination(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "test-key")
	servePage := func(source string, calls *int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			offset := *calls * 100
			*calls += 1
			if r.Method != "GET" || r.URL.RawQuery != fmt.Sprintf("limit=100&offset=%d", offset) {
				t.Errorf("request: %s %s", r.Method, r.URL)
			}
			prefix := ""
			if source == "local" {
				prefix = "local-"
			}
			count := 100
			if offset == 100 {
				count = 1
			}
			jobs := make([]job, count)
			for i := range jobs {
				jobs[i] = job{ID: fmt.Sprintf("%s%d", prefix, offset+i+1), Status: "queued", Command: []string{"echo", "hello world"}}
			}
			json.NewEncoder(w).Encode(map[string]any{"jobs": jobs})
		}
	}
	remoteCalls, localCalls := 0, 0
	server := httptest.NewServer(servePage("remote", &remoteCalls))
	defer server.Close()
	dir := fakeLocalWorker(t, servePage("local", &localCalls))
	t.Setenv("JOBD_CONTROLLER", server.URL)
	t.Setenv("JOBD_STATE_DIR", dir)
	// Listing must not invoke a lifecycle command, even with an existing worker.
	t.Setenv("PATH", t.TempDir())
	var out, diagnostic bytes.Buffer
	if err := run(nil, strings.NewReader(""), &out, &diagnostic); err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(rows) != 203 || remoteCalls != 2 || localCalls != 2 {
		t.Fatalf("rows=%d remote=%d local=%d", len(rows), remoteCalls, localCalls)
	}
	if strings.Fields(rows[0])[7] != "COMMAND" {
		t.Fatal(rows[0])
	}
	for i, row := range rows[1:] {
		fields := strings.Fields(row)
		want := fmt.Sprint(i + 1)
		if i >= 101 {
			want = fmt.Sprintf("local-%d", i-100)
		}
		if fields[0] != want {
			t.Fatalf("job order: %s", row)
		}
	}
	if diagnostic.Len() != 0 {
		t.Fatal(diagnostic.String())
	}
}

func TestCombinedListFailures(t *testing.T) {
	for _, tc := range []struct {
		name                                 string
		remoteFails, localFails, localAbsent bool
		wantError                            bool
		warning                              string
	}{
		{"remote only", false, false, true, false, ""},
		{"remote unavailable", true, false, false, false, "remote queue unavailable"},
		{"local unavailable", false, true, false, false, "local queue unavailable"},
		{"both unavailable", true, true, false, true, ""},
		{"remote unavailable without worker", true, false, true, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("JOBD_API_KEY", "test-key")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.remoteFails {
					http.Error(w, "offline", 503)
					return
				}
				fmt.Fprint(w, `{"jobs":[{"id":"1","status":"queued","command":["true"]}]}`)
			}))
			defer server.Close()
			dir := t.TempDir()
			if !tc.localAbsent {
				dir = fakeLocalWorker(t, func(w http.ResponseWriter, r *http.Request) {
					if tc.localFails {
						http.Error(w, "stopping", 503)
						return
					}
					fmt.Fprint(w, `{"jobs":[{"id":"local-1","status":"queued","command":["true"]}]}`)
				})
			}
			t.Setenv("JOBD_STATE_DIR", dir)
			t.Setenv("JOBD_CONTROLLER", server.URL)
			t.Setenv("PATH", t.TempDir())
			var out, diagnostic bytes.Buffer
			err := run([]string{"-l"}, strings.NewReader(""), &out, &diagnostic)
			if (err != nil) != tc.wantError {
				t.Fatalf("error: %v", err)
			}
			if tc.wantError {
				if out.Len() != 0 {
					t.Fatal("failed listing printed incomplete output")
				}
				return
			}
			if tc.warning == "" && diagnostic.Len() != 0 {
				t.Fatal(diagnostic.String())
			}
			if tc.warning != "" && !strings.Contains(diagnostic.String(), tc.warning) {
				t.Fatal(diagnostic.String())
			}
			hasRemote := false
			for _, row := range strings.Split(strings.TrimSpace(out.String()), "\n")[1:] {
				if strings.Fields(row)[0] == "1" {
					hasRemote = true
				}
			}
			if hasRemote == tc.remoteFails {
				t.Fatal("unexpected remote listing", out.String())
			}
			if !tc.localFails && !tc.localAbsent && !strings.Contains(out.String(), "local-1") {
				t.Fatal("local jobs missing")
			}
		})
	}
}

func TestCombinedListDiscardsIncompleteSource(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "test-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("offset") != "0" {
			http.Error(w, "page failed", 503)
			return
		}
		jobs := make([]job, 100)
		for i := range jobs {
			jobs[i] = job{ID: fmt.Sprint(i + 1), Status: "queued", Command: []string{"true"}}
		}
		json.NewEncoder(w).Encode(map[string]any{"jobs": jobs})
	}))
	defer server.Close()
	dir := fakeLocalWorker(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"jobs":[{"id":"local-1","status":"queued","command":["true"]}]}`)
	})
	t.Setenv("JOBD_STATE_DIR", dir)
	t.Setenv("JOBD_CONTROLLER", server.URL)
	t.Setenv("PATH", t.TempDir())
	var out, diagnostic bytes.Buffer
	if err := run(nil, strings.NewReader(""), &out, &diagnostic); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "\n") != 2 || !strings.Contains(out.String(), "local-1") {
		t.Fatal(out.String())
	}
	if !strings.Contains(diagnostic.String(), "page failed") {
		t.Fatal(diagnostic.String())
	}
}

func TestCombinedListRejectsArguments(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "test-key")
	t.Setenv("JOBD_CONTROLLER", "invalid")
	var out bytes.Buffer
	if err := run([]string{"-l", "extra"}, strings.NewReader(""), &out, &out); err == nil || err.Error() != "-l takes no arguments" {
		t.Fatalf("error: %v", err)
	}
}

func TestLocalListingDoesNotContactController(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "test-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("local listing contacted controller") }))
	defer server.Close()
	dir := fakeLocalWorker(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"jobs":[]}`) })
	t.Setenv("JOBD_STATE_DIR", dir)
	t.Setenv("JOBD_CONTROLLER", server.URL)
	var out bytes.Buffer
	if err := run([]string{"--local", "-l"}, strings.NewReader(""), &out, &out); err != nil {
		t.Fatal(err)
	}
}
