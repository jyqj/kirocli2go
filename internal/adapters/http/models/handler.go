package models

import (
	"encoding/json"
	"net/http"

	"kirocli-go/internal/domain/model"
	"kirocli-go/internal/ports"
)

type Handler struct {
	catalog ports.ModelCatalog
}

func NewHandler(catalog ports.ModelCatalog) *Handler {
	return &Handler{catalog: catalog}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	models, err := h.catalog.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	type item struct {
		ID     string `json:"id"`
		Object string `json:"object"`
	}

	data := make([]item, 0, len(models))
	for _, model := range filterDisplayModels(models) {
		data = append(data, item{
			ID:     model.ExternalName,
			Object: "model",
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   data,
	})
}

func filterDisplayModels(models []model.ResolvedModel) []model.ResolvedModel {
	result := make([]model.ResolvedModel, 0, len(models))
	seen := make(map[string]bool, len(models))
	for _, entry := range models {
		if seen[entry.ExternalName] {
			continue
		}
		seen[entry.ExternalName] = true
		result = append(result, entry)
	}
	return result
}
