package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"kirocli-go/internal/application/apikey"
	"kirocli-go/internal/config"
)

func TestRequireAPIKeyInjectsPrincipal(t *testing.T) {
	manager, err := apikey.New(config.SecurityConfig{
		APIKeysJSON: `[{"id":"team-a","token":"token-a","cache_namespace":"ns-a"}]`,
	})
	if err != nil {
		t.Fatalf("New error: %v", err)
	}

	protected := RequireAPIKey(manager)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := apikey.PrincipalFromContext(r.Context())
		if !ok {
			t.Fatal("expected principal in context")
		}
		if principal.ID != "team-a" || principal.CacheNamespace != "ns-a" {
			t.Fatalf("unexpected principal: %+v", principal)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.Header.Set("Authorization", "Bearer token-a")
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

func TestRequireAPIKeyRejectsUnknownKey(t *testing.T) {
	manager, err := apikey.New(config.SecurityConfig{APIToken: "secret"})
	if err != nil {
		t.Fatalf("New error: %v", err)
	}
	protected := RequireAPIKey(manager)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.Header.Set("X-Api-Key", "wrong")
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if rec.Header().Get("X-Kiro-Auth-Required") != "true" {
		t.Fatalf("expected auth header, got %q", rec.Header().Get("X-Kiro-Auth-Required"))
	}
	if got := rec.Body.String(); got == "" || got[0] != '{' {
		t.Fatalf("expected json unauthorized body, got %q", got)
	}
}
