package main

import (
	"bytes"
	"io"
	"testing"
)

func TestVersion(t *testing.T) {
	previous := version
	t.Cleanup(func() { version = previous })
	// Version reporting must not require a valid controller or local worker.
	t.Setenv("JOBD_CONTROLLER", "invalid")
	t.Setenv("JOBD_STATE_DIR", t.TempDir())
	for _, buildVersion := range []string{"dev", "v1.2.3", "v1.2.3-rc.1"} {
		version = buildVersion
		for _, args := range [][]string{{"--version"}, {"--local", "--version"}} {
			var out bytes.Buffer
			if err := run(args, nil, &out, io.Discard); err != nil {
				t.Fatal(err)
			}
			if want := "jobd version " + buildVersion + "\n"; out.String() != want {
				t.Fatalf("got %q, want %q", out.String(), want)
			}
		}
	}
	root := newManagementCommand(&cliOptions{}, nil, io.Discard, io.Discard)
	if isManagementCommand(root, "--") {
		t.Fatal("-- must preserve direct submission")
	}
}
