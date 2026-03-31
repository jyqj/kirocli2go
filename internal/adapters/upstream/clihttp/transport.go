package clihttp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/proxy"

	"kirocli-go/internal/domain/device"
	domainerrors "kirocli-go/internal/domain/errors"
	"kirocli-go/internal/domain/message"
	"kirocli-go/internal/domain/stream"
	"kirocli-go/internal/domain/truncation"
	"kirocli-go/internal/ports"
)

type Config struct {
	BaseURL      string
	ProxyURL     string
	UserAgent    string
	AmzUserAgent string
	Origin       string
	Target       string
	Timeout      time.Duration
}

type Client struct {
	cfg        Config
	httpClient *http.Client
}

type qEvent struct {
	Content        string `json:"content,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Message        string `json:"message,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	ToolUseID      string `json:"toolUseId,omitempty"`
	Name           string `json:"name,omitempty"`
	Input          string `json:"input,omitempty"`
	Stop           bool   `json:"stop,omitempty"`
}

type eventStream struct {
	body io.ReadCloser
}

const (
	maxToolDescLen        = 10237
	longToolDocThreshold  = 10000
	defaultThinkingBudget = 16000
)

func New(cfg Config) *Client {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Minute
	}

	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: NewTransport(cfg),
		},
	}
}

func NewTransport(cfg Config) *http.Transport {
	transport := &http.Transport{
		Proxy:               nil,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   false,
		TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
		DialTLSContext:      makeUTLSDialer(cfg.ProxyURL),
	}

	if cfg.ProxyURL != "" {
		if parsed, err := url.Parse(cfg.ProxyURL); err == nil {
			transport.Proxy = http.ProxyURL(parsed)
		}
	}

	return transport
}

func (c *Client) Send(ctx context.Context, req ports.UpstreamRequest) (ports.UpstreamStream, error) {
	httpReq, err := c.BuildRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, &domainerrors.UpstreamError{
			Category:  domainerrors.CategoryNetwork,
			Message:   "upstream request failed",
			Retryable: true,
			Cause:     err,
		}
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, classifyHTTPError(resp.StatusCode, body)
	}

	return &eventStream{body: resp.Body}, nil
}

func (c *Client) BuildRequest(ctx context.Context, req ports.UpstreamRequest) (*http.Request, error) {
	payload, err := c.buildPayload(req)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/x-amz-json-1.0")
	httpReq.Header.Set("X-Amz-Target", c.cfg.Target)
	httpReq.Header.Set("User-Agent", c.cfg.UserAgent)
	httpReq.Header.Set("X-Amz-User-Agent", c.cfg.AmzUserAgent)
	httpReq.Header.Set("X-Amzn-Codewhisperer-Optout", "false")
	httpReq.Header.Set("Authorization", "Bearer "+req.Lease.Token)
	httpReq.Header.Set("Amz-Sdk-Request", "attempt=1; max=3")
	httpReq.Header.Set("Amz-Sdk-Invocation-Id", newConversationID())
	httpReq.Header.Set("Accept", "*/*")
	httpReq.Header.Set("Accept-Encoding", "gzip")

	return httpReq, nil
}

