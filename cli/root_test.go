package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestUnknownManagementCommandSuggestsForcedSubmission(t *testing.T) {
	t.Setenv("JOBD_WORKER_TOKEN", "")
	t.Setenv("PATH", t.TempDir())
	for _, prefix := range []string{"env", "worker", "job", "completion"} {
		t.Run(prefix, func(t *testing.T) {
			var out bytes.Buffer
			err := run([]string{prefix, "exple", "sensitive-argument"}, strings.NewReader(""), &out, io.Discard)
			if err == nil {
				t.Fatal("unknown command accepted")
			}
			if !strings.Contains(err.Error(), `unknown command "exple"`) || !strings.Contains(err.Error(), "jobd -- "+prefix+" [ARGS...]") {
				t.Fatalf("missing error or submission hint: %v", err)
			}
			if strings.Contains(err.Error(), "sensitive-argument") {
				t.Fatal("hint echoed job arguments")
			}
			if out.Len() != 0 {
				t.Fatal("error polluted stdout")
			}
		})
	}
}
