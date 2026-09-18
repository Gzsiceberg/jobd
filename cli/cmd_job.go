package main

import "github.com/spf13/cobra"

func newJobCommand(options *cliOptions) *cobra.Command {
	group := &cobra.Command{Use: "job", Short: "Manage jobs (direct submission and short flags also work)"}
	for _, spec := range []struct {
		use, action, description string
		args                     cobra.PositionalArgs
	}{
		{"submit -- COMMAND [ARGS...]", "--", "Submit a command without interpreting its arguments", cobra.MinimumNArgs(1)},
		{"list", "-l", "List jobs", cobra.NoArgs},
		{"clear", "-C", "Clear finished records; keep log files", cobra.NoArgs},
		{"output [ID]", "-o", "Print output path (last run by default)", cobra.MaximumNArgs(1)},
		{"remove [ID]", "-r", "Remove a non-running job (last added by default)", cobra.MaximumNArgs(1)},
		{"cancel [ID]", "-k", "Request cancellation (last run by default)", cobra.MaximumNArgs(1)},
		{"urgent [ID]", "-u", "Move a queued job first (last added by default)", cobra.MaximumNArgs(1)},
		{"swap ID1 ID2", "-U", "Swap two queued jobs", cobra.ExactArgs(2)},
	} {
		command := &cobra.Command{
			Use: spec.use, Short: spec.description, Args: spec.args,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runAction(*options, spec.action, args, cmd.OutOrStdout(), cmd.ErrOrStderr())
			},
		}
		if spec.action == "--" {
			// Once the executable starts, its flags belong to it, not jobd.
			command.Flags().SetInterspersed(false)
		}
		group.AddCommand(command)
	}
	return group
}