func (c *Client) buildPayload(req ports.UpstreamRequest) (map[string]any, error) {
	var devID device.Identity
	if req.Lease.HomeDir != "" {
		devID = device.Identity{
			Username: filepath.Base(req.Lease.HomeDir),
			HomeDir:  req.Lease.HomeDir,
		}
	} else {
		devID = device.ForAccount(req.Lease.AccountID)
	}
	workingDirectory := device.EffectiveWorkdir(
		devID,
		req.Request.Metadata.SessionKey,
		req.Request.Metadata.WorkingDirectory,
	)
	envState := map[string]any{
		"operatingSystem":         "macos",
		"currentWorkingDirectory": workingDirectory,
	}

	items := make([]map[string]any, 0, len(req.Request.Messages)+2)
	if system := strings.TrimSpace(req.Request.SystemPrompt); system != "" {
		if req.Request.Protocol == message.ProtocolAnthropic {
			system = formatAnthropicSystemPrompt(system)
		}
		items = append(items,
			userHistoryItem(system, nil, nil, envState, c.cfg.Origin),
			assistantHistoryItem("-", nil),
		)
	}

	for _, msg := range req.Request.Messages {
		items = append(items, c.convertMessage(msg, envState)...)
	}

	items = ensureAlternating(items, envState, c.cfg.Origin)

	currentContent := "-"
	var currentImages []map[string]any
	currentContext := map[string]any{
		"envState": envState,
	}
	longToolDocs := ""
	payloadStrategy := strings.TrimSpace(req.Request.Metadata.PayloadStrategy)

	if len(req.Request.Tools) > 0 {
		var tools []map[string]any
		tools, longToolDocs = c.convertTools(req.Request.Tools)
		currentContext["tools"] = tools
	}

	history := items
	if len(items) > 0 {
		last := items[len(items)-1]
		if userInput, ok := last["userInputMessage"].(map[string]any); ok {
			history = items[:len(items)-1]
			if content, ok := userInput["content"].(string); ok && strings.TrimSpace(content) != "" {
				currentContent = content
			}
			if images, ok := userInput["images"].([]map[string]any); ok && len(images) > 0 {
				currentImages = images
			}
			if ctx, ok := userInput["userInputMessageContext"].(map[string]any); ok {
				if toolResults, ok := ctx["toolResults"]; ok {
					currentContext["toolResults"] = toolResults
				}
			}
		}
	}

	history = applyPayloadStrategy(history, payloadStrategy, strings.TrimSpace(req.Request.SystemPrompt) != "")
	if payloadStrategy == "sticky_compact" {
		currentContext = enrichCompactContext(currentContext, workingDirectory)
	}

	currentContent = applyTranslatorRules(currentContent, longToolDocs, req.Model.ThinkingEnabled)

	currentMessage := map[string]any{
		"content":                 ensureNonEmpty(currentContent),
		"userInputMessageContext": currentContext,
		"origin":                  c.cfg.Origin,
		"modelId":                 req.Model.InternalName,
	}
	if len(currentImages) > 0 {
		currentMessage["images"] = currentImages
	}

	conversationID := strings.TrimSpace(req.Request.Metadata.ConversationID)
	if conversationID == "" {
		conversationID = newConversationID()
	}
	chatTriggerType := strings.TrimSpace(req.Request.Metadata.ChatTriggerType)
	if chatTriggerType == "" {
		chatTriggerType = "MANUAL"
	}

	conversationState := map[string]any{
		"conversationId":  conversationID,
		"chatTriggerType": chatTriggerType,
		"history":         history,
		"currentMessage": map[string]any{
			"userInputMessage": currentMessage,
		},
		"agentContinuationId": newConversationID(),
		"agentTaskType":       "vibe",
	}
	if workspaceID := workspaceIDFrom(workingDirectory); workspaceID != "" {
		conversationState["workspaceId"] = workspaceID
	}

	return map[string]any{
		"conversationState": conversationState,
	}, nil
}

func applyPayloadStrategy(history []map[string]any, strategy string, hasSystem bool) []map[string]any {
	switch strings.TrimSpace(strategy) {
	case "sticky_compact":
		return trimHistoryWithSystem(history, hasSystem, 0)
	default:
		return history
	}
}

func trimHistoryWithSystem(history []map[string]any, hasSystem bool, tailLimit int) []map[string]any {
	if len(history) == 0 {
		return history
	}

	systemPrefix := 0
	if hasSystem && len(history) >= 2 {
		systemPrefix = 2
	}

	prefix := history[:systemPrefix]
	rest := history[systemPrefix:]
	if tailLimit <= 0 {
		result := make([]map[string]any, len(prefix))
		copy(result, prefix)
		return result
	}
	if len(rest) <= tailLimit {
		return history
	}

	start := len(rest) - tailLimit
	result := make([]map[string]any, 0, len(prefix)+tailLimit)
	result = append(result, prefix...)
	result = append(result, rest[start:]...)
	return result
}

