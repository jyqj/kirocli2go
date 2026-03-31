package openai

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	httpshared "kirocli-go/internal/adapters/http/shared"
	"kirocli-go/internal/application/apikey"
	"kirocli-go/internal/application/chat"
	domainerrors "kirocli-go/internal/domain/errors"
	"kirocli-go/internal/domain/message"
	"kirocli-go/internal/ports"
)

var openAIImageDataURL = regexp.MustCompile(`^data:(image/[^;]+);base64,(.+)$`)

type Handler struct {
	chat *chat.Service
}

func NewHandler(chatService *chat.Service) *Handler {
	return &Handler{chat: chatService}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	unified, err := toUnifiedRequest(req, r)
	if err != nil {
		writeError(w, err)
		return
	}

	if err := h.chat.Handle(r.Context(), unified, ports.ResponseFormatOpenAI, w); err != nil {
		writeError(w, err)
	}
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
		"error": map[string]any{
			"message": err.Error(),
			"type":    "server_error",
		},
	})
}

func toUnifiedRequest(req ChatCompletionRequest, r *http.Request) (message.UnifiedRequest, error) {
	systemPrompts := make([]string, 0, 2)
	messagesOut := make([]message.UnifiedMessage, 0, len(req.Messages))
	tools := make([]message.UnifiedTool, 0, len(req.Tools))
	sessionKey := httpshared.SessionKeyFrom(r)
	principal, _ := apikey.PrincipalFromContext(r.Context())

	for _, tool := range req.Tools {
		if tool.Type != "" && tool.Type != "function" {
			continue
		}
		tools = append(tools, message.UnifiedTool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: tool.Function.Parameters,
		})
	}

	for _, item := range req.Messages {
		role := toRole(item.Role)
		parts, err := parseContent(item.Content)
		if err != nil {
			return message.UnifiedRequest{}, err
		}

		if role == message.RoleSystem || role == message.RoleDeveloper {
			text := strings.TrimSpace(joinText(parts))
			if text != "" {
				systemPrompts = append(systemPrompts, text)
			}
			continue
		}

		out := message.UnifiedMessage{
			Role:  role,
			Parts: parts,
		}

		if role == message.RoleAssistant {
			for _, call := range item.ToolCalls {
				out.ToolCalls = append(out.ToolCalls, message.ToolCall{
					ID:        call.ID,
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
				})
			}
		}

		if role == message.RoleTool {
			out.ToolResults = []message.ToolResult{{
				ToolCallID: item.ToolCallID,
				Content:    parts,
			}}
			out.Parts = nil
		}

		messagesOut = append(messagesOut, out)
	}

	return message.UnifiedRequest{
		Protocol:     message.ProtocolOpenAI,
		Model:        req.Model,
		SystemPrompt: strings.Join(systemPrompts, "\n\n"),
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

func parseContent(raw RawContent) ([]message.Part, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return []message.Part{{Type: message.PartTypeText, Text: asString}}, nil
	}

	var parts []ContentPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		result := make([]message.Part, 0, len(parts))
		for _, part := range parts {
			switch part.Type {
			case "text":
				result = append(result, message.Part{Type: message.PartTypeText, Text: part.Text})
			case "image_url":
				if part.ImageURL == nil {
					continue
				}
				matches := openAIImageDataURL.FindStringSubmatch(strings.TrimSpace(part.ImageURL.URL))
				if matches == nil {
					return nil, domainerrors.New(domainerrors.CategoryValidation, "only base64 data URLs are supported for image_url")
				}
				if _, err := base64.StdEncoding.DecodeString(matches[2]); err != nil {
					return nil, domainerrors.New(domainerrors.CategoryValidation, "invalid base64 image data")
				}
				result = append(result, message.Part{
					Type:     message.PartTypeImage,
					MimeType: matches[1],
					Data:     []byte(matches[2]),
				})
			}
		}
		return result, nil
	}

	return nil, domainerrors.New(domainerrors.CategoryValidation, "unsupported openai content format")
}

func toRole(role string) message.Role {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "system":
		return message.RoleSystem
	case "developer":
		return message.RoleDeveloper
	case "assistant":
		return message.RoleAssistant
	case "tool":
		return message.RoleTool
	default:
		return message.RoleUser
	}
}

func joinText(parts []message.Part) string {
	var builder strings.Builder
	for _, part := range parts {
		if part.Type != message.PartTypeText || part.Text == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(part.Text)
	}
	return builder.String()
}

func requestIDFrom(r *http.Request) string {
	if requestID := strings.TrimSpace(r.Header.Get("X-Request-Id")); requestID != "" {
		return requestID
	}
	return "req-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}
