package provider

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"kirocli-go/internal/adapters/store/jsonfile"
	"kirocli-go/internal/adapters/upstream/clihttp"
	"kirocli-go/internal/domain/account"
	domainerrors "kirocli-go/internal/domain/errors"
)

type Config struct {
	Source         string
	BearerToken    string
	CSVPath        string
	APIURL         string
	APIToken       string
	APICategoryID  int
	APIFetchCount  int
	ActivePoolSize int
	MaxRefreshTry  int
	OIDCURL        string
	ProxyURL       string
	RefreshTimeout time.Duration
	StatePath      string
}

type Provider struct {
	mu         sync.Mutex
	cfg        Config
	httpClient *http.Client
	accounts   []*tokenAccount
	index      int
	rng        *rand.Rand
	lastWarmAt time.Time
	poolStats  poolStats
}

type poolStats struct {
	WarmRuns       int64
	RefreshRuns    int64
	RefillFetches  int64
	LastAction     string
	LastError      string
	LastRefillAdd  int
	LastActionUnix int64
}

type stateFile struct {
	ManagedAccounts []managedAccountState      `json:"managed_accounts,omitempty"`
	Overrides       map[string]accountOverride `json:"overrides,omitempty"`
}

type managedAccountState struct {
	ID            string         `json:"id"`
	Weight        int            `json:"weight,omitempty"`
	BearerToken   string         `json:"bearer_token,omitempty"`
	RefreshToken  string         `json:"refresh_token,omitempty"`
	ClientID      string         `json:"client_id,omitempty"`
	ClientSecret  string         `json:"client_secret,omitempty"`
	ExpiresAt     int64          `json:"expires_at,omitempty"`
	Disabled      bool           `json:"disabled,omitempty"`
	Status        account.Status `json:"status,omitempty"`
	CooldownUntil int64          `json:"cooldown_until,omitempty"`
	LastUsedAt    int64          `json:"last_used_at,omitempty"`
	LastRefreshAt int64          `json:"last_refresh_at,omitempty"`
	LastError     string         `json:"last_error,omitempty"`
	Failures      int            `json:"failures,omitempty"`
}

type ManagedExport struct {
	ID            string         `json:"id"`
	Source        string         `json:"source"`
	Weight        int            `json:"weight"`
	BearerToken   string         `json:"bearer_token,omitempty"`
	RefreshToken  string         `json:"refresh_token,omitempty"`
	ClientID      string         `json:"client_id,omitempty"`
	ClientSecret  string         `json:"client_secret,omitempty"`
	ExpiresAt     int64          `json:"expires_at,omitempty"`
	Disabled      bool           `json:"disabled,omitempty"`
	Status        account.Status `json:"status,omitempty"`
	CooldownUntil int64          `json:"cooldown_until,omitempty"`
	LastUsedAt    int64          `json:"last_used_at,omitempty"`
	LastError     string         `json:"last_error,omitempty"`
	Failures      int            `json:"failures,omitempty"`
}

type accountOverride struct {
	Disabled      bool           `json:"disabled,omitempty"`
	Status        account.Status `json:"status,omitempty"`
	Weight        int            `json:"weight,omitempty"`
	CooldownUntil int64          `json:"cooldown_until,omitempty"`
	LastUsedAt    int64          `json:"last_used_at,omitempty"`
	LastRefreshAt int64          `json:"last_refresh_at,omitempty"`
	LastError     string         `json:"last_error,omitempty"`
	Failures      int            `json:"failures,omitempty"`
	BearerToken   string         `json:"bearer_token,omitempty"`
	ExpiresAt     int64          `json:"expires_at,omitempty"`
}

type ImportRequest struct {
	ID           string `json:"id,omitempty"`
	Weight       int    `json:"weight,omitempty"`
	BearerToken  string `json:"bearer_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
}

type AccountSnapshot struct {
	ID            string         `json:"id"`
	Source        string         `json:"source"`
	Status        account.Status `json:"status"`
	Weight        int            `json:"weight"`
	Disabled      bool           `json:"disabled"`
	InPool        bool           `json:"in_pool,omitempty"`
	HasBearer     bool           `json:"has_bearer"`
	HasRefresh    bool           `json:"has_refresh"`
	ExpiresAt     int64          `json:"expires_at,omitempty"`
	CooldownUntil int64          `json:"cooldown_until,omitempty"`
	LastUsedAt    int64          `json:"last_used_at,omitempty"`
	LastRefreshAt int64          `json:"last_refresh_at,omitempty"`
	LastError     string         `json:"last_error,omitempty"`
	Failures      int            `json:"failures,omitempty"`
}

type tokenAccount struct {
	ID            string
	Weight        int
	BearerToken   string
	RefreshToken  string
	ClientID      string
	ClientSecret  string
	ExpiresAt     int64
	Disabled      bool
	Status        account.Status
	CooldownUntil int64
	LastUsedAt    int64
	LastRefreshAt int64
	LastError     string
	Failures      int
	Source        string
	RemoteID      int
	InPool        bool
}

type PoolSnapshot struct {
	Enabled        bool   `json:"enabled"`
	TargetSize     int    `json:"target_size"`
	WarmedAccounts int    `json:"warmed_accounts"`
	EligibleCounts int    `json:"eligible_accounts"`
	LastWarmUnix   int64  `json:"last_warm_unix,omitempty"`
	WarmRuns       int64  `json:"warm_runs,omitempty"`
	RefreshRuns    int64  `json:"refresh_runs,omitempty"`
	RefillFetches  int64  `json:"refill_fetches,omitempty"`
	LastAction     string `json:"last_action,omitempty"`
	LastActionUnix int64  `json:"last_action_unix,omitempty"`
	LastError      string `json:"last_error,omitempty"`
	LastRefillAdd  int    `json:"last_refill_add,omitempty"`
}

const (
	networkCooldown = 30 * time.Second
	quotaCooldown   = 10 * time.Minute
	unknownCooldown = 15 * time.Second
)

type apiAccountResponse struct {
	ID   int    `json:"id"`
	Data string `json:"data"`
}

type apiAccountData struct {
	RefreshToken string `json:"refresh_token"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

type tokenRefreshRequest struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	GrantType    string `json:"grantType"`
	RefreshToken string `json:"refreshToken"`
}

type tokenRefreshResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int    `json:"expiresIn"`
}

func New(cfg Config) (*Provider, error) {
	if cfg.RefreshTimeout <= 0 {
		cfg.RefreshTimeout = 30 * time.Second
	}
	if cfg.MaxRefreshTry <= 0 {
		cfg.MaxRefreshTry = 3
	}
	if cfg.ActivePoolSize < 0 {
		cfg.ActivePoolSize = 0
	}

	p := &Provider{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout:   cfg.RefreshTimeout,
			Transport: clihttp.NewTransport(clihttp.Config{ProxyURL: cfg.ProxyURL}),
		},
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	accounts, err := p.loadAccounts()
	if err != nil {
		return nil, err
	}
	p.accounts = accounts
	if err := p.loadState(); err != nil {
		return nil, err
	}

	return p, nil
}

func (p *Provider) Acquire(ctx context.Context, hint account.AcquireHint) (account.Lease, error) {
	_ = ctx
	_ = hint

	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.accounts) == 0 {
		return account.Lease{}, domainerrors.New(domainerrors.CategoryAuth, "no accounts configured")
	}

	candidates := p.availableCandidatesLocked()

	if len(candidates) == 0 {
		return account.Lease{}, domainerrors.New(domainerrors.CategoryAuth, "no active account available")
	}

	preferred := filterPoolAccounts(candidates, true)
	if len(preferred) == 0 {
		preferred = candidates
	}

	for attempts := 0; attempts < len(preferred); attempts++ {
		item := pickWeightedAccount(preferred, p.rng.Intn(totalWeight(preferred)))
		if err := p.ensureBearer(item, false); err != nil {
			p.applyFailure(item, account.FailureMeta{
				Reason:  mapProviderErrorToFailureReason(err),
				Message: err.Error(),
			})
			preferred = filterAccounts(preferred, item.ID)
			if len(preferred) == 0 && len(candidates) > 0 {
				candidates = filterAccounts(candidates, item.ID)
				preferred = candidates
			}
			if len(preferred) == 0 {
				break
			}
			continue
		}

		item.Status = account.StatusActive
		item.LastUsedAt = time.Now().Unix()
		if p.cfg.ActivePoolSize > 0 && !item.InPool && p.warmedCountLocked() < p.cfg.ActivePoolSize {
			item.InPool = true
			p.lastWarmAt = time.Now()
		}
		p.index++
		return account.Lease{
			AccountID: item.ID,
			Token:     item.BearerToken,
			Profile:   account.ProfileCLI,
			Metadata: map[string]string{
				"source": item.Source,
			},
		}, nil
	}

	return account.Lease{}, domainerrors.New(domainerrors.CategoryAuth, "no active account available")
}

func (p *Provider) ReportSuccess(ctx context.Context, lease account.Lease, meta account.SuccessMeta) error {
	_ = ctx
	_ = meta
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, item := range p.accounts {
		if item.ID != lease.AccountID {
			continue
		}
		item.Status = account.StatusActive
		item.CooldownUntil = 0
		item.Failures = 0
		item.LastError = ""
		item.LastUsedAt = time.Now().Unix()
		if p.cfg.ActivePoolSize > 0 && p.warmedCountLocked() < p.cfg.ActivePoolSize {
			item.InPool = true
			p.lastWarmAt = time.Now()
		}
		break
	}
	return nil
}

func (p *Provider) ReportFailure(ctx context.Context, lease account.Lease, meta account.FailureMeta) error {
	_ = ctx

	p.mu.Lock()
	triggerRefill := false
	for _, item := range p.accounts {
		if item.ID != lease.AccountID {
			continue
		}
		wasInPool := item.InPool
		p.applyFailure(item, meta)
		triggerRefill = wasInPool && p.apiRefillEnabledLocked() && p.warmedCountLocked() < p.cfg.ActivePoolSize
		break
	}
	p.mu.Unlock()

	if triggerRefill {
		go p.triggerImmediatePoolWarm()
	}
	return nil
}

