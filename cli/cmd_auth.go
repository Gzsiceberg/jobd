package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newAuthCommand(options *cliOptions) *cobra.Command {
	group := &cobra.Command{Use: "auth", Short: "Generate expiring worker credentials"}
	var duration string
	command := &cobra.Command{
		Use: "create-worker-key --duration 24h", Short: "Print a worker key for JOBD_QUEUE (HTTPS, admin only)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if options.local {
				return fmt.Errorf("worker key generation is not supported in local mode")
			}
			key := strings.TrimSpace(os.Getenv("JOBD_MASTER_KEY"))
			if key == "" {
				return fmt.Errorf("JOBD_MASTER_KEY is required to generate worker keys")
			}
			ttl, err := time.ParseDuration(duration)
			if err != nil || ttl < time.Second || ttl > 30*24*time.Hour || ttl%time.Second != 0 {
				return fmt.Errorf("duration must be whole seconds between 1s and 720h (30 days)")
			}
			c, err := newClient(options.address, options.queue)
			if err != nil {
				return err
			}
			if !strings.HasPrefix(c.base, "https://") {
				return fmt.Errorf("worker key generation requires HTTPS")
			}
			c.apiKey = key
			var result struct {
				APIKey    string `json:"api_key"`
				ExpiresAt string `json:"expires_at"`
			}
			if err := c.request("POST", "/auth/worker-key", map[string]int64{"duration_seconds": int64(ttl / time.Second)}, &result); err != nil {
				return err
			}
			if result.APIKey == "" || result.ExpiresAt == "" {
				return fmt.Errorf("controller returned an invalid worker key response")
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Worker key for queue %q expires at %s\n", options.queue, result.ExpiresAt)
			_, err = fmt.Fprintln(cmd.OutOrStdout(), result.APIKey)
			return err
		},
	}
	command.Flags().StringVar(&duration, "duration", "", "Lifetime, e.g. 24h or 168h (maximum 720h)")
	_ = command.MarkFlagRequired("duration")
	group.AddCommand(command)
	return group
}