func (c *Client) convertMessage(msg message.UnifiedMessage, envState map[string]any) []map[string]any {
	images := convertImages(msg.Parts)
	switch msg.Role {
	case message.RoleSystem, message.RoleDeveloper, message.RoleUser:
		return []map[string]any{
			userHistoryItem(joinTextParts(msg.Parts), images, convertToolResults(msg.ToolResults), envState, c.cfg.Origin),
		}
	case message.RoleAssistant:
		return []map[string]any{
			assistantHistoryItem(joinTextParts(msg.Parts), convertToolCalls(msg.ToolCalls)),
		}
	case message.RoleTool:
		results := msg.ToolResults
		if len(results) == 0 {
			results = []message.ToolResult{{Content: msg.Parts}}
		}
		return []map[string]any{
			userHistoryItem("-", images, convertToolResults(results), envState, c.cfg.Origin),
		}
	default:
		return nil
	}
}

func (c *Client) convertTools(tools []message.UnifiedTool) ([]map[string]any, string) {
	result := make([]map[string]any, 0, len(tools))
	var longToolDocs strings.Builder
	for _, tool := range tools {
		schema := map[string]any{}
		if len(tool.InputSchema) > 0 {
			_ = json.Unmarshal(tool.InputSchema, &schema)
		}
		description := tool.Description
		if len(description) > longToolDocThreshold {
			longToolDocs.WriteString("--- TOOL DOCUMENTATION BEGIN ---\n")
			longToolDocs.WriteString("Tool name: ")
			longToolDocs.WriteString(tool.Name)
			longToolDocs.WriteString("\nFull Description: ")
			longToolDocs.WriteString(description)
			longToolDocs.WriteString("\n--- TOOL DOCUMENTATION END ---\n\n")
			description = "See tool documentation section."
		} else if len(description) > maxToolDescLen {
			description = description[:maxToolDescLen] + "..."
		}

		result = append(result, map[string]any{
			"toolSpecification": map[string]any{
				"name":        shortenToolName(tool.Name),
				"description": description,
				"inputSchema": map[string]any{
					"json": schema,
				},
			},
		})
	}
	return result, longToolDocs.String()
}

func convertToolCalls(calls []message.ToolCall) []map[string]any {
	if len(calls) == 0 {
		return nil
	}

	result := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		var input any = map[string]any{}
		if len(call.Arguments) > 0 && json.Valid(call.Arguments) {
			if err := json.Unmarshal(call.Arguments, &input); err != nil {
				input = string(call.Arguments)
			}
		}
		result = append(result, map[string]any{
			"toolUseId": call.ID,
			"name":      shortenToolName(call.Name),
			"input":     input,
		})
	}
	return result
}

func convertToolResults(results []message.ToolResult) []map[string]any {
	if len(results) == 0 {
		return nil
	}

	output := make([]map[string]any, 0, len(results))
	for _, result := range results {
		item := map[string]any{
			"toolUseId": result.ToolCallID,
			"status":    "success",
		}
		if result.IsError {
			item["status"] = "error"
		}

		content := make([]map[string]any, 0, len(result.Content)+1)
		for _, part := range result.Content {
			if part.Type != message.PartTypeText {
				continue
			}
			content = append(content, map[string]any{
				"text": ensureNonEmpty(part.Text),
			})
		}
		if len(result.Payload) > 0 {
			var payload any
			if json.Valid(result.Payload) {
				if err := json.Unmarshal(result.Payload, &payload); err == nil {
					content = append(content, map[string]any{"json": payload})
				}
			}
		}
		if len(content) == 0 {
			content = append(content, map[string]any{"text": "-"})
		}
		item["content"] = content
		output = append(output, item)
	}

	return output
}

