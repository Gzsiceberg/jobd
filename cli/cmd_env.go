package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newEnvCommand(options *cliOptions) *cobra.Command {
	group := &cobra.Command{Use: "env", Short: "Manage per-queue environment secrets (HTTPS only)"}
	var stdin bool
	for _, spec := range []struct {
		name, use, description string
		args                   cobra.PositionalArgs
	}{
		{"set", "set NAME --stdin", "Set a secret from stdin; preserve all bytes including newlines", cobra.ExactArgs(1)},
		{"list", "list", "List variable names, never values", cobra.NoArgs},
		{"delete", "delete NAME", "Delete a secret", cobra.ExactArgs(1)},
	} {
		command := &cobra.Command{
			Use: spec.use, Short: spec.description, Args: spec.args,
			RunE: func(cmd *cobra.Command, args []string) error {
				if options.local {
					return fmt.Errorf("queue environment is not supported in local mode")
				}
				if spec.name == "set" && !stdin {
					return fmt.Errorf("use jobd env set NAME --stdin; secret values are read only from stdin")
				}
				c, err := newClient(options.address, options.queue)
				if err != nil {
					return err
				}
				return runEnv(c, spec.name, args, cmd.InOrStdin(), cmd.OutOrStdout())
			},
		}
		if spec.name == "set" {
			command.Flags().BoolVar(&stdin, "stdin", false, "Read the exact value from stdin (required)")
		}
		group.AddCommand(command)
	}
	return group
}
