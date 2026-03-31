package apikey

import (
	"context"
	"testing"

	"kirocli-go/internal/config"
)

func TestManagerParsesDefaultAndJSONKeys(t *testing.T) {
	manager, err := New(config.SecurityConfig{
		APIToken:    "default-token",
		APIKeysJSON: `[{"id":"team-a","name":"Team A","token":"token-a","cache_namespace":"ns-a"},{"id":"team-b","token":"token-b","enabled":false}]`,
	})
	if err != nil {
		t.Fatalf("New error: %v", err)
	}
	if !manager.Required() {
		t.Fatal("expected api keys to be required")
	}
	if _, ok := manager.Authenticate("token-b"); ok {
		t.Fatal("expected disabled key to fail auth")
	}
	principal, ok := manager.Authenticate("token-a")
	if !ok {
		t.Fatal("expected token-a to authenticate")
	}
	if principal.ID != "team-a" || principal.CacheNamespace != "ns-a" {
		t.Fatalf("unexpected principal: %+v", principal)
	}
	if len(manager.Snapshots()) != 3 {
		t.Fatalf("expected 3 visible keys, got %d", len(manager.Snapshots()))
	}
}

func TestPrincipalContextRoundtrip(t *testing.T) {
	ctx := WithPrincipal(context.Background(), Principal{ID: "k1", CacheNamespace: "ns1"})
	principal, ok := PrincipalFromContext(ctx)
	if !ok {
		t.Fatal("expected principal from context")
	}
	if principal.ID != "k1" || principal.CacheNamespace != "ns1" {
		t.Fatalf("unexpected principal: %+v", principal)
	}
}