func convertImages(parts []message.Part) []map[string]any {
	if len(parts) == 0 {
		return nil
	}

	images := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		if part.Type != message.PartTypeImage || len(part.Data) == 0 {
			continue
		}
		format := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(part.MimeType)), "image/")
		if format == "" {
			format = "png"
		}
		images = append(images, map[string]any{
			"format": format,
			"source": map[string]any{
				"bytes": string(part.Data),
			},
		})
	}
	return images
}

func userHistoryItem(content string, images []map[string]any, toolResults []map[string]any, envState map[string]any, origin string) map[string]any {
	context := map[string]any{
		"envState": envState,
	}
	if len(toolResults) > 0 {
		context["toolResults"] = toolResults
	}

	userInput := map[string]any{
		"content":                 ensureNonEmpty(content),
		"userInputMessageContext": context,
		"origin":                  origin,
	}
	if len(images) > 0 {
		userInput["images"] = images
	}

	return map[string]any{
		"userInputMessage": userInput,
	}
}

func assistantHistoryItem(content string, toolUses []map[string]any) map[string]any {
	item := map[string]any{
		"assistantResponseMessage": map[string]any{
			"content": ensureNonEmpty(content),
		},
	}
	if len(toolUses) > 0 {
		item["assistantResponseMessage"].(map[string]any)["toolUses"] = toolUses
	}
	return item
}

func ensureAlternating(items []map[string]any, envState map[string]any, origin string) []map[string]any {
	if len(items) < 2 {
		return items
	}

	result := make([]map[string]any, 0, len(items)*2)
	prevRole := roleOf(items[0])
	result = append(result, items[0])
	for i := 1; i < len(items); i++ {
		current := items[i]
		currentRole := roleOf(current)
		if prevRole != "" && currentRole == prevRole {
			if currentRole == "user" {
				result = append(result, assistantHistoryItem("-", nil))
			} else if currentRole == "assistant" {
				result = append(result, userHistoryItem("-", nil, nil, envState, origin))
			}
		}
		result = append(result, current)
		prevRole = currentRole
	}
	return result
}

func makeUTLSDialer(proxyURL string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}

		netConn, err := dialNetwork(ctx, network, addr, proxyURL)
		if err != nil {
			return nil, err
		}

		spec, err := utls.UTLSIdToSpec(utls.HelloChrome_Auto)
		if err != nil {
			netConn.Close()
			return nil, err
		}
		for _, ext := range spec.Extensions {
			alpn, ok := ext.(*utls.ALPNExtension)
			if !ok {
				continue
			}
			alpn.AlpnProtocols = []string{"http/1.1"}
			break
		}

		tlsConn := utls.UClient(netConn, &utls.Config{ServerName: host}, utls.HelloCustom)
		if err := tlsConn.ApplyPreset(&spec); err != nil {
			netConn.Close()
			return nil, err
		}
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			netConn.Close()
			return nil, err
		}
		return tlsConn, nil
	}
}

func dialNetwork(ctx context.Context, network, addr, proxyURL string) (net.Conn, error) {
	if proxyURL == "" {
		dialer := &net.Dialer{Timeout: 30 * time.Second}
		return dialer.DialContext(ctx, network, addr)
	}

	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}
	dialer, err := proxy.FromURL(parsed, proxy.Direct)
	if err != nil {
		return nil, err
	}
	return dialer.Dial(network, addr)
}

func roleOf(item map[string]any) string {
	if _, ok := item["userInputMessage"]; ok {
		return "user"
	}
	if _, ok := item["assistantResponseMessage"]; ok {
		return "assistant"
	}
	return ""
}

