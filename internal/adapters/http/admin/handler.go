package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	runtimecatalog "kirocli-go/internal/adapters/catalog/runtime"
	"kirocli-go/internal/adapters/token/provider"
	"kirocli-go/internal/application/apikey"
	"kirocli-go/internal/application/chat"
	"kirocli-go/internal/application/session"
	appstats "kirocli-go/internal/application/stats"
	"kirocli-go/internal/config"
	"kirocli-go/internal/domain/model"
)

type Handler struct {
	stats       *appstats.Collector
	requestLogs *appstats.RequestLogRing
	cfg         config.Config
	provider    interface {
		SnapshotAccounts() []provider.AccountSnapshot
		PoolSnapshot() provider.PoolSnapshot
		WarmPool(ctx context.Context) (provider.PoolSnapshot, error)
		RefreshPool(ctx context.Context) (provider.PoolSnapshot, error)
		DisableAccount(id string) error
		EnableAccount(id string) error
		DeleteAccount(id string) error
		UpdateWeight(id string, weight int) error
		RefreshAccount(ctx context.Context, id string) (provider.AccountSnapshot, error)
		ImportAccount(ctx context.Context, req provider.ImportRequest) (provider.AccountSnapshot, error)
		ExportAccounts() []provider.ManagedExport
	}
	catalog interface {
		List(ctx context.Context) ([]model.ResolvedModel, error)
		Refresh(ctx context.Context) (int, error)
		Snapshot() runtimecatalog.Snapshot
	}
	sessions interface {
		Snapshot() session.Snapshot
	}
	apiKeys interface {
		Snapshots() []apikey.Snapshot
		Required() bool
	}
	cache interface {
		Snapshot() chat.FakeCacheSnapshot
	}
}

func NewHandler(
	cfg config.Config,
	statsCollector *appstats.Collector,
	requestLogs *appstats.RequestLogRing,
	accountProvider interface {
		SnapshotAccounts() []provider.AccountSnapshot
		PoolSnapshot() provider.PoolSnapshot
		WarmPool(ctx context.Context) (provider.PoolSnapshot, error)
		RefreshPool(ctx context.Context) (provider.PoolSnapshot, error)
		DisableAccount(id string) error
		EnableAccount(id string) error
		DeleteAccount(id string) error
		UpdateWeight(id string, weight int) error
		RefreshAccount(ctx context.Context, id string) (provider.AccountSnapshot, error)
		ImportAccount(ctx context.Context, req provider.ImportRequest) (provider.AccountSnapshot, error)
		ExportAccounts() []provider.ManagedExport
	},
	modelCatalog interface {
		List(ctx context.Context) ([]model.ResolvedModel, error)
		Refresh(ctx context.Context) (int, error)
		Snapshot() runtimecatalog.Snapshot
	},
	sessionManager interface {
		Snapshot() session.Snapshot
	},
	apiKeyManager interface {
		Snapshots() []apikey.Snapshot
		Required() bool
	},
	fakeCache interface {
		Snapshot() chat.FakeCacheSnapshot
	},
) *Handler {
	return &Handler{
		cfg:         cfg,
		stats:       statsCollector,
		requestLogs: requestLogs,
		provider:    accountProvider,
		catalog:     modelCatalog,
		sessions:    sessionManager,
		apiKeys:     apiKeyManager,
		cache:       fakeCache,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/admin/api")
	w.Header().Set("Content-Type", "application/json")

	switch {
	case path == "/version" && r.Method == http.MethodGet:
		h.handleVersion(w)
	case path == "/config" && r.Method == http.MethodGet:
		h.handleConfig(w)
	case path == "/doctor" && r.Method == http.MethodGet:
		h.handleDoctor(w)
	case path == "/accounts" && r.Method == http.MethodGet:
		h.handleAccounts(w)
	case path == "/keys" && r.Method == http.MethodGet:
		h.handleKeys(w)
	case path == "/accounts/import" && r.Method == http.MethodPost:
		h.handleImport(w, r)
	case strings.HasPrefix(path, "/accounts/") && strings.HasSuffix(path, "/disable") && r.Method == http.MethodPost:
		h.handleDisable(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/accounts/"), "/disable"))
	case strings.HasPrefix(path, "/accounts/") && strings.HasSuffix(path, "/enable") && r.Method == http.MethodPost:
		h.handleEnable(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/accounts/"), "/enable"))
	case strings.HasPrefix(path, "/accounts/") && r.Method == http.MethodDelete:
		h.handleDelete(w, strings.TrimPrefix(path, "/accounts/"))
	case strings.HasPrefix(path, "/accounts/") && strings.HasSuffix(path, "/refresh") && r.Method == http.MethodPost:
		h.handleRefresh(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/accounts/"), "/refresh"))
	case strings.HasPrefix(path, "/accounts/") && strings.HasSuffix(path, "/weight") && r.Method == http.MethodPost:
		h.handleWeight(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/accounts/"), "/weight"))
	case path == "/export" && r.Method == http.MethodGet:
		h.handleExport(w)
	case path == "/models" && r.Method == http.MethodGet:
		h.handleModels(w, r)
	case path == "/models/refresh" && r.Method == http.MethodPost:
		h.handleRefreshModels(w, r)
	case path == "/pool/warm" && r.Method == http.MethodPost:
		h.handleWarmPool(w, r)
	case path == "/pool/refresh" && r.Method == http.MethodPost:
		h.handleRefreshPool(w, r)
	case path == "/request-logs" && r.Method == http.MethodGet:
		h.handleRequestLogs(w, r)
	case path == "/dashboard" && r.Method == http.MethodGet:
		h.handleDashboard(w)
	case path == "/status" && r.Method == http.MethodGet:
		h.handleStatus(w)
	default:
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "not found"})
	}
}

func (h *Handler) handleVersion(w http.ResponseWriter) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"version": config.Version,
	})
}

