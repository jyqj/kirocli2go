package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"kirocli-go/internal/application/apikey"
	"kirocli-go/internal/application/chat"
	"kirocli-go/internal/application/session"
	appstats "kirocli-go/internal/application/stats"
	"kirocli-go/internal/config"
	"kirocli-go/internal/domain/account"
)

type dashboardSessionStub struct{}

func (dashboardSessionStub) Snapshot() session.Snapshot {
	return session.Snapshot{ActiveSessions: 3}
}

type dashboardKeyStub struct{}

func (dashboardKeyStub) Snapshots() []apikey.Snapshot { return nil }
func (dashboardKeyStub) Required() bool               { return true }

type dashboardCacheStub struct{}

func (dashboardCacheStub) Snapshot() chat.FakeCacheSnapshot {
	return chat.FakeCacheSnapshot{Hits: 7, Lookups: 10, HitRate: 0.7}
}

func TestHandleDashboardAggregatesOverview(t *testing.T) {
	stats := appstats.NewCollector()
	stats.RecordRequest()
	stats.RecordSuccess(account.SuccessMeta{
		InputTokens:  10,
		OutputTokens: 20,
		Tokens:       30,
		Credits:      1.25,
	})
	stats.RecordCompact("manual")

	handler := NewHandler(
		config.Config{},
		stats,
		nil,
		nil,
		nil,
		dashboardSessionStub{},
		dashboardKeyStub{},
		dashboardCacheStub{},
	)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/dashboard", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	dashboard := payload["dashboard"].(map[string]any)
	summary := dashboard["summary"].(map[string]any)
	if summary["sessions_active"].(float64) != 3 {
		t.Fatalf("expected sessions_active 3, got %v", summary["sessions_active"])
	}
	if summary["fake_cache_hits"].(float64) != 7 {
		t.Fatalf("expected fake_cache_hits 7, got %v", summary["fake_cache_hits"])
	}
	if summary["compact_total"].(float64) != 1 {
		t.Fatalf("expected compact_total 1, got %v", summary["compact_total"])
	}
}