func joinTextParts(parts []message.Part) string {
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

func ensureNonEmpty(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func formatAnthropicSystemPrompt(system string) string {
	return "--- SYSTEM PROMPT BEGIN ---\n" + ensureNonEmpty(system) + "\n--- SYSTEM PROMPT END ---\n\n" +
		truncation.GetSystemPromptAddition()
}

func applyTranslatorRules(content, longToolDocs string, thinkingEnabled bool) string {
	base := strings.TrimSpace(content)
	if base == "-" {
		base = ""
	}
	if longToolDocs != "" {
		base = longToolDocs + base
	}
	if thinkingEnabled {
		base += fmt.Sprintf("<antml:thinking_mode>interleaved</antml:thinking_mode><antml:max_thinking_length>%d</antml:max_thinking_length>", defaultThinkingBudget)
	}
	return ensureNonEmpty(base)
}

func shortenToolName(name string) string {
	if len(name) <= 64 {
		return name
	}
	if strings.HasPrefix(name, "mcp__") {
		lastIdx := strings.LastIndex(name, "__")
		if lastIdx > 5 {
			shortened := "mcp__" + name[lastIdx+2:]
			if len(shortened) <= 64 {
				return shortened
			}
		}
	}
	return name[:64]
}

func (s *eventStream) Next(ctx context.Context) (stream.Event, error) {
	_ = ctx

	for {
		eventType, payloadBytes, err := readEventMessage(s.body)
		if err != nil {
			return stream.Event{}, err
		}

		var event map[string]any
		if err := json.Unmarshal(payloadBytes, &event); err != nil {
			return stream.Event{}, &domainerrors.UpstreamError{
				Category: domainerrors.CategoryUnknown,
				Message:  "failed to decode upstream payload",
				Cause:    err,
			}
		}

		switch eventType {
		case "initial-response":
			conversationID, _ := event["conversationId"].(string)
			if conversationID == "" {
				conversationID, _ = event["conversation_id"].(string)
			}
			if conversationID == "" {
				continue
			}
			return stream.Event{
				Type:           stream.EventTypeMetadata,
				ConversationID: conversationID,
			}, nil
		case "assistantResponseEvent":
			text, _ := event["content"].(string)
			if text == "" {
				continue
			}
			return stream.Event{Type: stream.EventTypeText, Text: text}, nil
		case "reasoningContentEvent":
			text, _ := event["text"].(string)
			if text == "" {
				text, _ = event["content"].(string)
			}
			if text == "" {
				continue
			}
			return stream.Event{Type: stream.EventTypeReasoning, Text: text}, nil
		case "toolUseEvent":
			toolUseID, _ := event["toolUseId"].(string)
			name, _ := event["name"].(string)
			input, _ := event["input"].(string)
			stop, _ := event["stop"].(bool)
			return stream.Event{
				Type: stream.EventTypeToolCall,
				ToolCall: &message.ToolCall{
					ID:        toolUseID,
					Name:      name,
					Arguments: json.RawMessage(input),
				},
				ToolCallDone: stop,
			}, nil
		case "messageMetadataEvent", "metadataEvent":
			tokenUsage, _ := event["tokenUsage"].(map[string]any)
			outputTokens, _ := tokenUsage["outputTokens"].(float64)
			uncached, _ := tokenUsage["uncachedInputTokens"].(float64)
			cacheRead, _ := tokenUsage["cacheReadInputTokens"].(float64)
			cacheWrite, _ := tokenUsage["cacheWriteInputTokens"].(float64)
			inputTokens := int(uncached + cacheRead + cacheWrite)
			totalTokens := inputTokens + int(outputTokens)
			return stream.Event{
				Type: stream.EventTypeUsage,
				Usage: &stream.Usage{
					InputTokens:      inputTokens,
					OutputTokens:     int(outputTokens),
					TotalTokens:      totalTokens,
					CacheReadTokens:  int(cacheRead),
					CacheWriteTokens: int(cacheWrite),
				},
			}, nil
		case "contextUsageEvent":
			percentage, _ := event["contextUsagePercentage"].(float64)
			if percentage == 0 {
				percentage, _ = event["context_usage_percentage"].(float64)
			}
			if percentage == 0 {
				continue
			}
			return stream.Event{
				Type:                   stream.EventTypeMetadata,
				ContextUsagePercentage: percentage,
			}, nil
		case "meteringEvent":
			credits, _ := event["usage"].(float64)
			return stream.Event{
				Type: stream.EventTypeUsage,
				Usage: &stream.Usage{
					Credits: credits,
				},
			}, nil
		default:
			reason, _ := event["reason"].(string)
			messageText, _ := event["message"].(string)
			if reason != "" || messageText != "" {
				category, signal := classifyReason(reason, messageText)
				return stream.Event{
					Type: stream.EventTypeError,
					Error: &domainerrors.UpstreamError{
						Category: category,
						Signal:   signal,
						Message:  strings.TrimSpace(reason + ": " + messageText),
					},
				}, nil
			}
		}
	}
}

func newConversationID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		now := time.Now().UnixNano()
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			uint32(now>>32),
			uint16(now>>16),
			uint16(now),
			uint16(now>>8),
			uint64(now)&0xFFFFFFFFFFFF,
		)
	}

	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uint32(b[0])<<24|uint32(b[1])<<16|uint32(b[2])<<8|uint32(b[3]),
		uint16(b[4])<<8|uint16(b[5]),
		uint16(b[6])<<8|uint16(b[7]),
		uint16(b[8])<<8|uint16(b[9]),
		uint64(b[10])<<40|uint64(b[11])<<32|uint64(b[12])<<24|uint64(b[13])<<16|uint64(b[14])<<8|uint64(b[15]),
	)
}

