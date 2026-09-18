package main

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type job struct {
	ID              string   `json:"id"`
	Status          string   `json:"status"`
	Command         []string `json:"command"`
	Hostname        *string  `json:"hostname"`
	WorkerID        *string  `json:"worker_id"`
	OutputPath      *string  `json:"output_path"`
	ExitCode        *int     `json:"exit_code"`
	StartedAt       *string  `json:"started_at"`
	FinishedAt      *string  `json:"finished_at"`
	CancelRequested int      `json:"cancel_requested"`
}

// Shared by shorthand actions and explicit Cobra job subcommands.
func runAction(options cliOptions, action string, args []string, out, diagnostic io.Writer) error {
	address, queue, local, stateDir := options.address, options.queue, options.local, options.stateDir
	switch action {
	case "--", "-l", "-C", "-o", "-r", "-u", "-k", "-U":
	default:
		if strings.HasPrefix(action, "-") {
			return fmt.Errorf("unknown action %s; use -h for help", action)
		}
	}
	if action == "-l" && len(args) != 0 {
		return fmt.Errorf("-l takes no arguments")
	}
	if !local && action == "-l" && strings.TrimSpace(os.Getenv("JOBD_API_KEY")) != "" {
		return listCombined(address, queue, stateDir, out, diagnostic)
	}
	if strings.TrimSpace(os.Getenv("JOBD_API_KEY")) == "" {
		local = true
		if action == "-l" {
			fmt.Fprintln(diagnostic, "Warning: JOBD_API_KEY is not set; using local mode. Set JOBD_API_KEY and run jobd worker restart to enable controller jobs.")
		}
	}
	var c *client
	var err error
	if local {
		if err := manageWorker("--start", address, queue, io.Discard, diagnostic); err != nil {
			return err
		}
		c, err = newLocalClient(stateDir)
	} else {
		c, err = newClient(address, queue)
	}
	if err != nil {
		return err
	}
	return runJobs(c, action, args, out, diagnostic)
}

// Confirmation happens before any network request or local worker startup.
func removeAllJobs(options cliOptions, input io.Reader, out, diagnostic io.Writer) error {
	options.local = options.local || strings.TrimSpace(os.Getenv("JOBD_API_KEY")) == ""
	target := fmt.Sprintf("queue %q on %s", options.queue, options.address)
	var c *client
	var err error
	if options.local {
		target = fmt.Sprintf("local queue at %q", options.stateDir)
		c, err = newLocalClient(options.stateDir)
	} else {
		c, err = newClient(options.address, options.queue)
	}
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(diagnostic, "Remove ALL queued and finished jobs from %s?\nRunning jobs, queue secrets, and output files will be kept. This cannot be undone.\nType \"yes\" to confirm: ", target); err != nil {
		return err
	}
	answer, err := bufio.NewReader(io.LimitReader(input, 256)).ReadString('\n')
	if err != nil || strings.TrimSuffix(strings.TrimSuffix(answer, "\n"), "\r") != "yes" {
		return fmt.Errorf("removal cancelled; confirmation did not match")
	}
	if options.local {
		if err := manageWorker("--start", options.address, options.queue, io.Discard, diagnostic); err != nil {
			return err
		}
	}
	var result struct {
		Removed     int64 `json:"removed"`
		KeptRunning int64 `json:"kept_running"`
	}
	if err := c.request("POST", "/jobs/remove-all", struct{}{}, &result); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Removed %d job(s); kept %d running job(s).\n", result.Removed, result.KeptRunning)
	return err
}

// Both queues share validation, action defaults, pagination and table rendering.
func runJobs(c *client, action string, args []string, out, diagnostic io.Writer) error {
	switch action {
	case "-l", "-C":
		if len(args) != 0 {
			return fmt.Errorf("%s takes no arguments", action)
		}
	case "-o", "-r", "-u", "-k":
		if len(args) > 1 {
			return fmt.Errorf("%s takes at most one job ID", action)
		}
	case "-U":
		if len(args) != 2 {
			return fmt.Errorf("use -U ID1 ID2")
		}
	default:
		command := append([]string{action}, args...)
		if action == "--" {
			command = args
		} else if strings.HasPrefix(action, "-") {
			return fmt.Errorf("unknown action %s; use -h for help", action)
		}
		if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
			return fmt.Errorf("command must not be empty")
		}
		var result job
		if err := c.request("POST", "/jobs", map[string]any{"command": command}, &result); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, result.ID)
		return err
	}
	if action == "-l" {
		jobs, err := fetchJobs(c)
		if err != nil {
			return err
		}
		return printJobs(out, jobs)
	}
	if action == "-C" {
		return c.request("POST", "/jobs/clear", struct{}{}, nil)
	}
	if action == "-U" {
		return c.request("POST", "/jobs/swap", map[string]string{"first": args[0], "second": args[1]}, nil)
	}
	var selected job
	if len(args) == 0 {
		kind := "added"
		if action == "-o" || action == "-k" {
			kind = "run"
		}
		if err := c.request("GET", "/jobs/latest?kind="+kind, nil, &selected); err != nil {
			return err
		}
	} else {
		selected.ID = args[0]
	}
	path := "/jobs/" + url.PathEscape(selected.ID)
	switch action {
	case "-k":
		if err := c.request("POST", path+"/cancel", struct{}{}, nil); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "Cancellation requested for", selected.ID)
		return err
	case "-r":
		return c.request("DELETE", path, nil, nil)
	case "-u":
		return c.request("POST", path+"/urgent", struct{}{}, nil)
	case "-o":
		if len(args) > 0 {
			if err := c.request("GET", path, nil, &selected); err != nil {
				return err
			}
		}
		if selected.OutputPath == nil || *selected.OutputPath == "" {
			return fmt.Errorf("job %s has no reported output file yet", selected.ID)
		}
		fmt.Fprintf(diagnostic, "Output on worker %s (host %s); file is not downloaded.\n", value(selected.WorkerID), strconv.Quote(value(selected.Hostname)))
		_, err := fmt.Fprintln(out, *selected.OutputPath)
		return err
	}
	return nil
}
