package static

import (
	"context"
	"testing"
)

func TestResolveHiddenModelUsesInternalID(t *testing.T) {
	catalog := New(Config{ThinkingSuffix: "-thinking"})

	resolved, err := catalog.Resolve(context.Background(), "claude-3.7-sonnet-thinking")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if resolved.InternalName != "CLAUDE_3_7_SONNET_20250219_V1_0" {
		t.Fatalf("unexpected internal model: %s", resolved.InternalName)
	}
	if !resolved.ThinkingEnabled {
		t.Fatalf("expected thinking model to remain enabled")
	}
	if resolved.Source != "hidden" {
		t.Fatalf("expected hidden source, got %s", resolved.Source)
	}
}

func TestListHidesAutoButKeepsHiddenModels(t *testing.T) {
	catalog := New(Config{ThinkingSuffix: "-thinking"})

	models, err := catalog.List(context.Background())
	if err != nil {
		t.Fatalf("List error: %v", err)
	}

	foundHidden := false
	for _, item := range models {
		if item.ExternalName == "auto" || item.ExternalName == "auto-thinking" {
			t.Fatalf("expected auto models to be hidden from list")
		}
		if item.ExternalName == "claude-3.7-sonnet" {
			foundHidden = true
		}
	}
	if !foundHidden {
		t.Fatalf("expected hidden model to be listed")
	}
}
