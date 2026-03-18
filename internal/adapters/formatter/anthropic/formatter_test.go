package anthropicformatter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

func TestFormatJSONUsesFakeCacheAndPreciseOutputTokens(t *testing.T) {
	formatter := New()
	streamIn := &stubUpstreamStream{
		events: []stream.Event{
			{Type: stream.EventTypeText, Text: "hello"},
			{Type: stream.EventTypeUsage, Usage: &stream.Usage{
				InputTokens:  2000,
				OutputTokens: 77,
				TotalTokens:  2077,
			}},
		},
	}

	req := message.UnifiedRequest{
		Model: "claude-sonnet-4.5",
		Metadata: message.RequestMetadata{
			RemainingInputTokens:     200,
			CacheCreationInputTokens: 0,
			CacheReadInputTokens:     1800,
		},
	}

	var buf bytes.Buffer
	if err := formatter.FormatJSON(context.Background(), req, streamIn, &buf); err != nil {
		t.Fatalf("FormatJSON error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	usage := payload["usage"].(map[string]any)
	if usage["input_tokens"].(float64) != 200 {
		t.Fatalf("expected input_tokens 200, got %v", usage["input_tokens"])
	}
	if usage["cache_read_input_tokens"].(float64) != 1800 {
		t.Fatalf("expected cache_read_input_tokens 1800, got %v", usage["cache_read_input_tokens"])
	}
	if usage["output_tokens"].(float64) != 77 {
		t.Fatalf("expected output_tokens 77, got %v", usage["output_tokens"])
	}
}
