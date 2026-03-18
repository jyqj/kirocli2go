package background

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type stubRefresher struct {
	count atomic.Int64
}

func (s *stubRefresher) Refresh(ctx context.Context) (int, error) {
	_ = ctx
	s.count.Add(1)
	return 1, nil
}

func TestModelRefreshRunnerRunsOnStartAndInterval(t *testing.T) {
	refresher := &stubRefresher{}
	runner := NewModelRefreshRunner(true, true, 10*time.Millisecond, 20*time.Millisecond, refresher)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner.Start(ctx)
	time.Sleep(55 * time.Millisecond)
	cancel()

	if refresher.count.Load() < 2 {
		t.Fatalf("expected at least 2 refresh runs, got %d", refresher.count.Load())
	}
}
