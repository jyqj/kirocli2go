package anthropic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	httpshared "kirocli-go/internal/adapters/http/shared"
	"kirocli-go/internal/adapters/mcp/websearch"
	"kirocli-go/internal/application/apikey"
	"kirocli-go/internal/application/chat"
	domainerrors "kirocli-go/internal/domain/errors"
	"kirocli-go/internal/domain/message"
	"kirocli-go/internal/ports"
)

type Handler struct {
	chat      *chat.Service
	webSearch interface {
		HandleAnthropic(ctx context.Context, req websearch.SearchRequest, w http.ResponseWriter) error
	}
}

func NewHandler(chatService *chat.Service, webSearch interface {
	HandleAnthropic(ctx context.Context, req websearch.SearchRequest, w http.ResponseWriter) error
}) *Handler {
	return &Handler{
		chat:      chatService,
		webSearch: webSearch,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	var req MessagesRequest
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if h.webSearch != nil && hasWebSearchTool(req.Tools) {
		searchReq := websearch.SearchRequest{
			RequestID: requestIDFrom(r),
			Model:     req.Model,
			Query:     extractSearchQuery(req),
			MaxUses:   maxUsesForWebSearch(req.Tools),
			InputTokens: chat.EstimateAnthropicInputTokens(message.UnifiedRequest{
				Model:        req.Model,
				SystemPrompt: mustParseSystem(req.System),
				Messages:     mustParseMessages(req.Messages),
				Tools:        mustParseTools(req.Tools),
			}),
			Stream: req.Stream,
		}
		if err := h.webSearch.HandleAnthropic(r.Context(), searchReq, w); err != nil {
			writeError(w, err)
		}
		return
	}

	unified, err := toUnifiedRequest(req, r)
	if err != nil {
		writeError(w, err)
		return
	}
	unified.Metadata.FakeCacheKey = chat.ComputeScopedCacheKey(unified.Metadata.FakeCacheNamespace, body)
	unified.Metadata.EstimatedInputTokens = chat.EstimateAnthropicInputTokens(unified)

	if err := h.chat.Handle(r.Context(), unified, ports.ResponseFormatAnthropic, w); err != nil {
		writeError(w, err)
	}
}

type CountTokensHandler struct{}

func NewCountTokensHandler() *CountTokensHandler {
	return &CountTokensHandler{}
}

func (h *CountTokensHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var raw map[string]any
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&raw); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	var req MessagesRequest
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	unified, err := toUnifiedRequest(req, r)
	if err != nil {
		writeError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"input_tokens": chat.EstimateAnthropicInputTokens(unified),
	})
}

func writeError(w http.ResponseWriter, err error) {
	statusCode := http.StatusBadGateway
	if upstreamErr, ok := err.(*domainerrors.UpstreamError); ok {
		switch upstreamErr.Category {
		case domainerrors.CategoryValidation:
			statusCode = http.StatusBadRequest
		case domainerrors.CategoryNotImplemented:
			statusCode = http.StatusNotImplemented
		case domainerrors.CategoryAuth:
			statusCode = http.StatusUnauthorized
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    "api_error",
			"message": err.Error(),
		},
	})
}

func toUnifiedRequest(req MessagesRequest, r *http.Request) (message.UnifiedRequest, error) {
	systemPrompt, err := parseSystem(req.System)
	if err != nil {
		return message.UnifiedRequest{}, err
	}

	messagesOut := make([]message.UnifiedMessage, 0, len(req.Messages))
	for _, item := range req.Messages {
		role := toRole(item.Role)
		out, err := parseAnthropicMessage(role, item.Content)
		if err != nil {
			return message.UnifiedRequest{}, err
		}
		messagesOut = append(messagesOut, out)
	}

	tools := make([]message.UnifiedTool, 0, len(req.Tools))
	for _, tool := range req.Tools {
		tools = append(tools, message.UnifiedTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		})
	}

	sessionKey := httpshared.SessionKeyFrom(r)
	principal, _ := apikey.PrincipalFromContext(r.Context())

	return message.UnifiedRequest{
		Protocol:     message.ProtocolAnthropic,
		Model:        req.Model,
		SystemPrompt: systemPrompt,
		Stream:       req.Stream,
		Messages:     messagesOut,
		Tools:        tools,
		Metadata: message.RequestMetadata{
			ClientRequestID:    requestIDFrom(r),
			Endpoint:           r.URL.Path,
			APIKeyID:           principal.ID,
			SessionKey:         sessionKey,
			WorkingDirectory:   httpshared.WorkingDirectoryFrom(r),
			CompactRequested:   httpshared.CompactRequestedFrom(r),
			StickyEnabled:      strings.TrimSpace(sessionKey) != "",
			ChatTriggerType:    "MANUAL",
			FakeCacheNamespace: principal.CacheNamespace,
		},
	}, nil
}