func (h *Handler) handleConfig(w http.ResponseWriter) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"server": map[string]any{
			"address": h.cfg.Server.Address,
		},
		"accounts": map[string]any{
			"source":           h.cfg.Accounts.Source,
			"csv_path":         h.cfg.Accounts.CSVPath,
			"api_url":          h.cfg.Accounts.APIURL,
			"api_category_id":  h.cfg.Accounts.APICategoryID,
			"active_pool_size": h.cfg.Accounts.ActivePoolSize,
			"max_refresh_try":  h.cfg.Accounts.MaxRefreshTry,
			"state_path":       h.cfg.Accounts.StatePath,
			"oidc_url":         h.cfg.Accounts.OIDCURL,
		},
		"models": map[string]any{
			"thinking_suffix": h.cfg.Models.ThinkingSuffix,
		},
		"upstream": map[string]any{
			"cli_base_url":   h.cfg.Upstream.CLIBaseURL,
			"cli_models_url": h.cfg.Upstream.CLIModelsURL,
			"cli_origin":     h.cfg.Upstream.CLIOrigin,
		},
		"sessions": map[string]any{
			"sticky_enabled":                 h.cfg.Session.StickyEnabled,
			"ttl_seconds":                    int64(h.cfg.Session.TTL.Seconds()),
			"sweep_interval_seconds":         int64(h.cfg.Session.SweepInterval.Seconds()),
			"max_entries":                    h.cfg.Session.MaxEntries,
			"auto_compact_context_threshold": h.cfg.Session.AutoCompactContextThreshold,
		},
		"security": map[string]any{
			"api_key_required": h.apiKeys != nil && h.apiKeys.Required(),
			"api_keys_count":   len(h.keySnapshots()),
		},
		"fake_cache": h.cacheSnapshot(),
	})
}

func (h *Handler) handleDoctor(w http.ResponseWriter) {
	checks := []map[string]any{
		pathCheck("account_state", h.cfg.Accounts.StatePath),
		pathCheck("stats_state", h.cfg.State.StatsPath),
		pathCheck("catalog_state", h.cfg.State.CatalogPath),
	}

	accountConfigured := false
	switch strings.ToLower(strings.TrimSpace(h.cfg.Accounts.Source)) {
	case "env":
		accountConfigured = strings.TrimSpace(h.cfg.Accounts.BearerToken) != ""
	case "csv":
		accountConfigured = strings.TrimSpace(h.cfg.Accounts.CSVPath) != ""
	case "api":
		accountConfigured = strings.TrimSpace(h.cfg.Accounts.APIURL) != "" && strings.TrimSpace(h.cfg.Accounts.APIToken) != ""
	case "auto", "":
		accountConfigured = strings.TrimSpace(h.cfg.Accounts.BearerToken) != "" ||
			strings.TrimSpace(h.cfg.Accounts.CSVPath) != "" ||
			(strings.TrimSpace(h.cfg.Accounts.APIURL) != "" && strings.TrimSpace(h.cfg.Accounts.APIToken) != "")
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "ok",
		"checks": checks,
		"runtime": map[string]any{
			"version":                config.Version,
			"account_source":         h.cfg.Accounts.Source,
			"account_configured":     accountConfigured,
			"model_refresh_enabled":  h.cfg.Background.ModelRefreshEnabled,
			"state_persist_enabled":  h.cfg.State.PersistEnabled,
			"session_sticky_enabled": h.cfg.Session.StickyEnabled,
			"api_key_required":       h.apiKeys != nil && h.apiKeys.Required(),
		},
		"fake_cache": h.cacheSnapshot(),
	})
}

