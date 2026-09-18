package main

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestListedCommand(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want string
	}{
		{"short", []string{"echo", "hello world"}, "echo 'hello world'"},
		{"at limit", []string{strings.Repeat("a", commandDisplayLimit)}, strings.Repeat("a", commandDisplayLimit)},
		{"over limit", []string{strings.Repeat("a", commandDisplayLimit+1)}, strings.Repeat("a", commandDisplayLimit-3) + "..."},
		{"unicode", []string{"echo", strings.Repeat("界", commandDisplayLimit)}, "echo '" + strings.Repeat("界", commandDisplayLimit-9) + "..."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := listedCommand(tc.argv)
			if got != tc.want {
				t.Fatalf("got %q; want %q", got, tc.want)
			}
			if !utf8.ValidString(got) || utf8.RuneCountInString(got) > commandDisplayLimit {
				t.Fatalf("invalid display: %q", got)
			}
		})
	}
}

func TestPrintJobsTruncatesCommands(t *testing.T) {
	command := []string{"echo", strings.Repeat("x", 200)}
	var out bytes.Buffer
	if err := printJobs(&out, []job{{ID: "1", Command: command}}, []job{{ID: "local-1", Command: command}}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "...") != 2 {
		t.Fatal(out.String())
	}
	if len(command[1]) != 200 {
		t.Fatal("listing changed original command")
	}
	if strings.Contains(out.String(), strings.Repeat("x", 200)) {
		t.Fatal("full command leaked into listing")
	}
}
