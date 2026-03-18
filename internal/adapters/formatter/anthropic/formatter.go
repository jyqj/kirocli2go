package anthropicformatter

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

func New() *Formatter {
	return &Formatter{}
}

func (f *Formatter) FormatStream(ctx context.Context, req message.UnifiedRequest, upstream ports.UpstreamStream, w io.Writer) error {
	prepareSSEHeaders(w)

	flusher, _ := w.(http.Flusher)
	splitter := &shared.ThinkingSplitter{}
	msgID := "msg_" + strconv.FormatInt(time.Now().UnixNano(), 10)

	blockIndex := -1
	activeText := -1
	activeThinking := -1
	toolBlocks := make(map[string]int)
	hasToolUse := false
	var outputText strings.Builder
	var outputReasoning strings.Builder
	var toolInputs strings.Builder
	var usage *stream.Usage

	writeEvent(w, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":      msgID,
			"type":    "message",
			"role":    "assistant",
			"model":   req.Model,
			"content": []any{},
			"usage": map[string]any{
				"input_tokens":                0,
				"output_tokens":               0,
				"cache_creation_input_tokens": 0,
				"cache_read_input_tokens":     0,
			},
		},
	})
	flush(flusher)

	closeText := func() error {
		if activeText >= 0 {
			if err := writeEvent(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": activeText}); err != nil {
				return err
			}
			activeText = -1
			flush(flusher)
		}
		return nil
	}
	closeThinking := func() error {
		if activeThinking >= 0 {
			if err := writeEvent(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": activeThinking}); err != nil {
				return err
			}
			activeThinking = -1
			flush(flusher)
		}
		return nil
	}

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
				switch segment.Kind {
				case shared.SegmentKindText:
					outputText.WriteString(segment.Text)
					if err := closeThinking(); err != nil {
						return err
					}
					if activeText < 0 {
						blockIndex++
						activeText = blockIndex
						if err := writeEvent(w, "content_block_start", map[string]any{
							"type":  "content_block_start",
							"index": activeText,
							"content_block": map[string]any{
								"type": "text",
								"text": "",
							},
						}); err != nil {
							return err
						}
					}
					if err := writeEvent(w, "content_block_delta", map[string]any{
						"type":  "content_block_delta",
						"index": activeText,
						"delta": map[string]any{
							"type": "text_delta",
							"text": segment.Text,
						},
					}); err != nil {
						return err
					}
				case shared.SegmentKindReasoning:
					outputReasoning.WriteString(segment.Text)
					if err := closeText(); err != nil {
						return err
					}
					if activeThinking < 0 {
						blockIndex++
						activeThinking = blockIndex
						if err := writeEvent(w, "content_block_start", map[string]any{
							"type":  "content_block_start",
							"index": activeThinking,
							"content_block": map[string]any{
								"type":     "thinking",
								"thinking": "",
							},
						}); err != nil {
							return err
						}
					}
					if err := writeEvent(w, "content_block_delta", map[string]any{
						"type":  "content_block_delta",
						"index": activeThinking,
						"delta": map[string]any{
							"type":     "thinking_delta",
							"thinking": segment.Text,
						},
					}); err != nil {
						return err
					}
				}
				flush(flusher)
			}
		case stream.EventTypeReasoning:
			outputReasoning.WriteString(event.Text)
			if err := closeText(); err != nil {
				return err
			}
			if activeThinking < 0 {
				blockIndex++
				activeThinking = blockIndex
				if err := writeEvent(w, "content_block_start", map[string]any{
					"type":  "content_block_start",
					"index": activeThinking,
					"content_block": map[string]any{
						"type":     "thinking",
						"thinking": "",
					},
				}); err != nil {
					return err
				}
			}
			if err := writeEvent(w, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": activeThinking,
				"delta": map[string]any{
					"type":     "thinking_delta",
					"thinking": event.Text,
				},
			}); err != nil {
				return err
			}
			flush(flusher)
		case stream.EventTypeToolCall:
			hasToolUse = true
			if err := closeText(); err != nil {
				return err
			}
			if err := closeThinking(); err != nil {
				return err
			}
			if event.ToolCall == nil {
				continue
			}
			idx, ok := toolBlocks[event.ToolCall.ID]
			if !ok {
				blockIndex++
				idx = blockIndex
				toolBlocks[event.ToolCall.ID] = idx
				if err := writeEvent(w, "content_block_start", map[string]any{
					"type":  "content_block_start",
					"index": idx,
					"content_block": map[string]any{
						"type":  "tool_use",
						"id":    event.ToolCall.ID,
						"name":  event.ToolCall.Name,
						"input": map[string]any{},
					},
				}); err != nil {
					return err
				}
			}
			if len(event.ToolCall.Arguments) > 0 {
				toolInputs.WriteString(string(event.ToolCall.Arguments))
				if err := writeEvent(w, "content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": idx,
					"delta": map[string]any{
						"type":         "input_json_delta",
						"partial_json": string(event.ToolCall.Arguments),
					},
				}); err != nil {
					return err
				}
			}
			if event.ToolCallDone {
				if err := writeEvent(w, "content_block_stop", map[string]any{
					"type":  "content_block_stop",
					"index": idx,
				}); err != nil {
					return err
				}
				delete(toolBlocks, event.ToolCall.ID)
			}
			flush(flusher)
		case stream.EventTypeUsage:
			usage = event.Usage
		}
	}

	for _, segment := range splitter.Feed("", true) {
		switch segment.Kind {
		case shared.SegmentKindText:
			outputText.WriteString(segment.Text)
			if err := closeThinking(); err != nil {
				return err
			}
			if activeText < 0 {
				blockIndex++
				activeText = blockIndex
				if err := writeEvent(w, "content_block_start", map[string]any{
					"type":  "content_block_start",
					"index": activeText,
					"content_block": map[string]any{
						"type": "text",
						"text": "",
					},
				}); err != nil {
					return err
				}
			}
			if err := writeEvent(w, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": activeText,
				"delta": map[string]any{
					"type": "text_delta",
					"text": segment.Text,
				},
			}); err != nil {
				return err
			}
		case shared.SegmentKindReasoning:
			outputReasoning.WriteString(segment.Text)
			if err := closeText(); err != nil {
				return err
			}
			if activeThinking < 0 {
				blockIndex++
				activeThinking = blockIndex
				if err := writeEvent(w, "content_block_start", map[string]any{
					"type":  "content_block_start",
					"index": activeThinking,
					"content_block": map[string]any{
						"type":     "thinking",
						"thinking": "",
					},
				}); err != nil {
					return err
				}
			}
			if err := writeEvent(w, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": activeThinking,
				"delta": map[string]any{
					"type":     "thinking_delta",
					"thinking": segment.Text,
				},
			}); err != nil {
				return err
			}
		}
		flush(flusher)
	}

	if err := closeText(); err != nil {
		return err
	}
	if err := closeThinking(); err != nil {
		return err
	}
	for _, idx := range toolBlocks {
		if err := writeEvent(w, "content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": idx,
		}); err != nil {
			return err
		}
	}

	stopReason := "end_turn"
	if hasToolUse {
		stopReason = "tool_use"
	}
	outputTokens := preciseOrEstimatedOutputTokens(usage, outputText.String()+outputReasoning.String()+toolInputs.String())
	if err := writeEvent(w, "message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason": stopReason,
		},
		"usage": map[string]any{
			"input_tokens":                req.Metadata.RemainingInputTokens,
			"output_tokens":               outputTokens,
			"cache_creation_input_tokens": req.Metadata.CacheCreationInputTokens,
			"cache_read_input_tokens":     req.Metadata.CacheReadInputTokens,
		},
	}); err != nil {
		return err
	}
	if err := writeEvent(w, "message_stop", map[string]any{"type": "message_stop"}); err != nil {
		return err
	}
	flush(flusher)
	return nil
}

