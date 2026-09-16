package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type Config struct {
	Controller        string
	Queue             string
	StateDir          string
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
}

func parseConfig(args []string, output io.Writer) (Config, error) {
	var config Config
	flags := flag.NewFlagSet("jobd-worker", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&config.Controller, "controller", envDefault("JOBD_CONTROLLER", "https://jobd-controller.aflashsheng.workers.dev"), "controller URL")
	flags.StringVar(&config.Queue, "queue", envDefault("JOBD_QUEUE", "default"), "queue name")
	flags.StringVar(&config.StateDir, "state-dir", envDefault("JOBD_STATE_DIR", "~/.local/state/jobd-worker"), "identity and lock directory")
	poll := flags.Float64("poll-interval", 5, "idle polling and HTTP retry interval in seconds")
	heartbeat := flags.Float64("heartbeat-interval", 15, "heartbeat interval in seconds")
	if err := flags.Parse(args); err != nil {
		return config, err
	}
	if flags.NArg() != 0 {
		return config, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	var err error
	if config.PollInterval, err = positiveSeconds(*poll); err != nil {
		return config, fmt.Errorf("poll-interval: %w", err)
	}
	if config.HeartbeatInterval, err = positiveSeconds(*heartbeat); err != nil {
		return config, fmt.Errorf("heartbeat-interval: %w", err)
	}
	if config.StateDir == "" {
		return config, fmt.Errorf("state-dir must not be empty")
	}
	if config.StateDir == "~" || strings.HasPrefix(config.StateDir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return config, err
		}
		config.StateDir = filepath.Join(home, strings.TrimPrefix(config.StateDir, "~"))
	}
	return config, nil
}

func positiveSeconds(seconds float64) (time.Duration, error) {
	nanoseconds := seconds * float64(time.Second)
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || nanoseconds < 1 || nanoseconds >= float64(math.MaxInt64) {
		return 0, fmt.Errorf("must be finite, at least 1ns, and within Go's duration range")
	}
	return time.Duration(nanoseconds), nil
}

func envDefault(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}

func run(config Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return runWithContext(ctx, config)
}

func runWithContext(ctx context.Context, config Config) error {
	if strings.TrimSpace(os.Getenv("JOBD_API_KEY")) == "" {
		slog.Info("Waiting for JOBD_API_KEY; restart worker with the environment variable set")
		<-ctx.Done()
		return nil
	}
	client, err := NewControllerClient(config.Controller, "", config.Queue, config.PollInterval)
	if err != nil {
		return err
	}
	defer client.http.CloseIdleConnections()
	identity, err := OpenWorkerIdentity(config.StateDir)
	if err != nil {
		return err
	}
	defer identity.Close()
	client.workerID = identity.ID
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}

	local, err := openLocalQueue(config.StateDir)
	if err != nil {
		return err
	}
	defer local.Close()
	worker := Worker{
		client: client, hostname: hostname, localQueue: local,
		pollInterval: config.PollInterval, heartbeatInterval: config.HeartbeatInterval,
		shutdownTimeout: 10 * time.Second, processGrace: 5 * time.Second,
	}
	local.workerID, local.hostname, local.cancel = identity.ID, hostname, worker.active.RequestCancellation
	localSocket, err := openLocalSocket(ctx, local)
	if err != nil {
		return err
	}
	defer localSocket.Close()
	slog.Info("Worker started", "worker", identity.ID, "queue", config.Queue)
	err = worker.Run(ctx)
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return nil
	}
	return err
}

func main() {
	config, err := parseConfig(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err == nil {
		err = run(config)
	}
	if err != nil {
		slog.Error("Worker stopped", "error", err)
		os.Exit(1)
	}
}