func (p *Provider) WarmPool(ctx context.Context) (PoolSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.maintainPoolLocked(ctx, false)
}

func (p *Provider) RefreshPool(ctx context.Context) (PoolSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.maintainPoolLocked(ctx, true)
}

func (p *Provider) PoolSnapshot() PoolSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	return PoolSnapshot{
		Enabled:        p.cfg.ActivePoolSize > 0,
		TargetSize:     p.cfg.ActivePoolSize,
		WarmedAccounts: p.warmedCountLocked(),
		EligibleCounts: len(p.availableCandidatesLocked()),
		LastWarmUnix:   lastWarmUnix(p.lastWarmAt),
		WarmRuns:       p.poolStats.WarmRuns,
		RefreshRuns:    p.poolStats.RefreshRuns,
		RefillFetches:  p.poolStats.RefillFetches,
		LastAction:     p.poolStats.LastAction,
		LastActionUnix: p.poolStats.LastActionUnix,
		LastError:      p.poolStats.LastError,
		LastRefillAdd:  p.poolStats.LastRefillAdd,
	}
}

func (p *Provider) SnapshotAccounts() []AccountSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	result := make([]AccountSnapshot, 0, len(p.accounts))
	now := time.Now().Unix()
	for _, item := range p.accounts {
		status := item.Status
		if item.Disabled && status == "" {
			status = account.StatusDisabled
		}
		if !item.Disabled && item.CooldownUntil > now {
			status = account.StatusCooling
		}
		if !item.Disabled && item.CooldownUntil <= now && status == account.StatusCooling {
			status = account.StatusActive
		}
		result = append(result, AccountSnapshot{
			ID:            item.ID,
			Source:        item.Source,
			Status:        status,
			Weight:        normalizedWeight(item.Weight),
			Disabled:      item.Disabled,
			InPool:        item.InPool,
			HasBearer:     item.BearerToken != "",
			HasRefresh:    item.RefreshToken != "",
			ExpiresAt:     item.ExpiresAt,
			CooldownUntil: item.CooldownUntil,
			LastUsedAt:    item.LastUsedAt,
			LastRefreshAt: item.LastRefreshAt,
			LastError:     item.LastError,
			Failures:      item.Failures,
		})
	}
	return result
}

func (p *Provider) DisableAccount(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	item, err := p.findAccountLocked(id)
	if err != nil {
		return err
	}
	item.Disabled = true
	item.Status = account.StatusDisabled
	item.CooldownUntil = 0
	item.LastError = "disabled by admin"
	return p.saveStateLocked()
}

func (p *Provider) EnableAccount(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	item, err := p.findAccountLocked(id)
	if err != nil {
		return err
	}
	item.Disabled = false
	item.Status = account.StatusActive
	item.CooldownUntil = 0
	item.LastError = ""
	item.Failures = 0
	return p.saveStateLocked()
}

func (p *Provider) UpdateWeight(id string, weight int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	item, err := p.findAccountLocked(id)
	if err != nil {
		return err
	}
	item.Weight = normalizedWeight(weight)
	return p.saveStateLocked()
}

func (p *Provider) DeleteAccount(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for idx, item := range p.accounts {
		if item.ID != id {
			continue
		}
		if item.Source == "managed" {
			p.accounts = append(p.accounts[:idx], p.accounts[idx+1:]...)
			return p.saveStateLocked()
		}
		item.Disabled = true
		item.Status = account.StatusDisabled
		item.CooldownUntil = 0
		item.LastError = "deleted by admin"
		return p.saveStateLocked()
	}
	return fmt.Errorf("account %s not found", id)
}

func (p *Provider) RefreshAccount(ctx context.Context, id string) (AccountSnapshot, error) {
	_ = ctx
	p.mu.Lock()
	defer p.mu.Unlock()

	item, err := p.findAccountLocked(id)
	if err != nil {
		return AccountSnapshot{}, err
	}
	if err := p.ensureBearer(item, true); err != nil {
		return AccountSnapshot{}, err
	}
	item.Status = account.StatusActive
	item.Disabled = false
	item.CooldownUntil = 0
	item.LastError = ""
	if p.cfg.ActivePoolSize > 0 && p.warmedCountLocked() < p.cfg.ActivePoolSize {
		item.InPool = true
		p.lastWarmAt = time.Now()
	}
	if err := p.saveStateLocked(); err != nil {
		return AccountSnapshot{}, err
	}
	return snapshotFromTokenAccount(item), nil
}

