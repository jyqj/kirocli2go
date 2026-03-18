package runtimecatalog

import (
	"context"
	"testing"

	"kirocli-go/internal/domain/account"
	"kirocli-go/internal/domain/model"
)

type stubTokens struct{}

func (s *stubTokens) Acquire(ctx context.Context, hint account.AcquireHint) (account.Lease, error) {
	_ = ctx
	_ = hint
	return account.Lease{}, nil
}

func (s *stubTokens) ReportSuccess(ctx context.Context, lease account.Lease, meta account.SuccessMeta) error {
	_ = ctx
	_ = lease
	_ = meta
	return nil
}

func (s *stubTokens) ReportFailure(ctx context.Context, lease account.Lease, meta account.FailureMeta) error {
	_ = ctx
	_ = lease
	_ = meta
	return nil
}

func TestListMergesRuntimeAndFallbackModels(t *testing.T) {
	catalog := New(Config{ThinkingSuffix: "-thinking"}, &stubTokens{})
	catalog.models = []string{"claude-sonnet-4.5", "custom-model", "auto"}
	catalog.modelSet = map[string]bool{
		"claude-sonnet-4.5": true,
		"custom-model":      true,
		"auto":              true,
	}

	models, err := catalog.List(context.Background())
	if err != nil {
		t.Fatalf("List error: %v", err)
	}

	foundCustom := false
	foundCustomThinking := false
	for _, item := range models {
		if item.ExternalName == "custom-model" {
			foundCustom = true
		}
		if item.ExternalName == "custom-model-thinking" {
			foundCustomThinking = true
		}
		if item.ExternalName == "auto" || item.ExternalName == "auto-thinking" {
			t.Fatalf("expected auto to remain hidden from list")
		}
	}
	if !foundCustom || !foundCustomThinking {
		t.Fatalf("expected runtime models to be merged into list, got %+v", models)
	}
}

func TestResolveMarksRuntimeModelVerified(t *testing.T) {
	catalog := New(Config{ThinkingSuffix: "-thinking"}, &stubTokens{})
	catalog.modelSet = map[string]bool{"custom-model": true}

	resolved, err := catalog.Resolve(context.Background(), "custom-model")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if !resolved.Verified {
		t.Fatalf("expected runtime model to be verified")
	}
	if resolved.Source != "runtime" {
		t.Fatalf("expected source runtime, got %s", resolved.Source)
	}
}

var _ interface {
	Resolve(context.Context, string) (model.ResolvedModel, error)
	List(context.Context) ([]model.ResolvedModel, error)
} = (*Catalog)(nil)
