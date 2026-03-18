package background

import (
	"context"
	"log"
	"time"
)

type ModelRefresher interface {
	Refresh(ctx context.Context) (int, error)
}

type ModelRefreshRunner struct {
	enabled      bool
	runOnStart   bool
	startupDelay time.Duration
	interval     time.Duration
	refresher    ModelRefresher
}

func NewModelRefreshRunner(enabled, runOnStart bool, startupDelay, interval time.Duration, refresher ModelRefresher) *ModelRefreshRunner {
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	if startupDelay < 0 {
		startupDelay = 0
	}
	return &ModelRefreshRunner{
		enabled:      enabled,
		runOnStart:   runOnStart,
		startupDelay: startupDelay,
		interval:     interval,
		refresher:    refresher,
	}
}

func (r *ModelRefreshRunner) Start(ctx context.Context) {
	if r == nil || !r.enabled || r.refresher == nil {
		return
	}

	go func() {
		if r.runOnStart {
			select {
			case <-time.After(r.startupDelay):
				r.refresh(ctx, "startup")
			case <-ctx.Done():
				return
			}
		}

		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				r.refresh(ctx, "periodic")
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (r *ModelRefreshRunner) refresh(ctx context.Context, source string) {
	refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	count, err := r.refresher.Refresh(refreshCtx)
	if err != nil {
		log.Printf("model refresh (%s) failed: %v", source, err)
		return
	}
	log.Printf("model refresh (%s) completed: %d models", source, count)
}
