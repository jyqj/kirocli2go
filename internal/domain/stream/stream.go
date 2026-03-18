package stream

import (
	domainerrors "kirocli-go/internal/domain/errors"
	"kirocli-go/internal/domain/message"
)

type EventType string

const (
	EventTypeText       EventType = "text"
	EventTypeReasoning  EventType = "reasoning"
	EventTypeToolCall   EventType = "tool_call"
	EventTypeUsage      EventType = "usage"
	EventTypeCompletion EventType = "completion"
	EventTypeError      EventType = "error"
)

type Usage struct {
	InputTokens      int
	OutputTokens     int
	TotalTokens      int
	CacheWriteTokens int
	CacheReadTokens  int
	Credits          float64
}

type Event struct {
	Type         EventType
	Text         string
	ToolCall     *message.ToolCall
	ToolCallDone bool
	Usage        *Usage
	FinishReason string
	Error        *domainerrors.UpstreamError
}