func pathCheck(name, path string) map[string]any {
	info := map[string]any{
		"name": name,
		"path": path,
	}
	if strings.TrimSpace(path) == "" {
		info["ok"] = false
		info["message"] = "path not configured"
		return info
	}

	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); err != nil {
		info["ok"] = false
		info["message"] = "parent directory missing"
		return info
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			info["ok"] = true
			info["message"] = "state file not created yet"
			return info
		}
		info["ok"] = false
		info["message"] = err.Error()
		return info
	}

	info["ok"] = true
	info["message"] = "ready"
	return info
}

func (h *Handler) handleAccounts(w http.ResponseWriter) {
	accounts := []provider.AccountSnapshot{}
	if h.provider != nil {
		accounts = h.provider.SnapshotAccounts()
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"accounts": accounts,
	})
}

func (h *Handler) handleKeys(w http.ResponseWriter) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"required": h.apiKeys != nil && h.apiKeys.Required(),
		"keys":     h.keySnapshots(),
	})
}

func (h *Handler) handleRequestLogs(w http.ResponseWriter, r *http.Request) {
	limit := 100
	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	var successPtr *bool
	if raw := strings.TrimSpace(r.URL.Query().Get("success")); raw != "" {
		value := raw == "true" || raw == "1"
		successPtr = &value
	}

	entries := []appstats.RequestLogEntry{}
	if h.requestLogs != nil {
		entries = h.requestLogs.Query(appstats.RequestLogQuery{
			Limit:           limit,
			Offset:          offset,
			Protocol:        strings.TrimSpace(r.URL.Query().Get("protocol")),
			Endpoint:        strings.TrimSpace(r.URL.Query().Get("endpoint")),
			Model:           strings.TrimSpace(r.URL.Query().Get("model")),
			AccountID:       strings.TrimSpace(r.URL.Query().Get("account_id")),
			APIKeyID:        strings.TrimSpace(r.URL.Query().Get("api_key_id")),
			ConversationID:  strings.TrimSpace(r.URL.Query().Get("conversation_id")),
			CompactReason:   strings.TrimSpace(r.URL.Query().Get("compact_reason")),
			PayloadStrategy: strings.TrimSpace(r.URL.Query().Get("payload_strategy")),
			Success:         successPtr,
			FailureReason:   strings.TrimSpace(r.URL.Query().Get("failure_reason")),
			BodySignal:      strings.TrimSpace(r.URL.Query().Get("body_signal")),
		})
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"entries": entries,
	})
}

func (h *Handler) handleStatus(w http.ResponseWriter) {
	_ = json.NewEncoder(w).Encode(h.buildDashboardPayload(0))
}

func (h *Handler) handleDashboard(w http.ResponseWriter) {
	_ = json.NewEncoder(w).Encode(h.buildDashboardPayload(10))
}