func (p *Provider) ImportAccount(ctx context.Context, req ImportRequest) (AccountSnapshot, error) {
	_ = ctx
	p.mu.Lock()
	defer p.mu.Unlock()

	if strings.TrimSpace(req.BearerToken) == "" && strings.TrimSpace(req.RefreshToken) == "" {
		return AccountSnapshot{}, fmt.Errorf("bearer_token or refresh_token is required")
	}

	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = fmt.Sprintf("managed-%d", time.Now().UnixNano())
	}
	if _, err := p.findAccountLocked(id); err == nil {
		return AccountSnapshot{}, fmt.Errorf("account %s already exists", id)
	}

	item := &tokenAccount{
		ID:           id,
		Weight:       normalizedWeight(req.Weight),
		BearerToken:  strings.TrimSpace(req.BearerToken),
		RefreshToken: strings.TrimSpace(req.RefreshToken),
		ClientID:     strings.TrimSpace(req.ClientID),
		ClientSecret: strings.TrimSpace(req.ClientSecret),
		Status:       account.StatusActive,
		Source:       "managed",
	}
	if item.BearerToken == "" {
		if err := p.ensureBearer(item, true); err != nil {
			return AccountSnapshot{}, err
		}
	}
	if p.cfg.ActivePoolSize > 0 && p.warmedCountLocked() < p.cfg.ActivePoolSize {
		item.InPool = true
		p.lastWarmAt = time.Now()
	}

	p.accounts = append(p.accounts, item)
	if err := p.saveStateLocked(); err != nil {
		return AccountSnapshot{}, err
	}
	return snapshotFromTokenAccount(item), nil
}

func (p *Provider) ExportAccounts() []ManagedExport {
	p.mu.Lock()
	defer p.mu.Unlock()

	result := make([]ManagedExport, 0, len(p.accounts))
	for _, item := range p.accounts {
		result = append(result, ManagedExport{
			ID:            item.ID,
			Source:        item.Source,
			Weight:        normalizedWeight(item.Weight),
			BearerToken:   item.BearerToken,
			RefreshToken:  item.RefreshToken,
			ClientID:      item.ClientID,
			ClientSecret:  item.ClientSecret,
			ExpiresAt:     item.ExpiresAt,
			Disabled:      item.Disabled,
			Status:        item.Status,
			CooldownUntil: item.CooldownUntil,
			LastUsedAt:    item.LastUsedAt,
			LastError:     item.LastError,
			Failures:      item.Failures,
		})
	}
	return result
}

func (p *Provider) loadAccounts() ([]*tokenAccount, error) {
	switch source := strings.ToLower(strings.TrimSpace(p.cfg.Source)); source {
	case "", "auto":
		if strings.TrimSpace(p.cfg.BearerToken) != "" {
			return p.loadFromEnv(), nil
		}
		if strings.TrimSpace(p.cfg.CSVPath) != "" {
			return p.loadFromCSV()
		}
		if strings.TrimSpace(p.cfg.APIURL) != "" && strings.TrimSpace(p.cfg.APIToken) != "" {
			return p.loadFromAPI()
		}
	case "env":
		if strings.TrimSpace(p.cfg.BearerToken) == "" {
			return nil, fmt.Errorf("bearer token is required for env source")
		}
		return p.loadFromEnv(), nil
	case "csv":
		return p.loadFromCSV()
	case "api":
		return p.loadFromAPI()
	default:
		return nil, fmt.Errorf("unsupported account source: %s", p.cfg.Source)
	}

	return nil, fmt.Errorf("no account source configured")
}

func (p *Provider) loadState() error {
	if strings.TrimSpace(p.cfg.StatePath) == "" {
		return nil
	}

	var state stateFile
	if err := jsonfile.Load(p.cfg.StatePath, &state); err != nil {
		return err
	}

	byID := make(map[string]*tokenAccount, len(p.accounts))
	for _, item := range p.accounts {
		byID[item.ID] = item
	}

	for _, managed := range state.ManagedAccounts {
		if existing, ok := byID[managed.ID]; ok {
			applyManagedState(existing, managed)
			existing.Source = "managed"
			continue
		}
		item := &tokenAccount{
			ID:            managed.ID,
			Weight:        normalizedWeight(managed.Weight),
			BearerToken:   managed.BearerToken,
			RefreshToken:  managed.RefreshToken,
			ClientID:      managed.ClientID,
			ClientSecret:  managed.ClientSecret,
			ExpiresAt:     managed.ExpiresAt,
			Disabled:      managed.Disabled,
			Status:        managed.Status,
			CooldownUntil: managed.CooldownUntil,
			LastUsedAt:    managed.LastUsedAt,
			LastRefreshAt: managed.LastRefreshAt,
			LastError:     managed.LastError,
			Failures:      managed.Failures,
			Source:        "managed",
		}
		p.accounts = append(p.accounts, item)
		byID[item.ID] = item
	}

	for id, override := range state.Overrides {
		if item, ok := byID[id]; ok {
			applyOverride(item, override)
		}
	}

	return nil
}

