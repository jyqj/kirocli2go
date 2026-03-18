package chat

import (
	"context"
	"io"
	"strings"

	"kirocli-go/internal/domain/account"
	"kirocli-go/internal/domain/message"
	"kirocli-go/internal/domain/stream"
	"kirocli-go/internal/ports"
	"kirocli-go/internal/tokenutil"
)

type observedStream struct {
	inner ports.UpstreamStream

	text      strings.Builder
	reasoning strings.Builder
	toolInput strings.Builder
	usage     stream.Usage
	hasUsage  bool

	// Truncation tracking
	pendingTools  map[string]string // toolCallID → toolName (started, not done)
	finishedTools map[string]bool   // toolCallID → true (completed normally)
	truncated     bool
}

func newObservedStream(inner ports.UpstreamStream) *observedStream {
	return &observedStream{
		inner:         inner,
		pendingTools:  make(map[string]string),
		finishedTools: make(map[string]bool),
	}
}

func (s *observedStream) Next(ctx context.Context) (stream.Event, error) {
	event, err := s.inner.Next(ctx)
	if err != nil {
		return event, err
	}

	switch event.Type {
	case stream.EventTypeText:
		s.text.WriteString(event.Text)
	case stream.EventTypeReasoning:
		s.reasoning.WriteString(event.Text)
	case stream.EventTypeToolCall:
		if event.ToolCall != nil {
			s.toolInput.WriteString(string(event.ToolCall.Arguments))
			if event.ToolCall.ID != "" && event.ToolCall.Name != "" {
				s.pendingTools[event.ToolCall.ID] = event.ToolCall.Name
			}
			if event.ToolCallDone && event.ToolCall.ID != "" {
				delete(s.pendingTools, event.ToolCall.ID)
				s.finishedTools[event.ToolCall.ID] = true
			}
		}
	case stream.EventTypeUsage:
		if event.Usage != nil {
			if event.Usage.InputTokens > 0 {
				s.usage.InputTokens = event.Usage.InputTokens
			}
			if event.Usage.OutputTokens > 0 {
				s.usage.OutputTokens = event.Usage.OutputTokens
			}
			if event.Usage.TotalTokens > 0 {
				s.usage.TotalTokens = event.Usage.TotalTokens
			}
			if event.Usage.CacheWriteTokens > 0 {
				s.usage.CacheWriteTokens = event.Usage.CacheWriteTokens
			}
			if event.Usage.CacheReadTokens > 0 {
				s.usage.CacheReadTokens = event.Usage.CacheReadTokens
			}
			if event.Usage.Credits > 0 {
				s.usage.Credits = event.Usage.Credits
			}
			s.hasUsage = true
		}
	}

	return event, nil
}

func (s *observedStream) Close() error {
	return s.inner.Close()
}

func (s *observedStream) SuccessMeta(req message.UnifiedRequest, attempts int) account.SuccessMeta {
	inputTokens := s.usage.InputTokens
	if inputTokens <= 0 {
		if req.Metadata.EstimatedInputTokens > 0 {
			inputTokens = req.Metadata.EstimatedInputTokens
		} else {
			inputTokens = req.Metadata.RemainingInputTokens + req.Metadata.CacheCreationInputTokens + req.Metadata.CacheReadInputTokens
		}
	}

	outputTokens := s.usage.OutputTokens
	if outputTokens <= 0 {
		outputTokens = tokenutil.CountText(s.text.String() + s.reasoning.String() + s.toolInput.String())
	}

	totalTokens := s.usage.TotalTokens
	if totalTokens <= 0 {
		totalTokens = inputTokens + outputTokens
	}

	return account.SuccessMeta{
		RequestID:                req.Metadata.ClientRequestID,
		Model:                    req.Model,
		Tokens:                   totalTokens,
		InputTokens:              inputTokens,
		OutputTokens:             outputTokens,
		Credits:                  s.usage.Credits,
		CacheCreationInputTokens: req.Metadata.CacheCreationInputTokens,
		CacheReadInputTokens:     req.Metadata.CacheReadInputTokens,
		Attempts:                 attempts,
	}
}

func (s *observedStream) PreciseUsage() *stream.Usage {
	if !s.hasUsage {
		return nil
	}
	usage := s.usage
	return &usage
}

func (s *observedStream) ApproxOutputTokens() int {
	return tokenutil.CountText(s.text.String() + s.reasoning.String() + s.toolInput.String())
}

// WasTruncated returns true if the stream ended with unclosed tool calls,
// indicating the upstream API truncated the response.
func (s *observedStream) WasTruncated() bool {
	return s.truncated || len(s.pendingTools) > 0
}

// MarkTruncated explicitly marks this stream as truncated.
func (s *observedStream) MarkTruncated() {
	s.truncated = true
}

// PendingToolCalls returns tool calls that started but were never completed.
func (s *observedStream) PendingToolCalls() map[string]string {
	return s.pendingTools
}

// PartialContent returns the accumulated text at the point of truncation.
func (s *observedStream) PartialContent() string {
	return s.text.String()
}

var _ ports.UpstreamStream = (*observedStream)(nil)

func drainObservedStream(ctx context.Context, upstream ports.UpstreamStream) (*observedStream, error) {
	observed := newObservedStream(upstream)
	for {
		_, err := observed.Next(ctx)
		if err == io.EOF {
			return observed, nil
		}
		if err != nil {
			return observed, err
		}
	}
}
