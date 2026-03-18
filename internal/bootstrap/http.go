package bootstrap

import (
	"encoding/json"
	"net/http"

	"kirocli-go/internal/adapters/http/admin"
	"kirocli-go/internal/adapters/http/anthropic"
	"kirocli-go/internal/adapters/http/middleware"
	"kirocli-go/internal/adapters/http/models"
	"kirocli-go/internal/adapters/http/openai"
	"kirocli-go/internal/adapters/mcp/websearch"
	"kirocli-go/internal/application/chat"
	"kirocli-go/internal/config"
	"kirocli-go/internal/ports"
)

func NewMux(cfg config.Config, chatService *chat.Service, catalog ports.ModelCatalog, webSearch *websearch.Client, statsHandler http.Handler, adminHandler *admin.Handler) http.Handler {
	mux := http.NewServeMux()
	adminPageHandler := NewAdminPageHandler()

	openAIHandler := openai.NewHandler(chatService)
	anthropicHandler := anthropic.NewHandler(chatService, webSearch)
	countTokensHandler := anthropic.NewCountTokensHandler()
	modelsHandler := models.NewHandler(catalog)

	protected := middleware.RequireAPIToken(cfg.Security.APIToken)

	mux.Handle("/health", http.HandlerFunc(handleHealth))
	mux.Handle("/admin", adminPageHandler)
	mux.Handle("/admin/", adminPageHandler)
	mux.Handle("/", http.HandlerFunc(handleHealth))
	mux.Handle("/v1/chat/completions", protected(openAIHandler))
	mux.Handle("/v1/messages", protected(anthropicHandler))
	mux.Handle("/v1/messages/count_tokens", protected(countTokensHandler))
	mux.Handle("/v1/models", protected(modelsHandler))
	if statsHandler != nil {
		mux.Handle("/v1/stats", protected(statsHandler))
	}
	if adminHandler != nil {
		mux.Handle("/admin/api/", protected(adminHandler))
	}

	return mux
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "ok",
		"mode":   "skeleton",
	})
}
