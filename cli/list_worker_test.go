package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestWorkerLabels(t *testing.T) {
	for _, tc := range []struct {
		name string
		ids  []string
		want []string
	}{
		{"short", []string{"worker", ""}, []string{"worker", ""}},
		{"unique", []string{"d9e2c122-d837-4258-b9f1-76bc97121efd", "12345678-abcd-4258-b9f1-76bc97121efd"}, []string{"d9e2c122", "12345678"}},
		{"repeated", []string{"d9e2c122-d837-4258-b9f1-76bc97121efd", "d9e2c122-d837-4258-b9f1-76bc97121efd"}, []string{"d9e2c122", "d9e2c122"}},
		{"collision", []string{"d9e2c122-d837-4258-b9f1-76bc97121efd", "d9e2c122-a837-4258-b9f1-76bc97121efd"}, []string{"d9e2c122-d", "d9e2c122-a"}},
		{"prefix", []string{"abcdefgh", "abcdefghijk"}, []string{"abcdefgh", "abcdefghi"}},
		{"late collision", []string{"d9e2c122-d837-4258-b9f1-76bc97121efd", "d9e2c122-d837-4258-b9f1-76bc97121efe"}, []string{"d9e2c122-d837-4258-b9f1-76bc97121efd", "d9e2c122-d837-4258-b9f1-76bc97121efe"}},
		{"three neighbors", []string{"abcdefgh-xyz", "abcdefgh-xya", "abcdefgh-abc"}, []string{"abcdefgh-xyz", "abcdefgh-xya", "abcdefgh-a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jobs := make([]job, len(tc.ids))
			for i := range tc.ids {
				jobs[i].WorkerID = &tc.ids[i]
			}
			labels := workerLabels(jobs)
			for i, id := range tc.ids {
				if labels[id] != tc.want[i] {
					t.Fatalf("%s: got %q, want %q", id, labels[id], tc.want[i])
				}
			}
		})
	}
}

func TestPrintJobsWorkerLabelsAcrossQueues(t *testing.T) {
	first, second := "d9e2c122-d837-4258-b9f1-76bc97121efd", "d9e2c122-a837-4258-b9f1-76bc97121efd"
	var out bytes.Buffer
	remote := []job{{ID: "1", Status: "queued", WorkerID: &first}, {ID: "2", Status: "queued", WorkerID: &first}, {ID: "3", Status: "queued"}}
	local := []job{{ID: "local-1", Status: "queued", WorkerID: &second}}
	if err := printJobs(&out, remote, local); err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSpace(out.String()), "\n")
	for i, want := range []string{"d9e2c122-d", "d9e2c122-d", "-", "d9e2c122-a"} {
		if got := strings.Fields(rows[i+1])[4]; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	if *remote[0].WorkerID != first || len(first) != 36 {
		t.Fatal("listing changed worker identity")
	}
}