func (p *Provider) saveStateLocked() error {
	if strings.TrimSpace(p.cfg.StatePath) == "" {
		return nil
	}

	state := stateFile{
		ManagedAccounts: make([]managedAccountState, 0),
		Overrides:       make(map[string]accountOverride),
	}

	for _, item := range p.accounts {
		if item.Source == "managed" {
			state.ManagedAccounts = append(state.ManagedAccounts, managedAccountState{
				ID:            item.ID,
				Weight:        normalizedWeight(item.Weight),
				BearerToken:   item.BearerToken,
				RefreshToken:  item.RefreshToken,
				ClientID:      item.ClientID,
				ClientSecret:  item.ClientSecret,
				ExpiresAt:     item.ExpiresAt,
				Disabled:      item.Disabled,
				Status:        item.Status,
				CooldownUntil: item.CooldownUntil,
				LastUsedAt:    item.LastUsedAt,
				LastRefreshAt: item.LastRefreshAt,
				LastError:     item.LastError,
				Failures:      item.Failures,
			})
			continue
		}
		state.Overrides[item.ID] = accountOverride{
			Disabled:      item.Disabled,
			Status:        item.Status,
			Weight:        normalizedWeight(item.Weight),
			CooldownUntil: item.CooldownUntil,
			LastUsedAt:    item.LastUsedAt,
			LastRefreshAt: item.LastRefreshAt,
			LastError:     item.LastError,
			Failures:      item.Failures,
			BearerToken:   item.BearerToken,
			ExpiresAt:     item.ExpiresAt,
		}
	}

	return jsonfile.Save(p.cfg.StatePath, state)
}

func (p *Provider) loadFromEnv() []*tokenAccount {
	return []*tokenAccount{{
		ID:          "env-0",
		Weight:      100,
		BearerToken: strings.TrimSpace(p.cfg.BearerToken),
		Status:      account.StatusActive,
		Source:      "env",
	}}
}

