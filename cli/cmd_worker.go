package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newWorkerCommand(options *cliOptions) *cobra.Command {
	group := &cobra.Command{Use: "worker", Short: "Manage workers and remote job assignments"}
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
	for _, action := range []string{"pause", "resume"} {
		group.AddCommand(&cobra.Command{
			Use:   action + " WORKER_ID_OR_PREFIX [WORKER_ID_OR_PREFIX ...]",
			Short: action + " remote job assignments for workers",
			Args:  cobra.MinimumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if options.local {
					return fmt.Errorf("worker %s only controls remote assignments", action)
				}
				c, err := newClient(options.address, options.queue)
				if err != nil {
					return err
				}
				return setWorkersPaused(c, args, action == "pause", cmd.OutOrStdout())
			},
		})
	}
	return group
}
