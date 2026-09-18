package main

import (
	"io"

	"github.com/spf13/cobra"
)

// Only known command roots enter Cobra. Everything else retains direct argv
// submission; in particular, -- bypasses management dispatch entirely.
func isManagementCommand(root *cobra.Command, name string) bool {
	if name == "-h" || name == "--help" || name == "__complete" || name == "__completeNoDesc" {
		return true
	}
	for _, command := range root.Commands() {
		if command.Name() == name {
			return true
		}
	}
	return false
}

func run(args []string, input io.Reader, out, diagnostic io.Writer) error {
	options := cliOptions{
		address:  env("JOBD_CONTROLLER", "https://jobd-controller.aflashsheng.workers.dev"),
		queue:    env("JOBD_QUEUE", "default"),
		stateDir: env("JOBD_STATE_DIR", "~/.local/state/jobd-worker"),
	}
	for len(args) > 0 && args[0] == "--local" {
		options.local = true
		args = args[1:]
	}
	root := newManagementCommand(&options, input, out, diagnostic)
	if len(args) > 0 && isManagementCommand(root, args[0]) {
		root.SetArgs(args)
		return root.Execute()
	}
	action := "-l"
	if len(args) > 0 {
		action = args[0]
		args = args[1:]
	}
	return runAction(options, action, args, out, diagnostic)
}
