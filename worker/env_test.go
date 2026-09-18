package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClaimQueueEnvironmentIsMemoryOnly(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "auth")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer auth" {
			t.Error("missing auth")
		}
		io.WriteString(w, `{"job":{"id":"1","command":["true"]},"environment":{"API_KEY":"secret","JOBD_CUSTOM":"allowed"}}`)
	}))
	defer server.Close()
	client, err := NewControllerClient(server.URL, "worker", "queue", time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	client.http = server.Client()
	job, err := client.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if job.queueEnv["API_KEY"] != "secret" || job.queueEnv["JOBD_CUSTOM"] != "allowed" {
		t.Fatal("environment not attached")
	}
	encoded, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "API_KEY") {
		t.Fatal("secret serialized in job record")
	}
}

func TestClaimSupportsEscapedEnvironmentWithinLimits(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "auth")
	environment := make(map[string]string)
	for i := 0; i < 64; i++ {
		environment[fmt.Sprintf("KEY_%d", i)] = strings.Repeat("\x01", 4096)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"job": map[string]string{"id": "1"}, "environment": environment})
	}))
	defer server.Close()
	client, _ := NewControllerClient(server.URL, "worker", "queue", time.Millisecond)
	client.http = server.Client()
	job, err := client.Claim(context.Background())
	if err != nil || len(job.queueEnv) != 64 {
		t.Fatalf("valid environment rejected: %v", err)
	}
}

func TestClaimEnvironmentRequiresHTTPS(t *testing.T) {
	t.Setenv("JOBD_API_KEY", "auth")
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"job":{"id":"1"},"environment":{"KEY":"secret"}}`)
	})
	if _, err := client.Claim(context.Background()); err == nil {
		t.Fatal("accepted secrets over HTTP")
	}
}
