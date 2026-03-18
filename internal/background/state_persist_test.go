package background

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

type stubSnapshot struct {
	Value string `json:"value"`
}

type stubSource struct {
	state stubSnapshot
}

func (s *stubSource) Snapshot() stubSnapshot {
	return s.state
}

func (s *stubSource) ApplySnapshot(snapshot stubSnapshot) {
	s.state = snapshot
}

func TestStatePersistRunnerLoadAndStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	source := &stubSource{state: stubSnapshot{Value: "one"}}
	runner := NewStatePersistRunner(true, 15*time.Millisecond, path, "stub", source)

	ctx, cancel := context.WithCancel(context.Background())
	runner.Start(ctx)
	time.Sleep(30 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)

	restored := &stubSource{}
	restoreRunner := NewStatePersistRunner(true, 15*time.Millisecond, path, "stub", restored)
	if err := restoreRunner.Load(); err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if restored.state.Value != "one" {
		t.Fatalf("expected restored state 'one', got %q", restored.state.Value)
	}
}
