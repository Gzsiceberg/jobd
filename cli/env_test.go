package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQueueEnvironmentCLI(t *testing.T) {
	var method, path, value string
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer auth" {
			t.Error("missing authentication")
		}
		requests++
		method, path = r.Method, r.URL.Path
		if r.Method == "PUT" {
			var body struct {
				Value string `json:"value"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			value = body.Value
		}
		io.WriteString(w, `{"names":["API_KEY"]}`)
	}))
	defer server.Close()
	c := &client{base: server.URL + "/queues/batch", apiKey: "auth", http: server.Client()}
	var out bytes.Buffer
	if err := runEnv(c, "set", []string{"API_KEY=secret\n"}, &out); err != nil {
		t.Fatal(err)
	}
	if method != "PUT" || path != "/queues/batch/env/API_KEY" || value != "secret\n" || out.Len() != 0 {
		t.Fatal("incorrect secret update")
	}
	if err := runEnv(c, "list", nil, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "API_KEY\n" {
		t.Fatal("listing must contain names only")
	}
	if err := runEnv(c, "delete", []string{"API_KEY"}, &out); err != nil {
		t.Fatal(err)
	}
	if method != "DELETE" {
		t.Fatal(method)
	}
	before := requests
	for _, args := range [][]string{nil, {"BAD-NAME=value"}, {"API_KEY", "secret"}, {"=secret"}, {"VALID=secret", "INVALID"}, {"VALID=secret", "BAD=x\x00y"}} {
		if err := runEnv(c, "set", args, &out); err == nil {
			t.Fatal("invalid arguments accepted")
		}
	}
	if requests != before {
		t.Fatal("invalid batch sent requests before validation finished")
	}
	for _, value := range []string{"x\x00y", strings.Repeat("x", 4097), "\xff"} {
		if err := runEnv(c, "set", []string{"KEY=" + value}, &out); err == nil {
			t.Fatal("invalid value accepted")
		}
	}
	if err := runEnv(c, "set", []string{"JOBD_CUSTOM=allowed"}, &out); err != nil {
		t.Fatal(err)
	}
	if path != "/queues/batch/env/JOBD_CUSTOM" || value != "allowed" {
		t.Fatal("JOBD_ variable not submitted")
	}
	c.base = "http://example.test/queues/a"
	if err := runEnv(c, "set", []string{"KEY=value"}, &out); err == nil {
		t.Fatal("HTTP accepted")
	}
}

func TestSecretErrorsDoNotEchoResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, "echoed-sensitive-value")
	}))
	defer server.Close()
	c := &client{base: server.URL, apiKey: "auth", http: server.Client()}
	err := runEnv(c, "set", []string{"KEY=echoed-sensitive-value"}, io.Discard)
	if err == nil || strings.Contains(err.Error(), "sensitive-value") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestSecretActionsNeverFallBackToLocal(t *testing.T) {
	t.Setenv("JOBD_WORKER_TOKEN", "")
	if err := run([]string{"env", "list"}, strings.NewReader(""), io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "JOBD_WORKER_TOKEN") {
		t.Fatalf("error: %v", err)
	}
	if err := run([]string{"--local", "env", "list"}, strings.NewReader(""), io.Discard, io.Discard); err == nil {
		t.Fatal("local secrets accepted")
	}
}
