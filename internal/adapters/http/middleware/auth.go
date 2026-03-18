package middleware

import "net/http"

func RequireAPIToken(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if token == "" {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if extractToken(r) != token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func extractToken(r *http.Request) string {
	if bearer := r.Header.Get("Authorization"); len(bearer) > len("Bearer ") && bearer[:7] == "Bearer " {
		return bearer[7:]
	}
	if apiKey := r.Header.Get("X-Api-Key"); apiKey != "" {
		return apiKey
	}
	return ""
}