func workspaceIDFrom(workingDirectory string) string {
	workingDirectory = strings.TrimSpace(workingDirectory)
	if workingDirectory == "" || workingDirectory == "." {
		return ""
	}
	cleaned := filepath.Clean(workingDirectory)
	sum := sha1.Sum([]byte(strings.ToLower(cleaned)))
	var b [16]byte
	copy(b[:], sum[:16])
	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uint32(b[0])<<24|uint32(b[1])<<16|uint32(b[2])<<8|uint32(b[3]),
		uint16(b[4])<<8|uint16(b[5]),
		uint16(b[6])<<8|uint16(b[7]),
		uint16(b[8])<<8|uint16(b[9]),
		uint64(b[10])<<40|uint64(b[11])<<32|uint64(b[12])<<24|uint64(b[13])<<16|uint64(b[14])<<8|uint64(b[15]),
	)
}

func enrichCompactContext(context map[string]any, workingDirectory string) map[string]any {
	if context == nil {
		context = make(map[string]any)
	}
	if _, ok := context["gitState"]; !ok {
		context["gitState"] = map[string]any{}
	}
	if _, ok := context["editorStates"]; !ok {
		context["editorStates"] = []map[string]any{}
	}
	if _, ok := context["shellState"]; !ok {
		context["shellState"] = map[string]any{
			"currentWorkingDirectory": workingDirectory,
		}
	}
	if _, ok := context["diagnostic"]; !ok {
		context["diagnostic"] = map[string]any{}
	}
	if _, ok := context["consoleState"]; !ok {
		context["consoleState"] = map[string]any{}
	}
	if _, ok := context["userSettings"]; !ok {
		context["userSettings"] = map[string]any{}
	}
	if _, ok := context["additionalContext"]; !ok {
		context["additionalContext"] = []map[string]any{}
	}
	if _, ok := context["appStudioContext"]; !ok {
		context["appStudioContext"] = map[string]any{
			"cursorState":          map[string]any{},
			"relevantDocuments":    []map[string]any{},
			"useRelevantDocuments": false,
		}
	}
	return context
}

