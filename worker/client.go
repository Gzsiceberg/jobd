package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Job struct {
	ID              string   `json:"id"`
	Command         []string `json:"command"`
	CancelRequested int      `json:"cancel_requested"`
}

type WorkerRecord struct {
	CurrentJobID *string `json:"current_job_id"`
	CancelJobID  *string `json:"cancel_job_id"`
}

type ControllerClient struct {
	baseURL       string
	workerID      string
	http          *http.Client
	retryInterval time.Duration
}

func NewControllerClient(address, workerID, queue string, retryInterval time.Duration) (*ControllerClient, error) {
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`).MatchString(queue) {
		return nil, fmt.Errorf("queue name must match [a-z0-9][a-z0-9_-]{0,62}")
	}
	u, err := url.Parse(address)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("controller must be an HTTP(S) URL without a query or fragment")
	}
	return &ControllerClient{
		baseURL:       strings.TrimRight(address, "/") + "/queues/" + queue,
		workerID:      workerID,
		retryInterval: retryInterval,
		http: &http.Client{
			Timeout: 10 * time.Second,
			// Do not turn a redirected POST into a GET or send it to another host.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func (c *ControllerClient) Register(ctx context.Context, hostname string) (WorkerRecord, error) {
	var worker WorkerRecord
	err := c.post(ctx, "/workers/register", struct {
		WorkerID string `json:"worker_id"`
		Hostname string `json:"hostname"`
	}{c.workerID, hostname}, &worker)
	return worker, err
}

func (c *ControllerClient) Heartbeat(ctx context.Context) (WorkerRecord, error) {
	var record WorkerRecord
	err := c.post(ctx, c.workerPath("heartbeat"), struct{}{}, &record)
	return record, err
}

func (c *ControllerClient) Claim(ctx context.Context) (*Job, error) {
	var response struct {
		Job *Job `json:"job"`
	}
	err := c.post(ctx, c.workerPath("claim"), struct{}{}, &response)
	return response.Job, err
}

func (c *ControllerClient) Progress(ctx context.Context, jobID string, progress float64) error {
	return c.post(ctx, "/jobs/"+url.PathEscape(jobID)+"/progress", struct {
		WorkerID string  `json:"worker_id"`
		Progress float64 `json:"progress"`
	}{c.workerID, progress}, nil)
}

func (c *ControllerClient) Output(ctx context.Context, jobID, path string) error {
	return c.post(ctx, "/jobs/"+url.PathEscape(jobID)+"/output", struct {
		WorkerID   string `json:"worker_id"`
		OutputPath string `json:"output_path"`
	}{c.workerID, path}, nil)
}

func (c *ControllerClient) Finish(ctx context.Context, jobID string, result Result) error {
	endpoint := "fail"
	if result.ExitCode != nil && *result.ExitCode == 0 && result.Error == "" {
		endpoint = "complete"
	}
	return c.post(ctx, "/jobs/"+url.PathEscape(jobID)+"/"+endpoint, struct {
		WorkerID string   `json:"worker_id"`
		ExitCode *int     `json:"exit_code"`
		Error    string   `json:"error,omitempty"`
		Progress *float64 `json:"progress,omitempty"`
	}{c.workerID, result.ExitCode, result.Error, result.Progress}, nil)
}

func (c *ControllerClient) workerPath(action string) string {
	return "/workers/" + url.PathEscape(c.workerID) + "/" + action
}

// post is the only retry layer. Calls keep their original payload until accepted,
// stopped, or rejected permanently. The controller's mutations are retry-safe.
func (c *ControllerClient) post(ctx context.Context, path string, body, output any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		retry, err := c.postOnce(ctx, path, payload, output)
		if err == nil || !retry {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("Controller unavailable; retrying", "path", path, "error", err)
		if err := wait(ctx, c.retryInterval); err != nil {
			return err
		}
	}
}

func (c *ControllerClient) postOnce(ctx context.Context, path string, payload []byte, output any) (retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return true, err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		retry = response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		return retry, fmt.Errorf("POST %s: %s", path, response.Status)
	}
	const maxResponse = 1 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil {
		return true, err
	}
	if len(data) > maxResponse {
		return false, fmt.Errorf("POST %s: response exceeds 1 MiB", path)
	}
	if output != nil {
		if err := json.Unmarshal(data, output); err != nil {
			return false, fmt.Errorf("POST %s: invalid JSON: %w", path, err)
		}
	}
	return false, nil
}

func wait(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
