package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"kirocli-go/internal/domain/account"
)

func TestImportDisableEnablePersistsState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "accounts_state.json")

	provider, err := New(Config{
		Source:    "env",
		StatePath: statePath,
	})
	if err == nil {
		t.Fatalf("expected env provider without bearer to fail")
	}

	provider, err = New(Config{
		Source:      "env",
		BearerToken: "env-bearer",
		StatePath:   statePath,
	})
	if err != nil {
		t.Fatalf("New provider error: %v", err)
	}

	imported, err := provider.ImportAccount(context.Background(), ImportRequest{
		ID:          "managed-1",
		BearerToken: "managed-bearer",
	})
	if err != nil {
		t.Fatalf("ImportAccount error: %v", err)
	}
	if imported.ID != "managed-1" {
		t.Fatalf("unexpected imported id: %s", imported.ID)
	}

	if err := provider.DisableAccount("managed-1"); err != nil {
		t.Fatalf("DisableAccount error: %v", err)
	}
	if err := provider.EnableAccount("managed-1"); err != nil {
		t.Fatalf("EnableAccount error: %v", err)
	}

	reloaded, err := New(Config{
		Source:      "env",
		BearerToken: "env-bearer",
		StatePath:   statePath,
	})
	if err != nil {
		t.Fatalf("reloaded provider error: %v", err)
	}

	foundManaged := false
	for _, snapshot := range reloaded.SnapshotAccounts() {
		if snapshot.ID == "managed-1" {
			foundManaged = true
			if snapshot.Disabled {
				t.Fatalf("expected managed account to be enabled after reload")
			}
		}
	}
	if !foundManaged {
		t.Fatalf("expected managed account to persist after reload")
	}
}

func TestUpdateWeightAndExport(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "accounts_state.json")

	provider, err := New(Config{
		Source:      "env",
		BearerToken: "env-bearer",
		StatePath:   statePath,
	})
	if err != nil {
		t.Fatalf("New provider error: %v", err)
	}

	if _, err := provider.ImportAccount(context.Background(), ImportRequest{
		ID:          "managed-2",
		BearerToken: "managed-bearer",
		Weight:      250,
	}); err != nil {
		t.Fatalf("ImportAccount error: %v", err)
	}

	if err := provider.UpdateWeight("managed-2", 50); err != nil {
		t.Fatalf("UpdateWeight error: %v", err)
	}

	snapshots := provider.SnapshotAccounts()
	found := false
	for _, snapshot := range snapshots {
		if snapshot.ID == "managed-2" {
			found = true
			if snapshot.Weight != 50 {
				t.Fatalf("expected weight 50, got %d", snapshot.Weight)
			}
		}
	}
	if !found {
		t.Fatalf("managed account not found in snapshots")
	}

	exports := provider.ExportAccounts()
	found = false
	for _, exported := range exports {
		if exported.ID == "managed-2" {
			found = true
			if exported.Weight != 50 {
				t.Fatalf("expected exported weight 50, got %d", exported.Weight)
			}
			if exported.BearerToken != "managed-bearer" {
				t.Fatalf("unexpected exported bearer token: %s", exported.BearerToken)
			}
		}
	}
	if !found {
		t.Fatalf("managed account not found in export")
	}
}

func TestDeleteManagedAccountPersistsRemoval(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "accounts_state.json")

	provider, err := New(Config{
		Source:      "env",
		BearerToken: "env-bearer",
		StatePath:   statePath,
	})
	if err != nil {
		t.Fatalf("New provider error: %v", err)
	}

	if _, err := provider.ImportAccount(context.Background(), ImportRequest{
		ID:          "managed-delete",
		BearerToken: "managed-bearer",
	}); err != nil {
		t.Fatalf("ImportAccount error: %v", err)
	}

	if err := provider.DeleteAccount("managed-delete"); err != nil {
		t.Fatalf("DeleteAccount error: %v", err)
	}

	reloaded, err := New(Config{
		Source:      "env",
		BearerToken: "env-bearer",
		StatePath:   statePath,
	})
	if err != nil {
		t.Fatalf("reloaded provider error: %v", err)
	}

	for _, snapshot := range reloaded.SnapshotAccounts() {
		if snapshot.ID == "managed-delete" {
			t.Fatalf("expected managed-delete to be removed after reload")
		}
	}
}

func TestAPISourceBanCallbackOnQuotaFailure(t *testing.T) {
	called := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/update" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Passkey"); got != "secret" {
			t.Fatalf("unexpected X-Passkey: %s", got)
		}
		called <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	provider := &Provider{
		cfg: Config{
			APIURL:   server.URL,
			APIToken: "secret",
		},
		httpClient: server.Client(),
	}

	item := &tokenAccount{
		ID:       "api-42",
		Source:   "api",
		RemoteID: 42,
	}
	provider.applyFailure(item, account.FailureMeta{
		Reason:  account.FailureReasonQuota,
		Message: "quota exceeded",
	})

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected API ban callback to be invoked")
	}
}

