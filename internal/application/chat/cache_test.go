package chat

import (
	"context"
	"io"
	"strings"
	"testing"

	"kirocli-go/internal/domain/account"
	"kirocli-go/internal/domain/message"
	"kirocli-go/internal/domain/model"
	"kirocli-go/internal/ports"
)

type captureFormatter struct {
	lastRequest message.UnifiedRequest
}

func (f *captureFormatter) FormatStream(ctx context.Context, req message.UnifiedRequest, upstream ports.UpstreamStream, w io.Writer) error {
	_ = ctx
	_ = upstream
	_ = w
	f.lastRequest = req
	return nil
}

func (f *captureFormatter) FormatJSON(ctx context.Context, req message.UnifiedRequest, upstream ports.UpstreamStream, w io.Writer) error {
	_ = ctx
	_ = upstream
	_ = w
	f.lastRequest = req
	return nil
}

type cacheTestCatalog struct{}

func (c *cacheTestCatalog) Resolve(ctx context.Context, externalModel string) (model.ResolvedModel, error) {
	_ = ctx
	return model.ResolvedModel{ExternalName: externalModel, InternalName: externalModel}, nil
}

func (c *cacheTestCatalog) List(ctx context.Context) ([]model.ResolvedModel, error) {
	_ = ctx
	return nil, nil
}

type cacheTestTokens struct{}

func (p *cacheTestTokens) Acquire(ctx context.Context, hint account.AcquireHint) (account.Lease, error) {
	_ = ctx
	_ = hint
	return account.Lease{AccountID: "a1", Token: "t1", Profile: account.ProfileCLI}, nil
}

func (p *cacheTestTokens) ReportSuccess(ctx context.Context, lease account.Lease, meta account.SuccessMeta) error {
	_ = ctx
	_ = lease
	_ = meta
	return nil
}

func (p *cacheTestTokens) ReportFailure(ctx context.Context, lease account.Lease, meta account.FailureMeta) error {
	_ = ctx
	_ = lease
	_ = meta
	return nil
}

type cacheTestUpstream struct{}

func (u *cacheTestUpstream) Send(ctx context.Context, req ports.UpstreamRequest) (ports.UpstreamStream, error) {
	_ = ctx
	_ = req
	return &stubStream{}, nil
}

func TestPrepareAnthropicCacheMetadataMissThenHit(t *testing.T) {
	cache := NewFakeCache()
	formatter := &captureFormatter{}

	service, err := NewService(Dependencies{
		Tokens:             &cacheTestTokens{},
		Upstream:           &cacheTestUpstream{},
		Catalog:            &cacheTestCatalog{},
		OpenAIFormatter:    formatter,
		AnthropicFormatter: formatter,
		Cache:              cache,
	})
	if err != nil {
		t.Fatalf("NewService error: %v", err)
	}

	baseReq := message.UnifiedRequest{
		Protocol:     message.ProtocolAnthropic,
		Model:        "claude-sonnet-4.5",
		SystemPrompt: "system",
		Messages: []message.UnifiedMessage{{
			Role: message.RoleUser,
			Parts: []message.Part{{
				Type: message.PartTypeText,
				Text: strings.Repeat("a", 6000),
			}},
		}},
		Metadata: message.RequestMetadata{
			EstimatedInputTokens: 1500,
			FakeCacheKey:         42,
		},
	}

	if err := service.Handle(context.Background(), baseReq, ports.ResponseFormatAnthropic, io.Discard); err != nil {
		t.Fatalf("first Handle error: %v", err)
	}
	if formatter.lastRequest.Metadata.CacheHit {
		t.Fatalf("expected first request to be cache miss")
	}
	if formatter.lastRequest.Metadata.CacheCreationInputTokens == 0 {
		t.Fatalf("expected cache creation tokens on first request")
	}

	if err := service.Handle(context.Background(), baseReq, ports.ResponseFormatAnthropic, io.Discard); err != nil {
		t.Fatalf("second Handle error: %v", err)
	}
	if !formatter.lastRequest.Metadata.CacheHit {
		t.Fatalf("expected second request to be cache hit")
	}
	if formatter.lastRequest.Metadata.CacheReadInputTokens == 0 {
		t.Fatalf("expected cache read tokens on second request")
	}
}

func TestComputeScopedCacheKeySeparatesNamespaces(t *testing.T) {
	body := []byte(`{"model":"claude","messages":[{"role":"user","content":"hello"}]}`)
	a := ComputeScopedCacheKey("key-a", body)
	b := ComputeScopedCacheKey("key-b", body)
	if a == b {
		t.Fatal("expected different scoped cache keys for different api keys")
	}
	if a != ComputeScopedCacheKey("key-a", body) {
		t.Fatal("expected deterministic scoped cache key")
	}
}

func TestFakeCacheSnapshotTracksTotals(t *testing.T) {
	cache := NewFakeCache()
	if cache.Lookup(1) {
		t.Fatal("expected first lookup to miss")
	}
	if !cache.Lookup(1) {
		t.Fatal("expected second lookup to hit")
	}
	snapshot := cache.Snapshot()
	if snapshot.Lookups != 2 || snapshot.Hits != 1 || snapshot.Misses != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if snapshot.HitRate != 0.5 {
		t.Fatalf("expected hit rate 0.5, got %v", snapshot.HitRate)
	}
}
