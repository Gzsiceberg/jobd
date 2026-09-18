package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newEnvCommand(options *cliOptions) *cobra.Command {
	group := &cobra.Command{Use: "env", Short: "Manage per-queue environment secrets (HTTPS only)"}
	for _, spec := range []struct {
		name, use, description string
		args                   cobra.PositionalArgs
	}{
		{"set", "set KEY[=VALUE] [KEY[=VALUE] ...]", "Set one or more environment secrets", cobra.MinimumNArgs(1)},
		{"list", "list", "List variable names, never values", cobra.NoArgs},
		{"delete", "delete NAME", "Delete a secret", cobra.ExactArgs(1)},
	} {
		command := &cobra.Command{
			Use: spec.use, Short: spec.description, Args: spec.args,
			RunE: func(cmd *cobra.Command, args []string) error {
				if options.local {
					return fmt.Errorf("queue environment is not supported in local mode")
				}
				c, err := newClient(options.address, options.queue)
				if err != nil {
					return err
				}
				if spec.name == "set" {
					args, err = readEnvAssignments(args, cmd.InOrStdin(), cmd.ErrOrStderr())
					if err != nil {
						return err
					}
				}
				return runEnv(c, spec.name, args, cmd.OutOrStdout())
			},
		}
		group.AddCommand(command)
	}
	return group
}