func (s *eventStream) Close() error {
	if s.body == nil {
		return nil
	}
	return s.body.Close()
}

func readEventMessage(body io.Reader) (string, []byte, error) {
	prelude := make([]byte, 12)
	_, err := io.ReadFull(body, prelude)
	if err == io.EOF {
		return "", nil, io.EOF
	}
	if err != nil {
		return "", nil, err
	}

	totalLength := int(prelude[0])<<24 | int(prelude[1])<<16 | int(prelude[2])<<8 | int(prelude[3])
	headersLength := int(prelude[4])<<24 | int(prelude[5])<<16 | int(prelude[6])<<8 | int(prelude[7])
	if totalLength < 16 {
		return "", nil, fmt.Errorf("invalid event frame length")
	}

	remaining := totalLength - 12
	msgBuf := make([]byte, remaining)
	if _, err := io.ReadFull(body, msgBuf); err != nil {
		return "", nil, err
	}
	if headersLength > len(msgBuf)-4 {
		return "", nil, fmt.Errorf("invalid headers length")
	}

	eventType := extractEventType(msgBuf[:headersLength])
	payloadBytes := msgBuf[headersLength : len(msgBuf)-4]
	return eventType, payloadBytes, nil
}

func extractEventType(headers []byte) string {
	offset := 0
	for offset < len(headers) {
		nameLen := int(headers[offset])
		offset++
		if offset+nameLen > len(headers) {
			break
		}
		name := string(headers[offset : offset+nameLen])
		offset += nameLen
		if offset >= len(headers) {
			break
		}
		valueType := headers[offset]
		offset++

		if valueType == 7 {
			if offset+2 > len(headers) {
				break
			}
			valueLen := int(headers[offset])<<8 | int(headers[offset+1])
			offset += 2
			if offset+valueLen > len(headers) {
				break
			}
			value := string(headers[offset : offset+valueLen])
			offset += valueLen
			if name == ":event-type" {
				return value
			}
			continue
		}

		skipSizes := map[byte]int{0: 0, 1: 0, 2: 1, 3: 2, 4: 4, 5: 8, 8: 8, 9: 16}
		if valueType == 6 {
			if offset+2 > len(headers) {
				break
			}
			l := int(headers[offset])<<8 | int(headers[offset+1])
			offset += 2 + l
		} else if skip, ok := skipSizes[valueType]; ok {
			offset += skip
		} else {
			break
		}
	}
	return ""
}

func classifyHTTPError(statusCode int, body []byte) error {
	message := strings.TrimSpace(string(body))
	category := domainerrors.CategoryUnknown
	retryable := false

	switch statusCode {
	case http.StatusBadRequest:
		category = domainerrors.CategoryValidation
	case http.StatusUnauthorized, http.StatusForbidden:
		category = domainerrors.CategoryAuth
	case http.StatusTooManyRequests:
		category = domainerrors.CategoryQuota
		retryable = true
	default:
		if statusCode >= 500 {
			category = domainerrors.CategoryNetwork
			retryable = true
		}
	}

	if detected, signal, ok := domainerrors.DetectSignal(message); ok {
		category = detected
		if detected == domainerrors.CategoryQuota {
			retryable = true
		}
		return &domainerrors.UpstreamError{
			Category:   category,
			Signal:     signal,
			Message:    message,
			StatusCode: statusCode,
			Retryable:  retryable,
		}
	}

	return &domainerrors.UpstreamError{
		Category:   category,
		Message:    message,
		StatusCode: statusCode,
		Retryable:  retryable,
	}
}

func classifyReasonCategory(reason, message string) domainerrors.Category {
	category, _ := classifyReason(reason, message)
	return category
}

func classifyReason(reason, message string) (domainerrors.Category, string) {
	if detected, signal, ok := domainerrors.DetectSignal(reason + " " + message); ok {
		return detected, signal
	}
	return domainerrors.CategoryUnknown, ""
}
