package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"syscall"
	"text/tabwriter"
	"time"
)

func fetchJobs(c *client) ([]job, error) {
	var jobs []job
	for offset := 0; ; offset += 100 {
		var page struct {
			Jobs []job `json:"jobs"`
		}
		if err := c.request("GET", fmt.Sprintf("/jobs?limit=100&offset=%d", offset), nil, &page); err != nil {
			// Do not present an incomplete queue as a successful listing.
			return nil, err
		}
		jobs = append(jobs, page.Jobs...)
		if len(page.Jobs) < 100 {
			return jobs, nil
		}
	}
}

func listCombined(address, queue, stateDir string, out, diagnostic io.Writer) error {
	remote, err := newClient(address, queue)
	if err != nil {
		return err
	}
	remoteJobs, remoteErr := fetchJobs(remote)
	local, localErr := newLocalClient(stateDir)
	var localJobs []job
	if localErr == nil {
		localJobs, localErr = fetchJobs(local)
	}
	// A missing/stale local socket is normal on remote-only clients. Never start
	// a worker just to list both queues, and never infer state from queue files.
	localAbsent := errors.Is(localErr, os.ErrNotExist) || errors.Is(localErr, syscall.ECONNREFUSED)
	if remoteErr != nil && localErr != nil {
		return fmt.Errorf("cannot list jobs: %w", errors.Join(
			fmt.Errorf("remote queue: %w", remoteErr), fmt.Errorf("local queue: %w", localErr)))
	}
	if remoteErr != nil {
		fmt.Fprintf(diagnostic, "Warning: remote queue unavailable: %v\n", remoteErr)
	}
	if localErr != nil && !localAbsent {
		fmt.Fprintf(diagnostic, "Warning: local queue unavailable: %v\n", localErr)
	}
	return printJobs(out, remoteJobs, localJobs)
}

const commandDisplayLimit = 120

func listedCommand(argv []string) string {
	command := displayCommand(argv)
	runes := []rune(command)
	if len(runes) > commandDisplayLimit {
		return string(runes[:commandDisplayLimit-3]) + "..."
	}
	return command
}

func printJobs(out io.Writer, listings ...[]job) error {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	now := time.Now()
	fmt.Fprintln(w, "ID\tSTATE\tELAPSED\tPROGRESS\tHOST\tWORKER\tEXIT\tOUTPUT\tCOMMAND")
	for _, listing := range listings {
		for _, j := range listing {
			exit := "-"
			if j.ExitCode != nil {
				exit = strconv.Itoa(*j.ExitCode)
			}
			progress := "-"
			if j.Progress != nil {
				progress = fmt.Sprintf("%.1f%%", *j.Progress*100)
			}
			state := j.Status
			if state == "running" && j.CancelRequested != 0 {
				state = "cancelling"
			}
			// Quote host/path so tabs and newlines cannot corrupt the table.
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", j.ID, state, elapsed(j, now), progress,
				strconv.Quote(value(j.Hostname)), value(j.WorkerID), exit, strconv.Quote(value(j.OutputPath)), listedCommand(j.Command))
		}
	}
	return w.Flush()
}
