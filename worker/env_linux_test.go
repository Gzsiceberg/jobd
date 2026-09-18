package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestExecuteJobInjectsSecretsWithoutChangingWorkerEnvironment(t *testing.T) {
	t.Setenv("API_KEY", "worker-value")
	backend := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		io.WriteString(w, `{}`)
	})
	worker := &Worker{processGrace: time.Millisecond}
	job := Job{ID: "1", Command: []string{"sh", "-c", `test "$API_KEY" = queue-value && test "$JOBD_CUSTOM" = allowed`}, queueEnv: map[string]string{"API_KEY": "queue-value", "JOBD_CUSTOM": "allowed"}}
	result := worker.executeJob(context.Background(), job, backend)
	os.Remove(result.OutputPath)
	if result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("injection failed: %+v", result)
	}
	if os.Getenv("API_KEY") != "worker-value" {
		t.Fatal("worker environment changed")
	}
	job.queueEnv = nil
	job.Command = []string{"sh", "-c", `test "$API_KEY" = worker-value`}
	result = worker.executeJob(context.Background(), job, backend)
	os.Remove(result.OutputPath)
	if result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("secret leaked to next/local job: %+v", result)
	}
}
