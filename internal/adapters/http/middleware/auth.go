package middleware

import (
	"encoding/json"
	"net/http"
	"strings"

	"kirocli-go/internal/application/apikey"
)

func RequireAPIKey(manager *apikey.Manager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if manager == nil || !manager.Required() {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := manager.Authenticate(extractToken(r))
			if !ok {
				writeUnauthorized(w, r)
				return
			}
			next.ServeHTTP(w, r.WithContext(apikey.WithPrincipal(r.Context(), principal)))
		})
	}
}

func extractToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	if bearer := strings.TrimSpace(r.Header.Get("Authorization")); len(bearer) > len("Bearer ") && strings.EqualFold(bearer[:7], "Bearer ") {
		return strings.TrimSpace(bearer[7:])
	}
	if apiKey := strings.TrimSpace(r.Header.Get("X-Api-Key")); apiKey != "" {
		return apiKey
	}
	return ""
}

func writeUnauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="kirocli-go"`)
	w.Header().Set("X-Kiro-Auth-Required", "true")
	w.WriteHeader(http.StatusUnauthorized)

	path := ""
	if r != nil && r.URL != nil {
		path = r.URL.Path
	}

	switch {
	case strings.HasPrefix(path, "/v1/messages"):
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "authentication_error",
				"message": "invalid or missing api key",
			},
		})
	case strings.HasPrefix(path, "/v1/chat/completions"):
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "invalid or missing api key",
				"type":    "invalid_request_error",
				"code":    "invalid_api_key",
			},
		})
	default:
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   "unauthorized",
			"message": "invalid or missing api key",
		})
	}
}