func (h *Handler) buildDashboardPayload(recentLimit int) map[string]any {
	accountCount := 0
	activeCount := 0
	byStatus := map[string]int{
		"active":   0,
		"cooling":  0,
		"disabled": 0,
		"banned":   0,
	}
	if h.provider != nil {
		accounts := h.provider.SnapshotAccounts()
		accountCount = len(accounts)
		for _, account := range accounts {
			if !account.Disabled && account.Status == "active" {
				activeCount++
			}
			if _, ok := byStatus[string(account.Status)]; ok {
				byStatus[string(account.Status)]++
			}
		}
	}

	var snapshot appstats.Snapshot
	if h.stats != nil {
		snapshot = h.stats.Snapshot()
	}

	pool := provider.PoolSnapshot{}
	if h.provider != nil {
		pool = h.provider.PoolSnapshot()
	}
	sessionSnapshot := session.Snapshot{}
	if h.sessions != nil {
		sessionSnapshot = h.sessions.Snapshot()
	}
	cacheSnapshot := h.cacheSnapshot()
	compactTotal := int64(0)
	for _, value := range snapshot.CompactTriggers {
		compactTotal += value
	}

	recentLogs := []appstats.RequestLogEntry{}
	recentFailures := 0
	if h.requestLogs != nil && recentLimit > 0 {
		recentLogs = h.requestLogs.List(recentLimit)
		for _, entry := range recentLogs {
			if !entry.Success {
				recentFailures++
			}
		}
	}

	catalogSnapshot := runtimecatalog.Snapshot{}
	if h.catalog != nil {
		catalogSnapshot = h.catalog.Snapshot()
	}

	return map[string]any{
		"status":    "ok",
		"accounts":  accountCount,
		"active":    activeCount,
		"by_status": byStatus,
		"status_descriptions": map[string]string{
			"active":   "可被调度的账号",
			"cooling":  "因网络或额度等原因暂时冷却中的账号",
			"disabled": "被管理员禁用或删除的账号",
			"banned":   "因风控或上游封禁被永久停用的账号",
		},
		"stats":      snapshot,
		"pool":       pool,
		"sessions":   sessionSnapshot,
		"fake_cache": cacheSnapshot,
		"security": map[string]any{
			"api_key_required": h.apiKeys != nil && h.apiKeys.Required(),
			"api_keys_count":   len(h.keySnapshots()),
		},
		"dashboard": map[string]any{
			"generated_at_unix": time.Now().Unix(),
			"summary": map[string]any{
				"accounts_total":        accountCount,
				"accounts_active":       activeCount,
				"requests_total":        snapshot.TotalRequests,
				"requests_success":      snapshot.SuccessRequests,
				"requests_failed":       snapshot.FailedRequests,
				"credits_total":         snapshot.TotalCredits,
				"sessions_active":       sessionSnapshot.ActiveSessions,
				"fake_cache_hit_rate":   cacheSnapshot.HitRate,
				"fake_cache_hits":       cacheSnapshot.Hits,
				"fake_cache_lookups":    cacheSnapshot.Lookups,
				"compact_total":         compactTotal,
				"pool_warmed_accounts":  pool.WarmedAccounts,
				"pool_target_size":      pool.TargetSize,
				"catalog_models_cached": len(catalogSnapshot.Models),
			},
			"sections": map[string]any{
				"accounts": map[string]any{
					"total":     accountCount,
					"active":    activeCount,
					"by_status": byStatus,
				},
				"pool":       pool,
				"stats":      snapshot,
				"sessions":   sessionSnapshot,
				"fake_cache": cacheSnapshot,
				"catalog":    catalogSnapshot,
				"security": map[string]any{
					"api_key_required": h.apiKeys != nil && h.apiKeys.Required(),
				},
				"recent": map[string]any{
					"entries":          recentLogs,
					"failure_count":    recentFailures,
					"returned":         len(recentLogs),
					"compact_triggers": snapshot.CompactTriggers,
				},
			},
		},
	}
}

func (h *Handler) keySnapshots() []apikey.Snapshot {
	if h.apiKeys == nil {
		return nil
	}
	return h.apiKeys.Snapshots()
}

func (h *Handler) cacheSnapshot() chat.FakeCacheSnapshot {
	if h.cache == nil {
		return chat.FakeCacheSnapshot{}
	}
	return h.cache.Snapshot()
}

func (h *Handler) handleImport(w http.ResponseWriter, r *http.Request) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	var req provider.ImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	accountSnapshot, err := h.provider.ImportAccount(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"account": accountSnapshot,
	})
}

func (h *Handler) handleDisable(w http.ResponseWriter, r *http.Request, id string) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	if err := h.provider.DisableAccount(strings.TrimSpace(id)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

func (h *Handler) handleEnable(w http.ResponseWriter, r *http.Request, id string) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	if err := h.provider.EnableAccount(strings.TrimSpace(id)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

func (h *Handler) handleDelete(w http.ResponseWriter, id string) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	if err := h.provider.DeleteAccount(strings.TrimSpace(id)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

func (h *Handler) handleRefresh(w http.ResponseWriter, r *http.Request, id string) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	accountSnapshot, err := h.provider.RefreshAccount(r.Context(), strings.TrimSpace(id))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"account": accountSnapshot,
	})
}

func (h *Handler) handleWeight(w http.ResponseWriter, r *http.Request, id string) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	var body struct {
		Weight int `json:"weight"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if err := h.provider.UpdateWeight(strings.TrimSpace(id), body.Weight); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
}

func (h *Handler) handleExport(w http.ResponseWriter) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"accounts": h.provider.ExportAccounts(),
	})
}

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	if h.catalog == nil {
		http.Error(w, "catalog unavailable", http.StatusServiceUnavailable)
		return
	}

	models, err := h.catalog.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"snapshot": h.catalog.Snapshot(),
		"models":   models,
	})
}

func (h *Handler) handleRefreshModels(w http.ResponseWriter, r *http.Request) {
	if h.catalog == nil {
		http.Error(w, "catalog unavailable", http.StatusServiceUnavailable)
		return
	}

	count, err := h.catalog.Refresh(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"success":  true,
		"count":    count,
		"snapshot": h.catalog.Snapshot(),
	})
}

func (h *Handler) handleWarmPool(w http.ResponseWriter, r *http.Request) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	pool, err := h.provider.WarmPool(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"pool":    pool,
	})
}

func (h *Handler) handleRefreshPool(w http.ResponseWriter, r *http.Request) {
	if h.provider == nil {
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		return
	}

	pool, err := h.provider.RefreshPool(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"pool":    pool,
	})
}
