package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// The worker owns one connection. SQLite serializes socket requests and job
// reports, and transactions replace the old snapshot/copy/rename machinery.
type localQueue struct {
	db                 *sql.DB
	dir                string
	workerID, hostname string
	cancel             func(string)
}

const localColumns = `'local-' || sequence, status, command, created_at,
 started_at, finished_at, worker_id, hostname, output_path, exit_code,
 error, progress, cancel_requested`

func openLocalQueue(stateDir string, persist bool) (*localQueue, error) {
	dir, err := filepath.Abs(filepath.Join(stateDir, "local"))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	dsn := ":memory:"
	if persist {
		path := filepath.Join(dir, "queue.db")
		// The private directory also protects SQLite journals.
		if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
			return nil, fmt.Errorf("local database must be a regular file")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
		if err := os.Chmod(path, 0600); err != nil {
			return nil, err
		}
		uri := url.URL{Scheme: "file", Path: path}
		dsn = uri.String()
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Keep one connection alive: a :memory: database belongs to that connection.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	q := &localQueue{db: db, dir: dir}
	_, err = db.Exec(`PRAGMA busy_timeout=5000;
 PRAGMA synchronous=FULL;
 CREATE TABLE IF NOT EXISTS jobs (
  sequence INTEGER PRIMARY KEY AUTOINCREMENT,
  status TEXT NOT NULL CHECK(status IN ('queued','running','succeeded','failed')),
  command TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  started_at INTEGER,
  finished_at INTEGER,
  worker_id TEXT NOT NULL DEFAULT '',
  hostname TEXT NOT NULL DEFAULT '',
  output_path TEXT NOT NULL DEFAULT '',
  exit_code INTEGER,
  error TEXT NOT NULL DEFAULT '',
  progress REAL NOT NULL DEFAULT 0 CHECK(progress BETWEEN 0 AND 1),
  cancel_requested INTEGER NOT NULL DEFAULT 0,
  queue_position INTEGER NOT NULL
 );
 CREATE INDEX IF NOT EXISTS jobs_order ON jobs(status,queue_position,sequence);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return q, nil
}

func (q *localQueue) Close() error { return q.db.Close() }

func localSequence(id string) (int64, error) {
	if !strings.HasPrefix(id, "local-") {
		return 0, fmt.Errorf("invalid local job ID %q", id)
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(id, "local-"), 10, 64)
	if err != nil || n < 1 || id != fmt.Sprintf("local-%d", n) {
		return 0, fmt.Errorf("invalid local job ID %q", id)
	}
	return n, nil
}

type jobScanner interface{ Scan(...any) error }

func scanLocalJob(row jobScanner) (Job, error) {
	var job Job
	var command string
	var created int64
	var started, finished sql.NullInt64
	err := row.Scan(&job.ID, &job.Status, &command, &created, &started, &finished, &job.WorkerID, &job.Hostname, &job.OutputPath, &job.ExitCode, &job.Error, &job.Progress, &job.CancelRequested)
	if err != nil {
		return job, err
	}
	if err := json.Unmarshal([]byte(command), &job.Command); err != nil {
		return job, err
	}
	job.CreatedAt = time.Unix(0, created).UTC()
	if started.Valid {
		value := time.Unix(0, started.Int64).UTC()
		job.StartedAt = &value
	}
	if finished.Valid {
		value := time.Unix(0, finished.Int64).UTC()
		job.FinishedAt = &value
	}
	return job, nil
}

func (q *localQueue) list(limit, offset int) ([]Job, error) {
	rows, err := q.db.Query(`SELECT `+localColumns+` FROM jobs
 ORDER BY CASE status WHEN 'running' THEN 0 WHEN 'queued' THEN 1 ELSE 2 END,
 queue_position,sequence LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []Job{}
	for rows.Next() {
		job, err := scanLocalJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (q *localQueue) get(id string) (Job, error) {
	sequence, err := localSequence(id)
	if err != nil {
		return Job{}, err
	}
	return scanLocalJob(q.db.QueryRow(`SELECT `+localColumns+` FROM jobs WHERE sequence=?`, sequence))
}

func (q *localQueue) latest(kind string) (Job, error) {
	query := `SELECT ` + localColumns + ` FROM jobs `
	switch kind {
	case "added":
		query += `ORDER BY sequence DESC LIMIT 1`
	case "run":
		query += `WHERE started_at IS NOT NULL ORDER BY started_at DESC,sequence DESC LIMIT 1`
	default:
		return Job{}, fmt.Errorf("invalid latest kind")
	}
	return scanLocalJob(q.db.QueryRow(query))
}

func (q *localQueue) submit(command []string) (*Job, error) {
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return nil, fmt.Errorf("command must not be empty")
	}
	size := 0
	for _, arg := range command {
		if strings.ContainsRune(arg, 0) {
			return nil, fmt.Errorf("command contains NUL")
		}
		size += len(arg)
	}
	if size > 256<<10 || len(command) > 4096 {
		return nil, fmt.Errorf("command is too large")
	}
	data, err := json.Marshal(command)
	if err != nil {
		return nil, err
	}
	job, err := scanLocalJob(q.db.QueryRow(`INSERT INTO jobs(status,command,created_at,queue_position)
 VALUES('queued',?,?,(SELECT COALESCE(MAX(queue_position),0)+1 FROM jobs))
 RETURNING `+localColumns, string(data), time.Now().UnixNano()))
	return &job, err
}

func (q *localQueue) claim() (*Job, error) {
	job, err := scanLocalJob(q.db.QueryRow(`UPDATE jobs SET status='running',started_at=?,worker_id=?,hostname=?
 WHERE sequence=(SELECT sequence FROM jobs WHERE status='queued' ORDER BY queue_position,sequence LIMIT 1)
 RETURNING `+localColumns, time.Now().UnixNano(), q.workerID, q.hostname))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &job, err
}

func (q *localQueue) updateRunning(ctx context.Context, id, assignments string, args ...any) error {
	sequence, err := localSequence(id)
	if err != nil {
		return err
	}
	result, err := q.db.ExecContext(ctx, `UPDATE jobs SET `+assignments+` WHERE sequence=? AND status='running'`, append(args, sequence)...)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("job %s is not running", id)
	}
	return nil
}

// The same execution/progress/reporting interfaces as ControllerClient.
func (q *localQueue) Output(ctx context.Context, id, path string) error {
	return q.updateRunning(ctx, id, `output_path=?`, path)
}
func (q *localQueue) Progress(ctx context.Context, id string, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return fmt.Errorf("invalid progress")
	}
	return q.updateRunning(ctx, id, `progress=MAX(progress,?)`, value)
}
func (q *localQueue) Finish(ctx context.Context, id string, result Result) error {
	status := "failed"
	progress := result.Progress
	if result.ExitCode != nil && *result.ExitCode == 0 && result.Error == "" {
		status = "succeeded"
		one := 1.0
		progress = &one
	}
	return q.updateRunning(ctx, id, `status=?,finished_at=?,exit_code=?,error=?,progress=MAX(progress,COALESCE(?,0))`, status, time.Now().UnixNano(), result.ExitCode, result.Error, progress)
}
func (q *localQueue) recover() error {
	_, err := q.db.Exec(`UPDATE jobs SET status='failed',finished_at=?,error='Worker restarted; previous outcome unknown' WHERE status='running'`, time.Now().UnixNano())
	return err
}
func (q *localQueue) requestCancel(id string) error {
	if err := q.updateRunning(context.Background(), id, `cancel_requested=1`); err != nil {
		return err
	}
	if q.cancel != nil {
		q.cancel(id)
	}
	return nil
}

func (q *localQueue) reorder(id, other string) error {
	first, err := localSequence(id)
	if err != nil {
		return err
	}
	tx, err := q.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	position := func(sequence int64) (int64, error) {
		var status string
		var pos int64
		err := tx.QueryRow(`SELECT status,queue_position FROM jobs WHERE sequence=?`, sequence).Scan(&status, &pos)
		if err == nil && status != "queued" {
			err = fmt.Errorf("only queued jobs can be reordered")
		}
		return pos, err
	}
	a, err := position(first)
	if err != nil {
		return err
	}
	if other == "" {
		_, err = tx.Exec(`UPDATE jobs SET queue_position=(SELECT MIN(queue_position)-1 FROM jobs WHERE status='queued') WHERE sequence=?`, first)
	} else {
		second, parseErr := localSequence(other)
		if parseErr != nil {
			return parseErr
		}
		b, readErr := position(second)
		if readErr != nil {
			return readErr
		}
		_, err = tx.Exec(`UPDATE jobs SET queue_position=CASE sequence WHEN ? THEN ? ELSE ? END WHERE sequence IN (?,?)`, first, b, a, first, second)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (q *localQueue) remove(id string) error {
	sequence, err := localSequence(id)
	if err != nil {
		return err
	}
	result, err := q.db.Exec(`DELETE FROM jobs WHERE sequence=? AND status!='running'`, sequence)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("job %s not found or still running", id)
	}
	return nil
}

type removeAllResult struct {
	Removed     int64 `json:"removed"`
	KeptRunning int64 `json:"kept_running"`
}

func (q *localQueue) removeAll() (removeAllResult, error) {
	var result removeAllResult
	tx, err := q.db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	deleted, err := tx.Exec(`DELETE FROM jobs WHERE status!='running'`)
	if err != nil {
		return result, err
	}
	result.Removed, err = deleted.RowsAffected()
	if err != nil {
		return result, err
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status='running'`).Scan(&result.KeptRunning); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func (q *localQueue) clear() error {
	_, err := q.db.Exec(`DELETE FROM jobs WHERE status IN ('succeeded','failed')`)
	return err
}
