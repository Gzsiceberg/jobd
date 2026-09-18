package main

import (
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestReadEnvAssignments(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		input string
		want  []string
	}{
		{"mixed", []string{"A", "B=fixed", "C"}, "hello world\r\nvalue=extra\n", []string{"A=hello world", "B=fixed", "C=value=extra"}},
		{"empty", []string{"A"}, "\n", []string{"A="}},
		{"limit", []string{"A"}, strings.Repeat("x", 4096) + "\r\n", []string{"A=" + strings.Repeat("x", 4096)}},
		{"missing", []string{"A"}, "", nil},
		{"missing second", []string{"A", "B"}, "first\n", nil},
		{"oversized", []string{"A"}, strings.Repeat("x", 5000), nil},
		{"invalid name", []string{"BAD-NAME"}, "secret\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readEnvAssignments(tc.args, strings.NewReader(tc.input), io.Discard)
			if tc.want == nil {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("unexpected result or error: %v", err)
			}
		})
	}
}

func TestEnvInputEnterWithoutEOF(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	go func() { _, _ = io.WriteString(writer, "secret\n") }()
	// The writer remains open: reading must finish on newline, not EOF.
	got, err := readEnvAssignments([]string{"API_KEY"}, reader, io.Discard)
	if err != nil || len(got) != 1 || got[0] != "API_KEY=secret" {
		t.Fatalf("line input failed: %v", err)
	}
}
