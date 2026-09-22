package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

const help = `Submit commands directly, or use subcommands to manage jobs, secrets, and workers.

Examples:
  jobd echo hello
  jobd -- env
  jobd env set API_KEY=XXX KEY=SSS
  jobd worker restart
  jobd auth create-worker-token --duration 24h
  jobd auth verify-worker-token

Shortcuts:
  -l               List remote and local jobs (default; --local lists local only)
  COMMAND [ARGS...] Submit a command and print its job ID
  -C               Clear finished job records (keep log files)
  -o [ID]          Print output path on executing host (last run by default)
  -r [ID...]       Remove non-running jobs (last added by default)
  -k [ID]          Request cancellation of a running job (last run by default)
  -u [ID...]       Move queued jobs first in argument order (last added by default)
  -U ID1 ID2       Swap two queued jobs
  -h               Show help
  --version        Show version
Use -- to force submission of a reserved name (env, worker, job, auth, help, completion)
or a command beginning with a dash. Commands run directly, not via a shell.
Defaults: JOBD_CONTROLLER=https://jobd-controller.aflashsheng.workers.dev, JOBD_QUEUE=default.
Authentication: JOBD_MASTER_KEY (admin) or JOBD_WORKER_TOKEN (expiring worker token).
Without either, local mode is automatic. Workers only use JOBD_WORKER_TOKEN.
--local uses the same actions against the local worker's single queue.
Local jobs run when the controller has no job available, or directly when no key is set.
JOBD_STATE_DIR selects the local worker (default ~/.local/state/jobd-worker).
Output files remain on the executing worker, not on the CLI machine.
`

type cliOptions struct {
	address  string
	queue    string
	stateDir string
	local    bool
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func newManagementCommand(options *cliOptions, input io.Reader, out, diagnostic io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use: "jobd [COMMAND [ARGS...]]", Short: "Submit jobs and manage jobd", Long: help,
		Version:       version,
		SilenceErrors: true, SilenceUsage: true,
	}
	root.SetIn(input)
	root.SetOut(out)
	root.SetErr(diagnostic)
	root.PersistentFlags().BoolVar(&options.local, "local", options.local, "Use the local job queue")
	root.AddCommand(newEnvCommand(options), newWorkerCommand(options), newJobCommand(options), newAuthCommand(options))
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	strictCommandGroups(root)
	return root
}

// Cobra otherwise prints help successfully for an unknown word under a
// non-runnable group. Typos must fail, never masquerade as a successful action.
func strictCommandGroups(command *cobra.Command) {
	if !command.Runnable() {
		command.Args = func(cmd *cobra.Command, args []string) error {
			if err := cobra.NoArgs(cmd, args); err != nil {
				prefix := cmd
				for prefix.Parent() != nil && prefix.Parent().Parent() != nil {
					prefix = prefix.Parent()
				}
				if prefix.Parent() != nil {
					// Show only the reserved prefix, not arguments that may contain secrets.
					return fmt.Errorf("%w\nHint: to submit a job starting with %q, use: jobd -- %s [ARGS...]", err, prefix.Name(), prefix.Name())
				}
				return err
			}
			return nil
		}
		command.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
	}
	for _, child := range command.Commands() {
		strictCommandGroups(child)
	}
}
