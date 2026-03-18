package background

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"kirocli-go/internal/adapters/token/provider"
)

type stubPoolMaintainer struct {
	warmCount    atomic.Int64
	refreshCount atomic.Int64
}

func (s *stubPoolMaintainer) WarmPool(ctx context.Context) (provider.PoolSnapshot, error) {
	_ = ctx
	s.warmCount.Add(1)
	return provider.PoolSnapshot{Enabled: true, TargetSize: 2, WarmedAccounts: 2, EligibleCounts: 3}, nil
}

func (s *stubPoolMaintainer) RefreshPool(ctx context.Context) (provider.PoolSnapshot, error) {
	_ = ctx
	s.refreshCount.Add(1)
	return provider.PoolSnapshot{Enabled: true, TargetSize: 2, WarmedAccounts: 2, EligibleCounts: 3}, nil
}

func TestTokenPoolRunnerRunsOnStartAndInterval(t *testing.T) {
	maintainer := &stubPoolMaintainer{}
	runner := NewTokenPoolRunner(true, true, 10*time.Millisecond, 20*time.Millisecond, maintainer)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner.Start(ctx)
	time.Sleep(55 * time.Millisecond)
	cancel()

	if maintainer.warmCount.Load() < 1 {
		t.Fatalf("expected at least 1 warm run, got %d", maintainer.warmCount.Load())
	}
	if maintainer.refreshCount.Load() < 1 {
		t.Fatalf("expected at least 1 refresh run, got %d", maintainer.refreshCount.Load())
	}
}
