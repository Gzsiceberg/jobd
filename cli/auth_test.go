package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIKeyAuthentication(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing bearer key")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	for _, key := range []string{"", "secret"} {
		t.Setenv("JOBD_API_KEY", key)
		c, err := newClient(server.URL, "default")
		if err != nil {
			t.Fatal(err)
		}
		err = c.request("GET", "/jobs", nil, nil)
		if (err != nil) != (key == "") {
			t.Fatalf("unexpected result: %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("got %d requests", calls)
	}
}
