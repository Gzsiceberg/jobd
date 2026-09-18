package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newWorkerCommand(options *cliOptions) *cobra.Command {
	group := &cobra.Command{Use: "worker", Short: "Manage the local worker daemon"}
	for _, spec := range []struct{ name, description string }{
		{"start", "Start the detached local worker if needed"},
		{"restart", "Start or restart the detached local worker"},
		{"stop", "Stop the local worker"},
	} {
		group.AddCommand(&cobra.Command{
			Use: spec.name, Short: spec.description, Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				if options.local {
					return fmt.Errorf("--local selects a job queue, not worker management")
				}
				return manageWorker("--"+spec.name, options.address, options.queue, cmd.OutOrStdout(), cmd.ErrOrStderr())
			},
		})
	}
	return group
}
