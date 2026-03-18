package stats

import (
	"encoding/json"
	"net/http"

	appstats "kirocli-go/internal/application/stats"
)

type Handler struct {
	collector *appstats.Collector
}

func NewHandler(collector *appstats.Collector) *Handler {
	return &Handler{collector: collector}
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
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "ok",
		"stats":  h.collector.Snapshot(),
	})
}
