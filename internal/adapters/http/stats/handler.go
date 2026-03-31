package stats

import (
	"encoding/json"
	"net/http"

	"kirocli-go/internal/application/chat"
	"kirocli-go/internal/application/session"
	appstats "kirocli-go/internal/application/stats"
)

type Handler struct {
	collector *appstats.Collector
	sessions  interface {
		Snapshot() session.Snapshot
	}
	cache interface {
		Snapshot() chat.FakeCacheSnapshot
	}
}

func NewHandler(collector *appstats.Collector, sessions interface {
	Snapshot() session.Snapshot
}, cache interface {
	Snapshot() chat.FakeCacheSnapshot
}) *Handler {
	return &Handler{collector: collector, sessions: sessions, cache: cache}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.collector == nil {
		http.Error(w, "stats collector unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	response := map[string]any{
		"status": "ok",
		"stats":  h.collector.Snapshot(),
	}
	if h.sessions != nil {
		response["sessions"] = h.sessions.Snapshot()
	}
	if h.cache != nil {
		response["fake_cache"] = h.cache.Snapshot()
	}
	_ = json.NewEncoder(w).Encode(response)
}
