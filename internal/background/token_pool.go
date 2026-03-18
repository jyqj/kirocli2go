package background

import (
	"context"
	"log"
	"time"

	"kirocli-go/internal/adapters/token/provider"
)

type TokenPoolMaintainer interface {
	WarmPool(ctx context.Context) (provider.PoolSnapshot, error)
	RefreshPool(ctx context.Context) (provider.PoolSnapshot, error)
}

type TokenPoolRunner struct {
	enabled      bool
	runOnStart   bool
	startupDelay time.Duration
	interval     time.Duration
	maintainer   TokenPoolMaintainer
}

func NewTokenPoolRunner(enabled, runOnStart bool, startupDelay, interval time.Duration, maintainer TokenPoolMaintainer) *TokenPoolRunner {
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	if startupDelay < 0 {
		startupDelay = 0
	}
	return &TokenPoolRunner{
		enabled:      enabled,
		runOnStart:   runOnStart,
		startupDelay: startupDelay,
		interval:     interval,
		maintainer:   maintainer,
	}
}

func (r *TokenPoolRunner) Start(ctx context.Context) {
	if r == nil || !r.enabled || r.maintainer == nil {
		return
	}

	go func() {
		if r.runOnStart {
			select {
			case <-time.After(r.startupDelay):
				r.warm(ctx, "startup")
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

func (r *TokenPoolRunner) warm(ctx context.Context, source string) {
	warmCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	snapshot, err := r.maintainer.WarmPool(warmCtx)
	if err != nil {
		log.Printf("token pool warm (%s) failed: %v", source, err)
		return
	}
	log.Printf("token pool warm (%s) completed: warmed=%d/%d eligible=%d", source, snapshot.WarmedAccounts, snapshot.TargetSize, snapshot.EligibleCounts)
}

func (r *TokenPoolRunner) refresh(ctx context.Context, source string) {
	refreshCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	snapshot, err := r.maintainer.RefreshPool(refreshCtx)
	if err != nil {
		log.Printf("token pool refresh (%s) failed: %v", source, err)
		return
	}
	log.Printf("token pool refresh (%s) completed: warmed=%d/%d eligible=%d", source, snapshot.WarmedAccounts, snapshot.TargetSize, snapshot.EligibleCounts)
}
