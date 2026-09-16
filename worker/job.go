package main

import "time"

// Job is the common record for controller and local queue work.
type Job struct {
	ID              string     `json:"id"`
	Status          string     `json:"status"`
	Command         []string   `json:"command"`
	CreatedAt       time.Time  `json:"created_at"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	WorkerID        string     `json:"worker_id,omitempty"`
	Hostname        string     `json:"hostname,omitempty"`
	OutputPath      string     `json:"output_path,omitempty"`
	ExitCode        *int       `json:"exit_code,omitempty"`
	Error           string     `json:"error,omitempty"`
	Progress        float64    `json:"progress"`
	CancelRequested int        `json:"cancel_requested"`
}