func TestWarmPoolMarksAccountsInPool(t *testing.T) {
	provider, err := New(Config{
		Source:         "env",
		BearerToken:    "env-bearer",
		ActivePoolSize: 2,
	})
	if err != nil {
		t.Fatalf("New provider error: %v", err)
	}

	if _, err := provider.ImportAccount(context.Background(), ImportRequest{
		ID:          "managed-a",
		BearerToken: "token-a",
	}); err != nil {
		t.Fatalf("ImportAccount A error: %v", err)
	}
	if _, err := provider.ImportAccount(context.Background(), ImportRequest{
		ID:          "managed-b",
		BearerToken: "token-b",
	}); err != nil {
		t.Fatalf("ImportAccount B error: %v", err)
	}

	snapshot, err := provider.WarmPool(context.Background())
	if err != nil {
		t.Fatalf("WarmPool error: %v", err)
	}
	if snapshot.WarmedAccounts != 2 {
		t.Fatalf("expected 2 warmed accounts, got %d", snapshot.WarmedAccounts)
	}

	inPool := 0
	for _, account := range provider.SnapshotAccounts() {
		if account.InPool {
			inPool++
		}
	}
	if inPool != 2 {
		t.Fatalf("expected 2 accounts marked in pool, got %d", inPool)
	}
}

func TestAcquirePrefersStickyAccountID(t *testing.T) {
	provider, err := New(Config{
		Source:      "env",
		BearerToken: "env-bearer",
	})
	if err != nil {
		t.Fatalf("New provider error: %v", err)
	}

	if _, err := provider.ImportAccount(context.Background(), ImportRequest{
		ID:          "managed-preferred",
		BearerToken: "managed-bearer",
	}); err != nil {
		t.Fatalf("ImportAccount error: %v", err)
	}

	lease, err := provider.Acquire(context.Background(), account.AcquireHint{
		Profile:            account.ProfileCLI,
		PreferredAccountID: "managed-preferred",
	})
	if err != nil {
		t.Fatalf("Acquire error: %v", err)
	}
	if lease.AccountID != "managed-preferred" {
		t.Fatalf("expected preferred account, got %s", lease.AccountID)
	}
}

func TestWarmPoolFetchesAdditionalAPIAccounts(t *testing.T) {
	var fetchCount atomic.Int64
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/fetch":
			current := fetchCount.Add(1)
			var records []map[string]any
			if current == 1 {
				records = []map[string]any{{
					"id":   1,
					"data": `{"refresh_token":"rt-1","client_id":"cid","client_secret":"secret"}`,
				}}
			} else {
				records = []map[string]any{{
					"id":   2,
					"data": `{"refresh_token":"rt-2","client_id":"cid","client_secret":"secret"}`,
				}}
			}
			_ = json.NewEncoder(w).Encode(records)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer apiServer.Close()

	oidcServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected OIDC method: %s", r.Method)
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(bodyBytes), "\"refreshToken\":\"rt-") {
			t.Fatalf("unexpected OIDC payload: %s", string(bodyBytes))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accessToken":  "bearer",
			"refreshToken": "",
			"expiresIn":    3600,
		})
	}))
	defer oidcServer.Close()

	provider, err := New(Config{
		Source:         "api",
		APIURL:         apiServer.URL,
		APIToken:       "secret",
		APICategoryID:  3,
		APIFetchCount:  1,
		ActivePoolSize: 2,
		MaxRefreshTry:  1,
		OIDCURL:        oidcServer.URL,
	})
	if err != nil {
		t.Fatalf("New provider error: %v", err)
	}

	snapshot, err := provider.WarmPool(context.Background())
	if err != nil {
		t.Fatalf("WarmPool error: %v", err)
	}
	if snapshot.WarmedAccounts != 2 {
		t.Fatalf("expected 2 warmed accounts, got %d", snapshot.WarmedAccounts)
	}
	if fetchCount.Load() < 2 {
		t.Fatalf("expected api fetch to be called at least twice, got %d", fetchCount.Load())
	}

	pool := provider.PoolSnapshot()
	if pool.WarmRuns == 0 {
		t.Fatalf("expected warm runs to be tracked")
	}
	if pool.RefillFetches == 0 {
		t.Fatalf("expected refill fetches to be tracked")
	}
}

func TestCSVSourceFailureDisablesRow(t *testing.T) {
	csvPath := filepath.Join(t.TempDir(), "accounts.csv")
	content := "enabled,refresh_token,client_id,client_secret\nTrue,rt-1,cid,secret\n"
	if err := os.WriteFile(csvPath, []byte(content), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	provider, err := New(Config{
		Source:        "csv",
		CSVPath:       csvPath,
		StatePath:     filepath.Join(t.TempDir(), "accounts_state.json"),
		MaxRefreshTry: 1,
	})
	if err != nil {
		t.Fatalf("New provider error: %v", err)
	}

	provider.applyFailure(provider.accounts[0], account.FailureMeta{
		Reason:     account.FailureReasonBan,
		BodySignal: "TEMPORARILY_SUSPENDED",
		Message:    "banned by upstream",
	})
	time.Sleep(100 * time.Millisecond)

	updated, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if !strings.Contains(string(updated), "False,rt-1") {
		t.Fatalf("expected csv row to be disabled, got:\n%s", string(updated))
	}
}
