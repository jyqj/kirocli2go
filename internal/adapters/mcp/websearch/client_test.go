package websearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"kirocli-go/internal/domain/account"
)

type testTokenProvider struct {
	lease          account.Lease
	successCalled  bool
	failureCalled  bool
	lastFailReason account.FailureReason
}

func (p *testTokenProvider) Acquire(ctx context.Context, hint account.AcquireHint) (account.Lease, error) {
	_ = ctx
	_ = hint
	return p.lease, nil
}

func (p *testTokenProvider) ReportSuccess(ctx context.Context, lease account.Lease, meta account.SuccessMeta) error {
	_ = ctx
	_ = lease
	_ = meta
	p.successCalled = true
	return nil
}

func (p *testTokenProvider) ReportFailure(ctx context.Context, lease account.Lease, meta account.FailureMeta) error {
	_ = ctx
	_ = lease
	p.failureCalled = true
	p.lastFailReason = meta.Reason
	return nil
}

func TestHandleAnthropicJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("unexpected auth header: %s", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{
				"content": []map[string]any{{
					"type": "text",
					"text": `{"results":[{"title":"OpenAI","url":"https://openai.com","snippet":"Research lab"},{"title":"AWS","url":"https://aws.amazon.com","snippet":"Cloud"}]}`,
				}},
			},
		})
	}))
	defer server.Close()

	tokens := &testTokenProvider{
		lease: account.Lease{
			AccountID: "api-1",
			Token:     "test-token",
			Profile:   account.ProfileCLI,
		},
	}
	client := New(Config{URL: server.URL}, tokens)

	recorder := httptest.NewRecorder()
	err := client.HandleAnthropic(context.Background(), SearchRequest{
		RequestID:   "req-1",
		Model:       "claude-sonnet-4.5",
		Query:       "OpenAI latest",
		MaxUses:     1,
		InputTokens: 123,
		Stream:      false,
	}, recorder)
	if err != nil {
		t.Fatalf("HandleAnthropic error: %v", err)
	}
	if !tokens.successCalled {
		t.Fatalf("expected ReportSuccess to be called")
	}

	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	content, ok := response["content"].([]any)
	if !ok || len(content) != 3 {
		t.Fatalf("unexpected content payload: %#v", response["content"])
	}
}

func TestHandleAnthropicBanSignalReportsFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"reason":"TEMPORARILY_SUSPENDED"}`))
	}))
	defer server.Close()

	tokens := &testTokenProvider{
		lease: account.Lease{
			AccountID: "api-1",
			Token:     "test-token",
			Profile:   account.ProfileCLI,
		},
	}
	client := New(Config{URL: server.URL}, tokens)

	recorder := httptest.NewRecorder()
	err := client.HandleAnthropic(context.Background(), SearchRequest{
		RequestID:   "req-ban",
		Model:       "claude-sonnet-4.5",
		Query:       "blocked",
		InputTokens: 10,
		Stream:      false,
	}, recorder)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !tokens.failureCalled {
		t.Fatalf("expected ReportFailure to be called")
	}
	if tokens.lastFailReason != account.FailureReasonBan {
		t.Fatalf("expected ban failure reason, got %s", tokens.lastFailReason)
	}
}
