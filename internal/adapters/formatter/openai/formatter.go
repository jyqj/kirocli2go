package openaiformatter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kirocli-go/internal/adapters/formatter/shared"
	domainerrors "kirocli-go/internal/domain/errors"
	"kirocli-go/internal/domain/message"
	"kirocli-go/internal/domain/stream"
	"kirocli-go/internal/ports"
	"kirocli-go/internal/tokenutil"
)

type Formatter struct{}

type toolAccumulator struct {
	Name  string
	Parts strings.Builder
	Index int
}

func New() *Formatter {
	return &Formatter{}
}

func (f *Formatter) FormatStream(ctx context.Context, req message.UnifiedRequest, upstream ports.UpstreamStream, w io.Writer) error {
	prepareSSEHeaders(w)

	flusher, _ := w.(http.Flusher)
	splitter := &shared.ThinkingSplitter{}
	toolIndexes := make(map[string]int)
	nextToolIndex := 0
	hasToolCall := false
	var outputText strings.Builder
	var reasoningText strings.Builder
	var toolInputText strings.Builder
	var usage *stream.Usage
	chatID := "chatcmpl-" + strconv.FormatInt(time.Now().UnixNano(), 10)

	if err := writeSSE(w, map[string]any{
		"id":      chatID,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   req.Model,
		"choices": []map[string]any{{
			"index": 0,
			"delta": map[string]any{
				"role": "assistant",
			},
			"finish_reason": nil,
		}},
	}); err != nil {
		return err
	}
	flush(flusher)

	for {
		event, err := upstream.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if event.Error != nil {
			return event.Error
		}

		switch event.Type {
		case stream.EventTypeText:
			for _, segment := range splitter.Feed(event.Text, false) {
				payload := map[string]any{
					"id":      chatID,
					"object":  "chat.completion.chunk",
					"created": time.Now().Unix(),
					"model":   req.Model,
					"choices": []map[string]any{{
						"index":         0,
						"finish_reason": nil,
					}},
				}
				delta := map[string]any{}
				if segment.Kind == shared.SegmentKindReasoning {
					reasoningText.WriteString(segment.Text)
					delta["reasoning_content"] = segment.Text
				} else {
					outputText.WriteString(segment.Text)
					delta["content"] = segment.Text
				}
				payload["choices"].([]map[string]any)[0]["delta"] = delta
				if err := writeSSE(w, payload); err != nil {
					return err
				}
				flush(flusher)
			}
		case stream.EventTypeReasoning:
			reasoningText.WriteString(event.Text)
			if err := writeSSE(w, map[string]any{
				"id":      chatID,
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   req.Model,
				"choices": []map[string]any{{
					"index": 0,
					"delta": map[string]any{
						"reasoning_content": event.Text,
					},
					"finish_reason": nil,
				}},
			}); err != nil {
				return err
			}
			flush(flusher)
		case stream.EventTypeToolCall:
			hasToolCall = true
			if event.ToolCall == nil {
				continue
			}
			idx, ok := toolIndexes[event.ToolCall.ID]
			if !ok {
				idx = nextToolIndex
				toolIndexes[event.ToolCall.ID] = idx
				nextToolIndex++
			}
			arguments := string(event.ToolCall.Arguments)
			toolInputText.WriteString(arguments)
			if err := writeSSE(w, map[string]any{
				"id":      chatID,
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   req.Model,
				"choices": []map[string]any{{
					"index": 0,
					"delta": map[string]any{
						"tool_calls": []map[string]any{{
							"index": idx,
							"id":    event.ToolCall.ID,
							"type":  "function",
							"function": map[string]any{
								"name":      event.ToolCall.Name,
								"arguments": arguments,
							},
						}},
					},
					"finish_reason": nil,
				}},
			}); err != nil {
				return err
			}
			flush(flusher)
		case stream.EventTypeUsage:
			usage = event.Usage
		}
	}

	for _, segment := range splitter.Feed("", true) {
		payload := map[string]any{
			"id":      chatID,
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   req.Model,
			"choices": []map[string]any{{
				"index":         0,
				"finish_reason": nil,
			}},
		}
		delta := map[string]any{}
		if segment.Kind == shared.SegmentKindReasoning {
			reasoningText.WriteString(segment.Text)
			delta["reasoning_content"] = segment.Text
		} else {
			outputText.WriteString(segment.Text)
			delta["content"] = segment.Text
		}
		payload["choices"].([]map[string]any)[0]["delta"] = delta
		if err := writeSSE(w, payload); err != nil {
			return err
		}
		flush(flusher)
	}

	finishReason := "stop"
	if hasToolCall {
		finishReason = "tool_calls"
	}
	promptTokens, completionTokens, totalTokens := usageTotals(usage, estimateOutputTokens(outputText.String()+reasoningText.String()+toolInputText.String()))

	if err := writeSSE(w, map[string]any{
		"id":      chatID,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   req.Model,
		"choices": []map[string]any{{
			"index":         0,
			"delta":         map[string]any{},
			"finish_reason": finishReason,
		}},
		"usage": map[string]any{
			"prompt_tokens":     promptTokens,
			"completion_tokens": completionTokens,
			"total_tokens":      totalTokens,
		},
	}); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "data: [DONE]\n\n"); err != nil {
		return err
	}
	flush(flusher)
	return nil
}