func hasWebSearchTool(tools []ToolDefinition) bool {
	for _, tool := range tools {
		if strings.TrimSpace(tool.Name) == "web_search" {
			return true
		}
	}
	return false
}

func maxUsesForWebSearch(tools []ToolDefinition) int {
	for _, tool := range tools {
		if strings.TrimSpace(tool.Name) == "web_search" && tool.MaxUses > 0 {
			return tool.MaxUses
		}
	}
	return 5
}

func extractSearchQuery(req MessagesRequest) string {
	if len(req.Messages) == 0 {
		return ""
	}

	for _, item := range req.Messages {
		if strings.TrimSpace(item.Role) != "user" {
			continue
		}

		if text := firstTextBlock(item.Content); text != "" {
			return trimSearchPrefix(text)
		}
	}

	return ""
}

func firstTextBlock(raw RawContent) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}

	var blocks []ContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		for _, block := range blocks {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				return strings.TrimSpace(block.Text)
			}
		}
	}

	return ""
}

func trimSearchPrefix(text string) string {
	prefix := "Perform a web search for the query: "
	if strings.HasPrefix(text, prefix) {
		return strings.TrimSpace(text[len(prefix):])
	}
	return strings.TrimSpace(text)
}

func mustParseSystem(raw RawSystem) string {
	value, _ := parseSystem(raw)
	return value
}

func mustParseMessages(items []Message) []message.UnifiedMessage {
	result := make([]message.UnifiedMessage, 0, len(items))
	for _, item := range items {
		parsed, err := parseAnthropicMessage(toRole(item.Role), item.Content)
		if err != nil {
			continue
		}
		result = append(result, parsed)
	}
	return result
}

func mustParseTools(items []ToolDefinition) []message.UnifiedTool {
	result := make([]message.UnifiedTool, 0, len(items))
	for _, item := range items {
		result = append(result, message.UnifiedTool{
			Name:        item.Name,
			Description: item.Description,
			InputSchema: item.InputSchema,
		})
	}
	return result
}

func parseSystem(raw RawSystem) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString, nil
	}

	var blocks []ContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		lines := make([]string, 0, len(blocks))
		for _, block := range blocks {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				lines = append(lines, block.Text)
			}
		}
		return strings.Join(lines, "\n"), nil
	}

	return "", domainerrors.New(domainerrors.CategoryValidation, "unsupported anthropic system format")
}

func parseAnthropicMessage(role message.Role, raw RawContent) (message.UnifiedMessage, error) {
	out := message.UnifiedMessage{Role: role}
	if len(raw) == 0 || string(raw) == "null" {
		return out, nil
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		out.Parts = []message.Part{{Type: message.PartTypeText, Text: asString}}
		return out, nil
	}

	var blocks []ContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		for _, block := range blocks {
			switch block.Type {
			case "text":
				out.Parts = append(out.Parts, message.Part{Type: message.PartTypeText, Text: block.Text})
			case "image":
				if block.Source == nil || strings.TrimSpace(block.Source.Data) == "" {
					continue
				}
				if _, err := base64.StdEncoding.DecodeString(block.Source.Data); err != nil {
					return message.UnifiedMessage{}, domainerrors.New(domainerrors.CategoryValidation, "invalid anthropic image data")
				}
				mediaType := strings.TrimSpace(block.Source.MediaType)
				if mediaType == "" {
					mediaType = "image/jpeg"
				}
				out.Parts = append(out.Parts, message.Part{
					Type:     message.PartTypeImage,
					MimeType: mediaType,
					Data:     []byte(block.Source.Data),
				})
			case "tool_use":
				out.ToolCalls = append(out.ToolCalls, message.ToolCall{
					ID:        block.ID,
					Name:      block.Name,
					Arguments: block.Input,
				})
			case "tool_result":
				resultParts, payload := parseToolResultContent(block.Content)
				out.ToolResults = append(out.ToolResults, message.ToolResult{
					ToolCallID: block.ToolUseID,
					Content:    resultParts,
					Payload:    payload,
				})
			}
		}
		return out, nil
	}

	return message.UnifiedMessage{}, domainerrors.New(domainerrors.CategoryValidation, "unsupported anthropic content format")
}

func parseToolResultContent(raw json.RawMessage) ([]message.Part, json.RawMessage) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return []message.Part{{Type: message.PartTypeText, Text: asString}}, nil
	}

	var blocks []ContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		parts := make([]message.Part, 0, len(blocks))
		for _, block := range blocks {
			if block.Type == "text" {
				parts = append(parts, message.Part{Type: message.PartTypeText, Text: block.Text})
			}
		}
		return parts, nil
	}

	return nil, raw
}

func toRole(role string) message.Role {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "assistant":
		return message.RoleAssistant
	default:
		return message.RoleUser
	}
}

func requestIDFrom(r *http.Request) string {
	if requestID := strings.TrimSpace(r.Header.Get("X-Request-Id")); requestID != "" {
		return requestID
	}
	return "req-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}
