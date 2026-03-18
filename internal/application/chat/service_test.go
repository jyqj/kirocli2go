package chat

import (
	"context"
	"io"
	"testing"

	appstats "kirocli-go/internal/application/stats"
	"kirocli-go/internal/domain/account"
	domainerrors "kirocli-go/internal/domain/errors"
	"kirocli-go/internal/domain/message"
	"kirocli-go/internal/domain/model"
	"kirocli-go/internal/domain/stream"
	"kirocli-go/internal/ports"
)

type stubTokenProvider struct {
	acquireCount       int
	reportSuccessCount int
	reportFailureCount int
	lastFailure        account.FailureMeta
}

func (p *stubTokenProvider) Acquire(ctx context.Context, hint account.AcquireHint) (account.Lease, error) {
	_ = ctx
	_ = hint
	p.acquireCount++
	return account.Lease{
		AccountID: "account-1",
		Token:     "token-1",
		Profile:   account.ProfileCLI,
	}, nil
}

func (p *stubTokenProvider) ReportSuccess(ctx context.Context, lease account.Lease, meta account.SuccessMeta) error {
	_ = ctx
	_ = lease
	_ = meta
	p.reportSuccessCount++
	return nil
}

func (p *stubTokenProvider) ReportFailure(ctx context.Context, lease account.Lease, meta account.FailureMeta) error {
	_ = ctx
	_ = lease
	p.reportFailureCount++
	p.lastFailure = meta
	return nil
}

type stubCatalog struct{}

func (c *stubCatalog) Resolve(ctx context.Context, externalModel string) (model.ResolvedModel, error) {
	_ = ctx
	return model.ResolvedModel{
		ExternalName: externalModel,
		InternalName: externalModel,
	}, nil
}

func (c *stubCatalog) List(ctx context.Context) ([]model.ResolvedModel, error) {
	_ = ctx
	return nil, nil
}

type stubUpstream struct {
	sendCount int
}

func (u *stubUpstream) Send(ctx context.Context, req ports.UpstreamRequest) (ports.UpstreamStream, error) {
	_ = ctx
	_ = req
	u.sendCount++
	if u.sendCount == 1 {
		return nil, &domainerrors.UpstreamError{
			Category:  domainerrors.CategoryNetwork,
			Message:   "temporary network failure",
			Retryable: true,
		}
	}
	return &stubStream{}, nil
}

type stubSignalUpstream struct{}

func (u *stubSignalUpstream) Send(ctx context.Context, req ports.UpstreamRequest) (ports.UpstreamStream, error) {
	_ = ctx
	_ = req
	return nil, &domainerrors.UpstreamError{
		Category:   domainerrors.CategoryBan,
		Signal:     "TEMPORARILY_SUSPENDED",
		Message:    "suspended by upstream",
		StatusCode: 403,
	}
}

type stubStream struct{}

func (s *stubStream) Next(ctx context.Context) (stream.Event, error) {
	_ = ctx
	return stream.Event{}, io.EOF
}

func (s *stubStream) Close() error {
	return nil
}

type stubFormatter struct{}

func (f *stubFormatter) FormatStream(ctx context.Context, req message.UnifiedRequest, upstream ports.UpstreamStream, w io.Writer) error {
	_ = ctx
	_ = req
	_ = upstream
	_ = w
	return nil
}

func (f *stubFormatter) FormatJSON(ctx context.Context, req message.UnifiedRequest, upstream ports.UpstreamStream, w io.Writer) error {
	_ = ctx
	_ = req
	_ = upstream
	_ = w
	return nil
}

func TestHandleRetriesRetryableSendError(t *testing.T) {
	tokens := &stubTokenProvider{}
	upstream := &stubUpstream{}
	formatter := &stubFormatter{}

	service, err := NewService(Dependencies{
		Tokens:             tokens,
		Upstream:           upstream,
		Catalog:            &stubCatalog{},
		OpenAIFormatter:    formatter,
		AnthropicFormatter: formatter,
	})
	if err != nil {
		t.Fatalf("NewService error: %v", err)
	}

	req := message.UnifiedRequest{
		Protocol: message.ProtocolOpenAI,
		Model:    "claude-sonnet-4.5",
	}

	if err := service.Handle(context.Background(), req, ports.ResponseFormatOpenAI, io.Discard); err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	if upstream.sendCount != 2 {
		t.Fatalf("expected 2 upstream send attempts, got %d", upstream.sendCount)
	}
	if tokens.acquireCount != 2 {
		t.Fatalf("expected 2 acquire attempts, got %d", tokens.acquireCount)
	}
	if tokens.reportFailureCount != 1 {
		t.Fatalf("expected 1 failure report, got %d", tokens.reportFailureCount)
	}
	if tokens.reportSuccessCount != 1 {
		t.Fatalf("expected 1 success report, got %d", tokens.reportSuccessCount)
	}
	if tokens.lastFailure.Reason != account.FailureReasonNetwork {
		t.Fatalf("expected network failure reason, got %s", tokens.lastFailure.Reason)
	}
}

func TestHandleRecordsBodySignalInRequestLog(t *testing.T) {
	tokens := &stubTokenProvider{}
	logs := appstats.NewRequestLogRing(10)
	formatter := &stubFormatter{}

	service, err := NewService(Dependencies{
		Tokens:             tokens,
		Upstream:           &stubSignalUpstream{},
		Catalog:            &stubCatalog{},
		OpenAIFormatter:    formatter,
		AnthropicFormatter: formatter,
		RequestLogs:        logs,
	})
	if err != nil {
		t.Fatalf("NewService error: %v", err)
	}

	req := message.UnifiedRequest{
		Protocol: message.ProtocolOpenAI,
		Model:    "claude-sonnet-4.5",
		Metadata: message.RequestMetadata{
			ClientRequestID: "req-signal",
			Endpoint:        "/v1/chat/completions",
		},
	}

	if err := service.Handle(context.Background(), req, ports.ResponseFormatOpenAI, io.Discard); err == nil {
		t.Fatalf("expected Handle to fail")
	}

	entries := logs.List(10)
	if len(entries) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(entries))
	}
	if entries[0].BodySignal != "TEMPORARILY_SUSPENDED" {
		t.Fatalf("expected body signal TEMPORARILY_SUSPENDED, got %q", entries[0].BodySignal)
	}
	if tokens.lastFailure.BodySignal != "TEMPORARILY_SUSPENDED" {
		t.Fatalf("expected failure meta signal TEMPORARILY_SUSPENDED, got %q", tokens.lastFailure.BodySignal)
	}
}
