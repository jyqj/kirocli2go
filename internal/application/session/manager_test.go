package session

import (
	"context"
	"testing"
	"time"
)

func TestManagerSlidingTTLAndRotate(t *testing.T) {
	now := time.Date(2026, 3, 22, 12, 0, 0, 0, time.UTC)
	manager := New(Config{
		Enabled:       true,
		TTL:           30 * time.Minute,
		SweepInterval: time.Minute,
		Now: func() time.Time {
			return now
		},
	})

	first := manager.Ensure("sess-1", "/tmp/a")
	if first.ConversationID == "" {
		t.Fatal("expected conversation id")
	}
	if first.Epoch != 1 {
		t.Fatalf("expected epoch 1, got %d", first.Epoch)
	}

	now = now.Add(20 * time.Minute)
	if _, ok := manager.Update("sess-1", Update{Touch: true}); !ok {
		t.Fatal("expected update to succeed")
	}

	now = now.Add(20 * time.Minute)
	manager.DeleteExpired()
	if _, ok := manager.Get("sess-1"); !ok {
		t.Fatal("expected sliding ttl to keep session alive")
	}

	rotated := manager.Rotate("sess-1", "/tmp/b")
	if rotated.Epoch != 2 {
		t.Fatalf("expected epoch 2, got %d", rotated.Epoch)
	}
	if rotated.ConversationID == first.ConversationID {
		t.Fatal("expected rotated conversation id")
	}
	if rotated.WorkingDirectory != "/tmp/b" {
		t.Fatalf("unexpected working directory: %s", rotated.WorkingDirectory)
	}

	now = now.Add(31 * time.Minute)
	manager.DeleteExpired()
	if _, ok := manager.Get("sess-1"); ok {
		t.Fatal("expected expired session to be deleted")
	}
	snapshot := manager.Snapshot()
	if snapshot.CreatedTotal == 0 || snapshot.RotatedTotal == 0 || snapshot.ExpiredTotal == 0 {
		t.Fatalf("expected snapshot counters to be populated, got %+v", snapshot)
	}
}

func TestManagerStartSweeper(t *testing.T) {
	now := time.Date(2026, 3, 22, 12, 0, 0, 0, time.UTC)
	manager := New(Config{
		Enabled:       true,
		TTL:           time.Minute,
		SweepInterval: 10 * time.Millisecond,
		Now: func() time.Time {
			return now
		},
	})
	manager.Ensure("sess-1", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)

	now = now.Add(2 * time.Minute)
	time.Sleep(30 * time.Millisecond)
	if _, ok := manager.Get("sess-1"); ok {
		t.Fatal("expected background sweeper to remove expired session")
	}
}