func (p *Provider) loadFromCSV() ([]*tokenAccount, error) {
	file, err := os.Open(p.cfg.CSVPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	var accounts []*tokenAccount
	for i, record := range records {
		if i == 0 || len(record) < 4 {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(record[0]), "true") {
			continue
		}

		accounts = append(accounts, &tokenAccount{
			ID:           fmt.Sprintf("csv-%d", i),
			Weight:       100,
			RefreshToken: strings.TrimSpace(record[1]),
			ClientID:     strings.TrimSpace(record[2]),
			ClientSecret: strings.TrimSpace(record[3]),
			Status:       account.StatusActive,
			Source:       "csv",
		})
	}

	if len(accounts) == 0 {
		return nil, fmt.Errorf("no enabled accounts found in csv")
	}

	return accounts, nil
}

func (p *Provider) loadFromAPI() ([]*tokenAccount, error) {
	return p.loadFromAPIWithCount(p.cfg.APIFetchCount)
}

func (p *Provider) loadFromAPIWithCount(count int) ([]*tokenAccount, error) {
	if count <= 0 {
		count = 1
	}

	requestBody, err := json.Marshal(map[string]int{
		"category_id": p.cfg.APICategoryID,
		"count":       count,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(p.cfg.APIURL, "/")+"/api/accounts/fetch", bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Passkey", p.cfg.APIToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("account api returned %d: %s", resp.StatusCode, string(body))
	}

	var records []apiAccountResponse
	if err := json.NewDecoder(resp.Body).Decode(&records); err != nil {
		return nil, err
	}

	var accounts []*tokenAccount
	for _, record := range records {
		var data apiAccountData
		if err := json.Unmarshal([]byte(record.Data), &data); err != nil {
			continue
		}
		accounts = append(accounts, &tokenAccount{
			ID:           fmt.Sprintf("api-%d", record.ID),
			Weight:       100,
			RefreshToken: strings.TrimSpace(data.RefreshToken),
			ClientID:     strings.TrimSpace(data.ClientID),
			ClientSecret: strings.TrimSpace(strings.ReplaceAll(data.ClientSecret, "\r", "")),
			Status:       account.StatusActive,
			Source:       "api",
			RemoteID:     record.ID,
		})
	}

	if len(accounts) == 0 {
		return nil, fmt.Errorf("no usable accounts returned by api")
	}

	return accounts, nil
}

func (p *Provider) ensureBearer(item *tokenAccount, force bool) error {
	if !force && item.BearerToken != "" && (item.ExpiresAt == 0 || time.Now().Unix() < item.ExpiresAt-300) {
		return nil
	}

	if item.RefreshToken == "" || item.ClientID == "" || item.ClientSecret == "" {
		if item.BearerToken != "" {
			return nil
		}
		return domainerrors.New(domainerrors.CategoryAuth, "missing refresh credentials")
	}

	requestBody, err := json.Marshal(tokenRefreshRequest{
		ClientID:     item.ClientID,
		ClientSecret: item.ClientSecret,
		GrantType:    "refresh_token",
		RefreshToken: item.RefreshToken,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, p.cfg.OIDCURL, bytes.NewReader(requestBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "aws-sdk-rust/1.3.10 os/macos lang/rust/1.88.0")
	req.Header.Set("X-Amz-User-Agent", "aws-sdk-rust/1.3.10 ua/2.1 api/ssooidc/1.89.0 os/macos lang/rust/1.88.0 m/E app/AmazonQ-For-KIRO_CLI")
	req.Header.Set("Amz-Sdk-Request", "attempt=1; max=3")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Encoding", "gzip")

	var lastErr error
	for attempt := 0; attempt < p.cfg.MaxRefreshTry; attempt++ {
		req.Header.Set("Amz-Sdk-Invocation-Id", fmt.Sprintf("oidc-%d-%d", time.Now().UnixNano(), attempt))

		resp, err := p.httpClient.Do(req)
		if err != nil {
			lastErr = &domainerrors.UpstreamError{
				Category:  domainerrors.CategoryNetwork,
				Message:   "oidc refresh request failed",
				Retryable: true,
				Cause:     err,
			}
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = classifyRefreshHTTPError(resp.StatusCode, respBody)
			if upstreamErr, ok := lastErr.(*domainerrors.UpstreamError); ok && !upstreamErr.Retryable {
				return lastErr
			}
			continue
		}

		var refreshed tokenRefreshResponse
		if err := json.Unmarshal(respBody, &refreshed); err != nil {
			lastErr = &domainerrors.UpstreamError{
				Category: domainerrors.CategoryUnknown,
				Message:  "failed to decode oidc refresh response",
				Cause:    err,
			}
			continue
		}

		item.BearerToken = refreshed.AccessToken
		item.ExpiresAt = time.Now().Unix() + int64(refreshed.ExpiresIn)
		item.LastRefreshAt = time.Now().Unix()
		if refreshed.RefreshToken != "" {
			item.RefreshToken = refreshed.RefreshToken
		}
		_ = p.saveStateLocked()
		return nil
	}

	if lastErr != nil {
		return lastErr
	}
	return domainerrors.New(domainerrors.CategoryUnknown, "oidc refresh exhausted")
}

func (p *Provider) applyFailure(item *tokenAccount, meta account.FailureMeta) {
	item.Failures++
	item.LastError = meta.Message

	switch meta.Reason {
	case account.FailureReasonAuth:
		item.Disabled = true
		item.Status = account.StatusDisabled
		item.CooldownUntil = 0
		item.InPool = false
	case account.FailureReasonBan:
		item.Disabled = true
		item.Status = account.StatusBanned
		item.CooldownUntil = 0
		item.InPool = false
	case account.FailureReasonQuota:
		item.Status = account.StatusCooling
		item.CooldownUntil = time.Now().Add(quotaCooldown).Unix()
		item.InPool = false
	case account.FailureReasonNetwork:
		item.Status = account.StatusCooling
		item.CooldownUntil = time.Now().Add(networkCooldown).Unix()
	case account.FailureReasonNotImplemented:
		item.Status = account.StatusActive
	default:
		item.Status = account.StatusCooling
		item.CooldownUntil = time.Now().Add(unknownCooldown).Unix()
	}
	_ = p.saveStateLocked()
	if item.Source == "api" && item.RemoteID > 0 && (meta.Reason == account.FailureReasonBan || meta.Reason == account.FailureReasonQuota) {
		go p.banAccountViaAPI(item.RemoteID)
	}
	if item.Source == "csv" && item.RefreshToken != "" && shouldDisableCSV(meta.Reason) {
		go p.disableCSVAccount(item.RefreshToken)
	}
}

func shouldDisableCSV(reason account.FailureReason) bool {
	switch reason {
	case account.FailureReasonAuth, account.FailureReasonQuota, account.FailureReasonBan:
		return true
	default:
		return false
	}
}

func (p *Provider) disableCSVAccount(refreshToken string) {
	if strings.TrimSpace(p.cfg.CSVPath) == "" {
		return
	}

	file, err := os.Open(p.cfg.CSVPath)
	if err != nil {
		return
	}
	records, err := csv.NewReader(file).ReadAll()
	file.Close()
	if err != nil || len(records) == 0 {
		return
	}

	updated := false
	for i := 1; i < len(records); i++ {
		record := records[i]
		if len(record) < 4 {
			continue
		}
		if strings.TrimSpace(record[1]) != refreshToken {
			continue
		}
		record[0] = "False"
		records[i] = record
		updated = true
		break
	}
	if !updated {
		return
	}

	output, err := os.Create(p.cfg.CSVPath)
	if err != nil {
		return
	}
	writer := csv.NewWriter(output)
	_ = writer.WriteAll(records)
	writer.Flush()
	_ = output.Close()
}

func (p *Provider) banAccountViaAPI(accountID int) {
	if strings.TrimSpace(p.cfg.APIURL) == "" || strings.TrimSpace(p.cfg.APIToken) == "" {
		return
	}

	body, err := json.Marshal(map[string]any{
		"ids":    []int{accountID},
		"banned": true,
	})
	if err != nil {
		return
	}

	req, err := http.NewRequest(http.MethodPut, strings.TrimRight(p.cfg.APIURL, "/")+"/update", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("X-Passkey", p.cfg.APIToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
}

func mapProviderErrorToFailureReason(err error) account.FailureReason {
	upstreamErr, ok := err.(*domainerrors.UpstreamError)
	if !ok {
		return account.FailureReasonUnknown
	}

	switch upstreamErr.Category {
	case domainerrors.CategoryAuth:
		return account.FailureReasonAuth
	case domainerrors.CategoryQuota:
		return account.FailureReasonQuota
	case domainerrors.CategoryBan:
		return account.FailureReasonBan
	case domainerrors.CategoryNetwork:
		return account.FailureReasonNetwork
	case domainerrors.CategoryNotImplemented:
		return account.FailureReasonNotImplemented
	default:
		return account.FailureReasonUnknown
	}
}

func classifyRefreshHTTPError(statusCode int, body []byte) error {
	message := strings.TrimSpace(string(body))
	category := domainerrors.CategoryUnknown
	retryable := false

	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		category = domainerrors.CategoryAuth
	case http.StatusTooManyRequests:
		category = domainerrors.CategoryQuota
		retryable = true
	default:
		if statusCode >= 500 {
			category = domainerrors.CategoryNetwork
			retryable = true
		}
	}

	if detected, signal, ok := domainerrors.DetectSignal(message); ok {
		category = detected
		return &domainerrors.UpstreamError{
			Category:   category,
			Signal:     signal,
			Message:    message,
			StatusCode: statusCode,
			Retryable:  retryable,
		}
	}

	return &domainerrors.UpstreamError{
		Category:   category,
		Message:    message,
		StatusCode: statusCode,
		Retryable:  retryable,
	}
}

func (p *Provider) findAccountLocked(id string) (*tokenAccount, error) {
	for _, item := range p.accounts {
		if item.ID == id {
			return item, nil
		}
	}
	return nil, fmt.Errorf("account %s not found", id)
}

func snapshotFromTokenAccount(item *tokenAccount) AccountSnapshot {
	return AccountSnapshot{
		ID:            item.ID,
		Source:        item.Source,
		Status:        item.Status,
		Weight:        normalizedWeight(item.Weight),
		Disabled:      item.Disabled,
		InPool:        item.InPool,
		HasBearer:     item.BearerToken != "",
		HasRefresh:    item.RefreshToken != "",
		ExpiresAt:     item.ExpiresAt,
		CooldownUntil: item.CooldownUntil,
		LastUsedAt:    item.LastUsedAt,
		LastRefreshAt: item.LastRefreshAt,
		LastError:     item.LastError,
		Failures:      item.Failures,
	}
}

func applyManagedState(item *tokenAccount, state managedAccountState) {
	item.Weight = normalizedWeight(state.Weight)
	item.BearerToken = state.BearerToken
	item.RefreshToken = state.RefreshToken
	item.ClientID = state.ClientID
	item.ClientSecret = state.ClientSecret
	item.ExpiresAt = state.ExpiresAt
	item.Disabled = state.Disabled
	item.Status = state.Status
	item.CooldownUntil = state.CooldownUntil
	item.LastUsedAt = state.LastUsedAt
	item.LastRefreshAt = state.LastRefreshAt
	item.LastError = state.LastError
	item.Failures = state.Failures
}

func applyOverride(item *tokenAccount, override accountOverride) {
	item.Disabled = override.Disabled
	if override.Status != "" {
		item.Status = override.Status
	}
	if override.Weight > 0 {
		item.Weight = normalizedWeight(override.Weight)
	}
	item.CooldownUntil = override.CooldownUntil
	item.LastUsedAt = override.LastUsedAt
	item.LastRefreshAt = override.LastRefreshAt
	item.LastError = override.LastError
	item.Failures = override.Failures
	if override.BearerToken != "" {
		item.BearerToken = override.BearerToken
	}
	if override.ExpiresAt > 0 {
		item.ExpiresAt = override.ExpiresAt
	}
}

func normalizedWeight(weight int) int {
	if weight <= 0 {
		return 100
	}
	return weight
}

func totalWeight(items []*tokenAccount) int {
	total := 0
	for _, item := range items {
		total += normalizedWeight(item.Weight)
	}
	if total <= 0 {
		return len(items)
	}
	return total
}

func pickWeightedAccount(items []*tokenAccount, roll int) *tokenAccount {
	if len(items) == 0 {
		return nil
	}
	total := totalWeight(items)
	if total <= 0 {
		return items[0]
	}
	if roll < 0 {
		roll = 0
	}
	if roll >= total {
		roll = total - 1
	}
	for _, item := range items {
		roll -= normalizedWeight(item.Weight)
		if roll < 0 {
			return item
		}
	}
	return items[len(items)-1]
}

func filterAccounts(items []*tokenAccount, id string) []*tokenAccount {
	result := make([]*tokenAccount, 0, len(items))
	for _, item := range items {
		if item.ID == id {
			continue
		}
		result = append(result, item)
	}
	return result
}

func filterPoolAccounts(items []*tokenAccount, inPool bool) []*tokenAccount {
	result := make([]*tokenAccount, 0, len(items))
	for _, item := range items {
		if item.InPool != inPool {
			continue
		}
		result = append(result, item)
	}
	return result
}

func (p *Provider) warmedCountLocked() int {
	count := 0
	for _, item := range p.accounts {
		if item.InPool && !item.Disabled {
			count++
		}
	}
	return count
}

func (p *Provider) availableCandidatesLocked() []*tokenAccount {
	candidates := make([]*tokenAccount, 0, len(p.accounts))
	now := time.Now().Unix()
	for _, item := range p.accounts {
		if item.Disabled {
			item.InPool = false
			continue
		}
		if item.CooldownUntil > now {
			item.Status = account.StatusCooling
			if item.Status == account.StatusCooling && item.CooldownUntil > now {
				item.InPool = false
			}
			continue
		}
		if item.Status == account.StatusCooling {
			item.Status = account.StatusActive
			item.CooldownUntil = 0
		}
		candidates = append(candidates, item)
	}
	return candidates
}

func (p *Provider) maintainPoolLocked(ctx context.Context, forceRefresh bool) (PoolSnapshot, error) {
	_ = ctx
	if p.cfg.ActivePoolSize <= 0 {
		return PoolSnapshot{Enabled: false}, nil
	}

	if forceRefresh {
		p.poolStats.RefreshRuns++
		p.poolStats.LastAction = "refresh"
	} else {
		p.poolStats.WarmRuns++
		p.poolStats.LastAction = "warm"
	}
	p.poolStats.LastActionUnix = time.Now().Unix()
	p.poolStats.LastError = ""
	p.poolStats.LastRefillAdd = 0

	eligible := p.availableCandidatesLocked()
	pool := filterPoolAccounts(eligible, true)

	for _, item := range pool {
		if err := p.ensureBearer(item, forceRefresh); err != nil {
			p.applyFailure(item, account.FailureMeta{
				Reason:  mapProviderErrorToFailureReason(err),
				Message: err.Error(),
			})
			item.InPool = false
		}
	}

	eligible = p.availableCandidatesLocked()
	pool = filterPoolAccounts(eligible, true)
	need := p.cfg.ActivePoolSize - len(pool)
	if need < 0 {
		need = 0
	}
	if need > 0 && p.apiRefillEnabledLocked() {
		if fetched, err := p.fetchAndAppendAPIAccountsLocked(need); err == nil && fetched > 0 {
			p.poolStats.RefillFetches++
			p.poolStats.LastRefillAdd = fetched
			eligible = p.availableCandidatesLocked()
		} else if err != nil {
			p.poolStats.LastError = err.Error()
		}
	}
	if need > 0 {
		for _, item := range eligible {
			if item.InPool {
				continue
			}
			if err := p.ensureBearer(item, false); err != nil {
				p.applyFailure(item, account.FailureMeta{
					Reason:  mapProviderErrorToFailureReason(err),
					Message: err.Error(),
				})
				if p.poolStats.LastError == "" {
					p.poolStats.LastError = err.Error()
				}
				continue
			}
			item.InPool = true
			p.lastWarmAt = time.Now()
			need--
			if need == 0 {
				break
			}
		}
	}

	return PoolSnapshot{
		Enabled:        true,
		TargetSize:     p.cfg.ActivePoolSize,
		WarmedAccounts: p.warmedCountLocked(),
		EligibleCounts: len(p.availableCandidatesLocked()),
		LastWarmUnix:   lastWarmUnix(p.lastWarmAt),
		WarmRuns:       p.poolStats.WarmRuns,
		RefreshRuns:    p.poolStats.RefreshRuns,
		RefillFetches:  p.poolStats.RefillFetches,
		LastAction:     p.poolStats.LastAction,
		LastActionUnix: p.poolStats.LastActionUnix,
		LastError:      p.poolStats.LastError,
		LastRefillAdd:  p.poolStats.LastRefillAdd,
	}, nil
}

func lastWarmUnix(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.Unix()
}

func (p *Provider) apiRefillEnabledLocked() bool {
	source := strings.ToLower(strings.TrimSpace(p.cfg.Source))
	switch source {
	case "api":
		return strings.TrimSpace(p.cfg.APIURL) != "" && strings.TrimSpace(p.cfg.APIToken) != ""
	case "auto", "":
		for _, item := range p.accounts {
			if item.Source == "api" {
				return strings.TrimSpace(p.cfg.APIURL) != "" && strings.TrimSpace(p.cfg.APIToken) != ""
			}
		}
	}
	return false
}

func (p *Provider) fetchAndAppendAPIAccountsLocked(count int) (int, error) {
	accounts, err := p.loadFromAPIWithCount(count)
	if err != nil {
		return 0, err
	}

	existing := make(map[string]bool, len(p.accounts))
	for _, item := range p.accounts {
		existing[item.ID] = true
	}

	added := 0
	for _, item := range accounts {
		if existing[item.ID] {
			continue
		}
		p.accounts = append(p.accounts, item)
		existing[item.ID] = true
		added++
	}
	if added > 0 {
		_ = p.saveStateLocked()
	}
	return added, nil
}

func (p *Provider) triggerImmediatePoolWarm() {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	_, _ = p.WarmPool(ctx)
}
