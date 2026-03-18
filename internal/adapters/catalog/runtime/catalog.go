package runtimecatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"kirocli-go/internal/adapters/catalog/static"
	"kirocli-go/internal/adapters/upstream/clihttp"
	"kirocli-go/internal/domain/account"
	domainerrors "kirocli-go/internal/domain/errors"
	"kirocli-go/internal/domain/model"
	"kirocli-go/internal/ports"
)

type Config struct {
	ModelsURL      string
	ProxyURL       string
	UserAgent      string
	AmzUserAgent   string
	Origin         string
	ModelsTarget   string
	Timeout        time.Duration
	ThinkingSuffix string
}

type Snapshot struct {
	LastRefreshUnix int64    `json:"last_refresh_unix,omitempty"`
	LastError       string   `json:"last_error,omitempty"`
	Models          []string `json:"models,omitempty"`
}

type Catalog struct {
	mu       sync.RWMutex
	cfg      Config
	tokens   ports.TokenProvider
	fallback *static.Catalog
	client   *http.Client

	lastRefresh time.Time
	lastError   string
	models      []string
	modelSet    map[string]bool
}

type responseEnvelope struct {
	Models []struct {
		ModelID string `json:"modelId"`
	} `json:"models"`
}

func New(cfg Config, tokens ports.TokenProvider) *Catalog {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return &Catalog{
		cfg:    cfg,
		tokens: tokens,
		fallback: static.New(static.Config{
			ThinkingSuffix: cfg.ThinkingSuffix,
		}),
		client: &http.Client{
			Timeout:   timeout,
			Transport: clihttp.NewTransport(clihttp.Config{ProxyURL: cfg.ProxyURL}),
		},
		modelSet: make(map[string]bool),
	}
}

func (c *Catalog) Resolve(ctx context.Context, externalModel string) (model.ResolvedModel, error) {
	resolved, err := c.fallback.Resolve(ctx, externalModel)
	if err != nil {
		return resolved, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.modelSet[resolved.InternalName] {
		resolved.Verified = true
		resolved.Source = "runtime"
	}
	return resolved, nil
}

func (c *Catalog) List(ctx context.Context) ([]model.ResolvedModel, error) {
	fallbackModels, err := c.fallback.List(ctx)
	if err != nil {
		return nil, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.models) == 0 {
		return fallbackModels, nil
	}

	merged := make(map[string]bool, len(c.models)*2+len(fallbackModels))
	for _, entry := range fallbackModels {
		merged[entry.ExternalName] = true
	}
	for _, id := range c.models {
		if c.fallback.HiddenFromList(id) {
			continue
		}
		merged[id] = true
		if suffix := strings.TrimSpace(c.cfg.ThinkingSuffix); suffix != "" {
			merged[id+suffix] = true
		}
	}

	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make([]model.ResolvedModel, 0, len(keys))
	for _, key := range keys {
		resolved, err := c.Resolve(ctx, key)
		if err != nil {
			continue
		}
		result = append(result, resolved)
	}
	return result, nil
}

func (c *Catalog) Refresh(ctx context.Context) (int, error) {
	if c.tokens == nil {
		return 0, fmt.Errorf("token provider unavailable")
	}

	lease, err := c.tokens.Acquire(ctx, account.AcquireHint{
		Profile: account.ProfileCLI,
		Model:   "catalog-refresh",
	})
	if err != nil {
		return 0, err
	}

	reqBody, _ := json.Marshal(map[string]string{"origin": c.cfg.Origin})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.ModelsURL, bytes.NewReader(reqBody))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("X-Amz-User-Agent", c.cfg.AmzUserAgent)
	req.Header.Set("X-Amz-Target", c.cfg.ModelsTarget)
	req.Header.Set("Authorization", "Bearer "+lease.Token)
	req.Header.Set("X-Amzn-Codewhisperer-Optout", "true")

	resp, err := c.client.Do(req)
	if err != nil {
		_ = c.tokens.ReportFailure(ctx, lease, account.FailureMeta{
			RequestID: "catalog-refresh",
			Model:     "catalog-refresh",
			Reason:    account.FailureReasonNetwork,
			Message:   err.Error(),
		})
		c.setLastError(err.Error())
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.setLastError(err.Error())
		return 0, err
	}

	if resp.StatusCode != http.StatusOK {
		upstreamErr := &domainerrors.UpstreamError{
			Category:   domainerrors.CategoryUnknown,
			StatusCode: resp.StatusCode,
			Message:    strings.TrimSpace(string(body)),
		}
		reason := account.FailureReasonUnknown
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			reason = account.FailureReasonAuth
		case http.StatusTooManyRequests:
			reason = account.FailureReasonQuota
		}
		_ = c.tokens.ReportFailure(ctx, lease, account.FailureMeta{
			RequestID:  "catalog-refresh",
			Model:      "catalog-refresh",
			StatusCode: resp.StatusCode,
			Reason:     reason,
			Message:    upstreamErr.Error(),
		})
		c.setLastError(upstreamErr.Error())
		return 0, upstreamErr
	}

	var envelope responseEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		c.setLastError(err.Error())
		return 0, err
	}

	models := make([]string, 0, len(envelope.Models))
	modelSet := make(map[string]bool, len(envelope.Models))
	for _, item := range envelope.Models {
		id := strings.TrimSpace(item.ModelID)
		if id == "" || modelSet[id] {
			continue
		}
		modelSet[id] = true
		models = append(models, id)
	}
	sort.Strings(models)

	c.mu.Lock()
	c.models = models
	c.modelSet = modelSet
	c.lastRefresh = time.Now()
	c.lastError = ""
	c.mu.Unlock()

	_ = c.tokens.ReportSuccess(ctx, lease, account.SuccessMeta{
		RequestID: "catalog-refresh",
		Model:     "catalog-refresh",
	})
	return len(models), nil
}

func (c *Catalog) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	models := make([]string, len(c.models))
	copy(models, c.models)

	return Snapshot{
		LastRefreshUnix: c.lastRefresh.Unix(),
		LastError:       c.lastError,
		Models:          models,
	}
}

func (c *Catalog) ApplySnapshot(snapshot Snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()

	models := make([]string, len(snapshot.Models))
	copy(models, snapshot.Models)
	modelSet := make(map[string]bool, len(models))
	for _, item := range models {
		modelSet[item] = true
	}
	c.models = models
	c.modelSet = modelSet
	c.lastError = snapshot.LastError
	if snapshot.LastRefreshUnix > 0 {
		c.lastRefresh = time.Unix(snapshot.LastRefreshUnix, 0)
	}
}

func (c *Catalog) setLastError(message string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastError = message
}
