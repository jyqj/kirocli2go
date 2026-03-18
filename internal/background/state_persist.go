package background

import (
	"context"
	"log"
	"time"

	"kirocli-go/internal/adapters/store/jsonfile"
)

type SnapshotWriter[T any] interface {
	Snapshot() T
}

type SnapshotLoader[T any] interface {
	ApplySnapshot(snapshot T)
}

type StatePersistRunner[T any] struct {
	enabled  bool
	interval time.Duration
	path     string
	source   SnapshotWriter[T]
	name     string
}

func NewStatePersistRunner[T any](enabled bool, interval time.Duration, path, name string, source SnapshotWriter[T]) *StatePersistRunner[T] {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	return &StatePersistRunner[T]{
		enabled:  enabled,
		interval: interval,
		path:     path,
		source:   source,
		name:     name,
	}
}

func (r *StatePersistRunner[T]) Load() error {
	if r == nil || !r.enabled || r.path == "" || r.source == nil {
		return nil
	}
	loader, ok := any(r.source).(SnapshotLoader[T])
	if !ok {
		return nil
	}
	var snapshot T
	if err := jsonfile.Load(r.path, &snapshot); err != nil {
		return err
	}
	loader.ApplySnapshot(snapshot)
	return nil
}

func (r *StatePersistRunner[T]) Start(ctx context.Context) {
	if r == nil || !r.enabled || r.path == "" || r.source == nil {
		return
	}

	go func() {
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := jsonfile.Save(r.path, r.source.Snapshot()); err != nil {
					log.Printf("state persist (%s) failed: %v", r.name, err)
				}
			case <-ctx.Done():
				_ = jsonfile.Save(r.path, r.source.Snapshot())
				return
			}
		}
	}()
}
