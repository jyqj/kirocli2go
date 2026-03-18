package bootstrap

import (
	"embed"
	"net/http"
)

//go:embed web/admin.html
var adminAssets embed.FS

func NewAdminPageHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := adminAssets.ReadFile("web/admin.html")
		if err != nil {
			http.Error(w, "admin page unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	})
}
