package main

import (
	"time"
)

type idleWindow struct {
	since time.Time
	delay time.Duration
}

func (i *idleWindow) reset() {
	i.since = time.Time{}
}

func (i *idleWindow) observe(now time.Time) bool {
	if i.since.IsZero() {
		i.since = now
	}
	delay := i.delay
	if delay <= 0 {
		delay = localIdleDelay
	}
	return now.Sub(i.since) >= delay
}