func (f *Formatter) FormatJSON(ctx context.Context, req message.UnifiedRequest, upstream ports.UpstreamStream, w io.Writer) error {
	splitter := &shared.ThinkingSplitter{}
	var text strings.Builder
	var reasoning strings.Builder
	order := make([]string, 0)
	tools := make(map[string]message.ToolCall)
	inputs := make(map[string]*strings.Builder)
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
					text.WriteString(segment.Text)
				}
			}
		case stream.EventTypeReasoning:
			reasoning.WriteString(event.Text)
		case stream.EventTypeToolCall:
			if event.ToolCall == nil {
				continue
			}
			if _, ok := tools[event.ToolCall.ID]; !ok {
				order = append(order, event.ToolCall.ID)
			}
			tools[event.ToolCall.ID] = *event.ToolCall
			builder := inputs[event.ToolCall.ID]
			if builder == nil {
				builder = &strings.Builder{}
				inputs[event.ToolCall.ID] = builder
			}
			builder.WriteString(string(event.ToolCall.Arguments))
		case stream.EventTypeUsage:
			usage = event.Usage
		}
	}

	for _, segment := range splitter.Feed("", true) {
		if segment.Kind == shared.SegmentKindReasoning {
			reasoning.WriteString(segment.Text)
		} else {
			text.WriteString(segment.Text)
		}
	}

	content := make([]map[string]any, 0, len(order)+2)
	if text.Len() > 0 {
		content = append(content, map[string]any{
			"type": "text",
			"text": text.String(),
		})
	}
	if reasoning.Len() > 0 {
		content = append(content, map[string]any{
			"type":     "thinking",
			"thinking": reasoning.String(),
		})
	}
	for _, id := range order {
		call := tools[id]
		raw := ""
		if inputs[id] != nil {
			raw = inputs[id].String()
		}
		var input any = map[string]any{}
		if strings.TrimSpace(raw) != "" {
			if err := json.Unmarshal([]byte(raw), &input); err != nil {
				input = raw
			}
		}
		content = append(content, map[string]any{
			"type":  "tool_use",
			"id":    call.ID,
			"name":  call.Name,
			"input": input,
		})
	}

	stopReason := "end_turn"
	if len(order) > 0 {
		stopReason = "tool_use"
	}
	outputTokens := preciseOrEstimatedOutputTokens(usage, text.String()+reasoning.String()+collectToolInputs(inputs, order))

	return json.NewEncoder(w).Encode(map[string]any{
		"id":          "msg_" + strconv.FormatInt(time.Now().UnixNano(), 10),
		"type":        "message",
		"role":        "assistant",
		"model":       req.Model,
		"content":     content,
		"stop_reason": stopReason,
		"usage": map[string]any{
			"input_tokens":                req.Metadata.RemainingInputTokens,
			"output_tokens":               outputTokens,
			"cache_creation_input_tokens": req.Metadata.CacheCreationInputTokens,
			"cache_read_input_tokens":     req.Metadata.CacheReadInputTokens,
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

func writeEvent(w io.Writer, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
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

func preciseOrEstimatedOutputTokens(usage *stream.Usage, fallbackText string) int {
	if usage != nil && usage.OutputTokens > 0 {
		return usage.OutputTokens
	}
	return estimateOutputTokens(fallbackText)
}

func collectToolInputs(inputs map[string]*strings.Builder, order []string) string {
	var builder strings.Builder
	for _, id := range order {
		if inputs[id] != nil {
			builder.WriteString(inputs[id].String())
		}
	}
	return builder.String()
}
