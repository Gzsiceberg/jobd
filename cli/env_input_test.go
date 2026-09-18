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

func TestEnvValuePreview(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"", "Received 0 characters"},
		{"short-secret", "Received 12 characters"},
		{"abcd-middle-wxyz", "Received 16 characters: \"abcd…wxyz\""},
		{strings.Repeat("界", 13), "Received 13 characters: \"界界界界…界界界界\""},
		{"\x1b[2Jmiddle-wxyz", "Received 15 characters: \"\\x1b[2J…wxyz\""},
	} {
		if got := envValuePreview(tc.value); got != tc.want {
			t.Errorf("preview = %q, want %q", got, tc.want)
		}
	}
}

func TestConfirmedEnvValue(t *testing.T) {
	for _, tc := range []struct {
		name             string
		secrets, answers []string
		want             string
		fail             bool
	}{
		{"default yes", []string{"secret"}, []string{""}, "secret", false},
		{"explicit yes", []string{"secret"}, []string{"YES"}, "secret", false},
		{"retry", []string{"wrong", "correct"}, []string{"n", "y"}, "correct", false},
		{"invalid answer", []string{"secret"}, []string{"maybe", "y"}, "secret", false},
		{"cancel", []string{"secret"}, nil, "", true},
		{"read failure", nil, nil, "", true},
		{"invalid value", []string{"bad\x00value"}, []string{"y"}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := func(values []string) func() (string, error) {
				return func() (string, error) {
					if len(values) == 0 {
						return "", io.EOF
					}
					value := values[0]
					values = values[1:]
					return value, nil
				}
			}
			var prompts strings.Builder
			got, err := readConfirmedEnvValue("API_KEY", &prompts, reader(tc.secrets), reader(tc.answers))
			if (err != nil) != tc.fail || got != tc.want {
				t.Fatalf("unexpected result: %v", err)
			}
			for _, secret := range tc.secrets {
				if strings.Contains(prompts.String(), secret) {
					t.Fatal("full secret leaked")
				}
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