func (f *Formatter) FormatJSON(ctx context.Context, req message.UnifiedRequest, upstream ports.UpstreamStream, w io.Writer) error {
	splitter := &shared.ThinkingSplitter{}
	var content strings.Builder
	var reasoning strings.Builder
	order := make([]string, 0)
	tools := make(map[string]*toolAccumulator)
	var usage *stream.Usage

	for {
		event, err := upstream.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if event.Error != nil {
			return event.Error
		}

		switch event.Type {
		case stream.EventTypeText:
			for _, segment := range splitter.Feed(event.Text, false) {
				if segment.Kind == shared.SegmentKindReasoning {
					reasoning.WriteString(segment.Text)
				} else {
					content.WriteString(segment.Text)
				}
			}
		case stream.EventTypeReasoning:
			reasoning.WriteString(event.Text)
		case stream.EventTypeToolCall:
			if event.ToolCall == nil {
				continue
			}
			acc, ok := tools[event.ToolCall.ID]
			if !ok {
				acc = &toolAccumulator{
					Name:  event.ToolCall.Name,
					Index: len(order),
				}
				tools[event.ToolCall.ID] = acc
				order = append(order, event.ToolCall.ID)
			}
			if event.ToolCall.Name != "" {
				acc.Name = event.ToolCall.Name
			}
			acc.Parts.WriteString(string(event.ToolCall.Arguments))
		case stream.EventTypeUsage:
			usage = event.Usage
		}
	}

	for _, segment := range splitter.Feed("", true) {
		if segment.Kind == shared.SegmentKindReasoning {
			reasoning.WriteString(segment.Text)
		} else {
			content.WriteString(segment.Text)
		}
	}

	toolCalls := make([]map[string]any, 0, len(order))
	for _, id := range order {
		acc := tools[id]
		raw := acc.Parts.String()
		if strings.TrimSpace(raw) == "" {
			raw = "{}"
		}
		toolCalls = append(toolCalls, map[string]any{
			"id":    id,
			"type":  "function",
			"index": acc.Index,
			"function": map[string]any{
				"name":      acc.Name,
				"arguments": raw,
			},
		})
	}

	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}
	promptTokens, completionTokens, totalTokens := usageTotals(usage, estimateOutputTokens(content.String()+reasoning.String()))
	for _, id := range order {
		if tools[id] != nil {
			completionTokens = maxInt(completionTokens, estimateOutputTokens(content.String()+reasoning.String()+tools[id].Parts.String()))
			totalTokens = promptTokens + completionTokens
		}
	}

	messageObj := map[string]any{
		"role":    "assistant",
		"content": content.String(),
	}
	if reasoning.Len() > 0 {
		messageObj["reasoning_content"] = reasoning.String()
	}
	if len(toolCalls) > 0 {
		messageObj["tool_calls"] = toolCalls
	}

	return json.NewEncoder(w).Encode(map[string]any{
		"id":      "chatcmpl-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   req.Model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       messageObj,
			"finish_reason": finishReason,
		}},
		"usage": map[string]any{
			"prompt_tokens":     promptTokens,
			"completion_tokens": completionTokens,
			"total_tokens":      totalTokens,
		},
	})
}

func prepareSSEHeaders(w io.Writer) {
	if rw, ok := w.(interface{ Header() http.Header }); ok {
		headers := rw.Header()
		headers.Set("Content-Type", "text/event-stream")
		headers.Set("Cache-Control", "no-cache")
		headers.Set("Connection", "keep-alive")
	}
}

func writeSSE(w io.Writer, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}

func flush(flusher http.Flusher) {
	if flusher != nil {
		flusher.Flush()
	}
}

func asUpstreamError(err error) *domainerrors.UpstreamError {
	if err == nil {
		return nil
	}
	if upstreamErr, ok := err.(*domainerrors.UpstreamError); ok {
		return upstreamErr
	}
	return domainerrors.New(domainerrors.CategoryUnknown, err.Error())
}

func estimateOutputTokens(text string) int {
	return tokenutil.CountText(text)
}

func usageTotals(usage *stream.Usage, fallbackOutput int) (int, int, int) {
	if usage == nil {
		return 0, fallbackOutput, fallbackOutput
	}
	completion := usage.OutputTokens
	if completion == 0 {
		completion = fallbackOutput
	}
	total := usage.TotalTokens
	if total == 0 {
		total = usage.InputTokens + completion
	}
	return usage.InputTokens, completion, total
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
