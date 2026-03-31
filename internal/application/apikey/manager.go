package apikey

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"kirocli-go/internal/config"
)

type Record struct {
	ID             string `json:"id"`
	Name           string `json:"name,omitempty"`
	Token          string `json:"token"`
	Enabled        bool   `json:"enabled,omitempty"`
	CacheNamespace string `json:"cache_namespace,omitempty"`
}

type rawRecord struct {
	ID             string `json:"id"`
	Name           string `json:"name,omitempty"`
	Token          string `json:"token"`
	Enabled        *bool  `json:"enabled,omitempty"`
	CacheNamespace string `json:"cache_namespace,omitempty"`
}

type Principal struct {
	ID             string
	Name           string
	CacheNamespace string
}

type Snapshot struct {
	ID             string `json:"id"`
	Name           string `json:"name,omitempty"`
	Enabled        bool   `json:"enabled"`
	CacheNamespace string `json:"cache_namespace,omitempty"`
}

type Manager struct {
	required bool
	records  map[string]Record
	list     []Snapshot
}

type contextKey string

const principalContextKey contextKey = "api_key_principal"

func New(security config.SecurityConfig) (*Manager, error) {
	records := make(map[string]Record)

	if token := strings.TrimSpace(security.APIToken); token != "" {
		records[token] = Record{
			ID:             "default",
			Name:           "default",
			Token:          token,
			Enabled:        true,
			CacheNamespace: "default",
		}
	}

	if raw := strings.TrimSpace(security.APIKeysJSON); raw != "" {
		var parsed []rawRecord
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			return nil, fmt.Errorf("api key config parse failed: %w", err)
		}
		for i, item := range parsed {
			token := strings.TrimSpace(item.Token)
			if token == "" {
				return nil, fmt.Errorf("api key config item %d missing token", i)
			}
			id := strings.TrimSpace(item.ID)
			if id == "" {
				id = fmt.Sprintf("key-%d", i+1)
			}
			name := strings.TrimSpace(item.Name)
			if name == "" {
				name = id
			}
			enabled := true
			if item.Enabled != nil {
				enabled = *item.Enabled
			}
			namespace := strings.TrimSpace(item.CacheNamespace)
			if namespace == "" {
				namespace = id
			}
			records[token] = Record{
				ID:             id,
				Name:           name,
				Token:          token,
				Enabled:        enabled,
				CacheNamespace: namespace,
			}
		}
	}

	snapshots := make([]Snapshot, 0, len(records))
	for _, item := range records {
		snapshots = append(snapshots, Snapshot{
			ID:             item.ID,
			Name:           item.Name,
			Enabled:        item.Enabled,
			CacheNamespace: item.CacheNamespace,
		})
	}

	return &Manager{
		required: len(records) > 0,
		records:  records,
		list:     snapshots,
	}, nil
}

func (m *Manager) Required() bool {
	return m != nil && m.required
}

func (m *Manager) Authenticate(token string) (Principal, bool) {
	if m == nil {
		return Principal{}, false
	}
	record, ok := m.records[strings.TrimSpace(token)]
	if !ok || !record.Enabled {
		return Principal{}, false
	}
	return Principal{
		ID:             record.ID,
		Name:           record.Name,
		CacheNamespace: record.CacheNamespace,
	}, true
}

func (m *Manager) Snapshots() []Snapshot {
	if m == nil {
		return nil
	}
	out := make([]Snapshot, len(m.list))
	copy(out, m.list)
	return out
}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	if ctx == nil {
		return Principal{}, false
	}
	principal, ok := ctx.Value(principalContextKey).(Principal)
	return principal, ok
}
