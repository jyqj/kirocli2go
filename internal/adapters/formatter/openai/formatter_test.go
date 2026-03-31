package openaiformatter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"kirocli-go/internal/domain/message"
	"kirocli-go/internal/domain/stream"
	"kirocli-go/internal/ports"
)

type stubUpstreamStream struct {
	events []stream.Event
	index  int
}

func (s *stubUpstreamStream) Next(ctx context.Context) (stream.Event, error) {
	_ = ctx
	if s.index >= len(s.events) {
		return stream.Event{}, io.EOF
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func (s *stubUpstreamStream) Close() error {
	return nil
}

var _ ports.UpstreamStream = (*stubUpstreamStream)(nil)

func TestFormatJSONUsesPreciseUsage(t *testing.T) {
	formatter := New()
	streamIn := &stubUpstreamStream{
		events: []stream.Event{
			{Type: stream.EventTypeText, Text: "hello"},
			{Type: stream.EventTypeUsage, Usage: &stream.Usage{
				InputTokens:  123,
				OutputTokens: 45,
				TotalTokens:  168,
			}},
		},
	}

	var buf bytes.Buffer
	if err := formatter.FormatJSON(context.Background(), message.UnifiedRequest{
		Model: "claude-sonnet-4.5",
	}, streamIn, &buf); err != nil {
		t.Fatalf("FormatJSON error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	usage := payload["usage"].(map[string]any)
	if usage["prompt_tokens"].(float64) != 123 {
		t.Fatalf("expected prompt_tokens 123, got %v", usage["prompt_tokens"])
	}
	if usage["completion_tokens"].(float64) != 45 {
		t.Fatalf("expected completion_tokens 45, got %v", usage["completion_tokens"])
	}
	if usage["total_tokens"].(float64) != 168 {
		t.Fatalf("expected total_tokens 168, got %v", usage["total_tokens"])
	}
}

func TestFormatJSONExposesCacheHeadersAndPromptTokenDetails(t *testing.T) {
	formatter := New()
	streamIn := &stubUpstreamStream{
		events: []stream.Event{
			{Type: stream.EventTypeText, Text: "hello"},
			{Type: stream.EventTypeUsage, Usage: &stream.Usage{
				InputTokens:  20,
				OutputTokens: 10,
				TotalTokens:  30,
			}},
		},
	}

	rec := httptest.NewRecorder()
	req := message.UnifiedRequest{
		Model: "claude-sonnet-4.5",
		Metadata: message.RequestMetadata{
			APIKeyID:                 "team-a",
			FakeCacheKey:             1,
			CacheHit:                 true,
			CacheReadInputTokens:     128,
			CacheCreationInputTokens: 0,
		},
	}
	if err := formatter.FormatJSON(context.Background(), req, streamIn, rec); err != nil {
		t.Fatalf("FormatJSON error: %v", err)
	}
	if rec.Header().Get("X-Kiro-Cache-Status") != "hit" {
		t.Fatalf("expected cache status hit, got %q", rec.Header().Get("X-Kiro-Cache-Status"))
	}
	if rec.Header().Get("X-Kiro-Auth-Key") != "team-a" {
		t.Fatalf("expected auth key header team-a, got %q", rec.Header().Get("X-Kiro-Auth-Key"))
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	usage := payload["usage"].(map[string]any)
	details := usage["prompt_tokens_details"].(map[string]any)
	if details["cached_tokens"].(float64) != 128 {
		t.Fatalf("expected cached_tokens 128, got %v", details["cached_tokens"])
	}
}
