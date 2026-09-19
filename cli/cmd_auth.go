package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newAuthCommand(options *cliOptions) *cobra.Command {
	group := &cobra.Command{Use: "auth", Short: "Generate and verify expiring worker tokens"}
	var duration string
	command := &cobra.Command{
		Use: "create-worker-token --duration 24h", Short: "Print a worker token for JOBD_QUEUE (HTTPS, admin only)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if options.local {
				return fmt.Errorf("worker token generation is not supported in local mode")
			}
			key := strings.TrimSpace(os.Getenv("JOBD_MASTER_KEY"))
			if key == "" {
				return fmt.Errorf("JOBD_MASTER_KEY is required to generate worker tokens")
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
				return fmt.Errorf("worker token generation requires HTTPS")
			}
			c.apiKey = key
			var result struct {
				Token     string `json:"token"`
				ExpiresAt string `json:"expires_at"`
			}
			if err := c.request("POST", "/auth/worker-token", map[string]int64{"duration_seconds": int64(ttl / time.Second)}, &result); err != nil {
				return err
			}
			if result.Token == "" || result.ExpiresAt == "" {
				return fmt.Errorf("controller returned an invalid worker token response")
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Worker token for queue %q expires at %s\n", options.queue, result.ExpiresAt)
			_, err = fmt.Fprintln(cmd.OutOrStdout(), result.Token)
			return err
		},
	}
	command.Flags().StringVar(&duration, "duration", "", "Lifetime, e.g. 24h or 168h (maximum 720h)")
	_ = command.MarkFlagRequired("duration")
	group.AddCommand(command, newVerifyWorkerTokenCommand(options))
	return group
}

func newVerifyWorkerTokenCommand(options *cliOptions) *cobra.Command {
	return &cobra.Command{
		Use: "verify-worker-token", Short: "Verify JOBD_WORKER_TOKEN and print its remaining lifetime (HTTPS)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if options.local {
				return fmt.Errorf("worker token verification is not supported in local mode")
			}
			token := strings.TrimSpace(os.Getenv("JOBD_WORKER_TOKEN"))
			if token == "" {
				return fmt.Errorf("JOBD_WORKER_TOKEN is required")
			}
			c, err := newClient(options.address, options.queue)
			if err != nil {
				return err
			}
			if !strings.HasPrefix(c.base, "https://") {
				return fmt.Errorf("worker token verification requires HTTPS")
			}
			c.apiKey = token
			var result struct {
				Valid     bool   `json:"valid"`
				Queue     string `json:"queue"`
				ExpiresAt string `json:"expires_at"`
			}
			if err := c.request("GET", "/auth/worker-token/verify", nil, &result); err != nil {
				return err
			}
			expires, err := time.Parse(time.RFC3339, result.ExpiresAt)
			if err != nil || !result.Valid || result.Queue != options.queue {
				return fmt.Errorf("controller returned an invalid worker token verification response")
			}
			remaining := time.Until(expires)
			if remaining < 0 {
				remaining = 0
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Worker token valid for queue %q\nExpires at: %s\nRemaining: %s\n", result.Queue, expires.UTC().Format(time.RFC3339), remaining.Truncate(time.Second))
			return err
		},
	}
}
