package main

import (
	"context"
	"log/slog"
	"time"
)

// Progress is stored as a fraction. Ignore binary floating-point noise at exactly
// five percentage points; only a strictly greater increase triggers early upload.
func progressAdvanced(current, previous float64) bool {
	return current > previous+0.05+1e-12
}

func (p *progressReporter) uploadLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	previous := 0.0
	for {
		periodic := false
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			periodic = true
		case <-p.changed:
		}
		if ctx.Err() != nil {
			return
		}
		current := p.snapshot()
		if !periodic && !progressAdvanced(current, previous) {
			continue
		}
		// One in-flight upload at a time. Retries keep their snapshot; newer values
		// stay buffered and are considered immediately after this request completes.
		if err := p.client.Progress(ctx, p.jobID, current); err != nil {
			if ctx.Err() == nil {
				slog.Warn("Progress upload failed", "job", p.jobID, "error", err)
			}
			continue
		}
		previous = current
	}
}
