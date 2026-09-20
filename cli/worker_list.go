package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

func tokenTimeRemaining(expiry *string, now time.Time) string {
	if expiry == nil {
		return "unknown"
	}
	at, err := time.Parse(time.RFC3339, *expiry)
	if err != nil {
		return "unknown"
	}
	remaining := at.Sub(now)
	if remaining <= 0 {
		return "expired " + (-remaining).Truncate(time.Second).String() + " ago"
	}
	if remaining < time.Second {
		return "<1s remaining"
	}
	return remaining.Truncate(time.Second).String() + " remaining"
}

func listWorkers(c *client, out io.Writer) error {
	if strings.TrimSpace(os.Getenv("JOBD_MASTER_KEY")) == "" {
		return fmt.Errorf("JOBD_MASTER_KEY is required to list workers")
	}
	var result struct {
		Workers []struct {
			WorkerID    string  `json:"worker_id"`
			Hostname    string  `json:"hostname"`
			TokenExpiry *string `json:"token_expired_time"`
		} `json:"workers"`
	}
	if err := c.request("GET", "/workers", nil, &result); err != nil {
		return err
	}
	jobs := make([]job, len(result.Workers))
	for i := range result.Workers {
		jobs[i].WorkerID = &result.Workers[i].WorkerID
	}
	labels := workerLabels(jobs)
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "WORKER_ID\tWORKER_NAME\tTOKEN_EXPIRED_TIME")
	now := time.Now()
	for _, worker := range result.Workers {
		fmt.Fprintf(w, "%s\t%q\t%s\n", labels[worker.WorkerID], worker.Hostname, tokenTimeRemaining(worker.TokenExpiry, now))
	}
	return w.Flush()
}
