package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"kirocli-go/internal/adapters/upstream/clihttp"
	"kirocli-go/internal/domain/account"
	domainerrors "kirocli-go/internal/domain/errors"
	"kirocli-go/internal/ports"
	"kirocli-go/internal/tokenutil"
)

type Config struct {
	URL      string
	ProxyURL string
	Timeout  time.Duration
}

type SearchRequest struct {
	RequestID   string
	Model       string
	Query       string
	MaxUses     int
	InputTokens int
	Stream      bool
}

type Client struct {
	cfg        Config
	tokens     ports.TokenProvider
	httpClient *http.Client
}

type rpcRequest struct {
	ID      string    `json:"id"`
	JSONRPC string    `json:"jsonrpc"`
	Method  string    `json:"method"`
	Params  rpcParams `json:"params"`
}

type rpcParams struct {
	Name      string       `json:"name"`
	Arguments rpcArguments `json:"arguments"`
}

type rpcArguments struct {
	Query string `json:"query"`
}

type rpcResponse struct {
	Result *rpcResult `json:"result,omitempty"`
	Error  *rpcError  `json:"error,omitempty"`
}

type rpcResult struct {
	Content []rpcContent `json:"content"`
}

type rpcContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type rpcError struct {
	Message string `json:"message,omitempty"`
}

type searchResults struct {
	Results []searchResult `json:"results"`
}

type searchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

func New(cfg Config, tokens ports.TokenProvider) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	return &Client{
		cfg:    cfg,
		tokens: tokens,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: clihttp.NewTransport(clihttp.Config{ProxyURL: cfg.ProxyURL}),
		},
	}
}

func (c *Client) HandleAnthropic(ctx context.Context, req SearchRequest, w http.ResponseWriter) error {
	if strings.TrimSpace(req.Query) == "" {
		return domainerrors.New(domainerrors.CategoryValidation, "web_search query is required")
	}
	if c == nil || c.tokens == nil {
		return domainerrors.New(domainerrors.CategoryNotImplemented, "web_search is not configured")
	}

	lease, err := c.tokens.Acquire(ctx, account.AcquireHint{
		Profile:  account.ProfileCLI,
		Model:    "web_search",
		Protocol: "anthropic",
		Stream:   req.Stream,
	})
	if err != nil {
		return err
	}

	results, err := c.call(ctx, lease, req)
	if err != nil {
		failure := account.FailureMeta{
			RequestID: req.RequestID,
			Model:     req.Model,
			Reason:    mapCategoryToFailureReason(err),
			Message:   err.Error(),
		}
		if upstreamErr, ok := err.(*domainerrors.UpstreamError); ok {
			failure.StatusCode = upstreamErr.StatusCode
		}
		_ = c.tokens.ReportFailure(ctx, lease, failure)
		return err
	}

	_ = c.tokens.ReportSuccess(ctx, lease, account.SuccessMeta{
		RequestID: req.RequestID,
		Model:     req.Model,
	})

	if req.Stream {
		return c.writeStream(req, results, w)
	}
	return c.writeJSON(req, results, w)
}

func (c *Client) call(ctx context.Context, lease account.Lease, req SearchRequest) (*searchResults, error) {
	payload, err := json.Marshal(rpcRequest{
		ID:      fmt.Sprintf("web_search_tooluse_%d", time.Now().UnixNano()),
		JSONRPC: "2.0",
		Method:  "tools/call",
		Params: rpcParams{
			Name:      "web_search",
			Arguments: rpcArguments{Query: req.Query},
		},
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+lease.Token)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, &domainerrors.UpstreamError{
			Category:  domainerrors.CategoryNetwork,
			Message:   "mcp request failed",
			Retryable: true,
			Cause:     err,
		}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &domainerrors.UpstreamError{
			Category: domainerrors.CategoryUnknown,
			Message:  "failed to read mcp response",
			Cause:    err,
		}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, classifyMCPHTTPError(resp.StatusCode, body)
	}

	var rpc rpcResponse
	if err := json.Unmarshal(body, &rpc); err != nil {
		return nil, &domainerrors.UpstreamError{
			Category: domainerrors.CategoryUnknown,
			Message:  "failed to decode mcp response",
			Cause:    err,
		}
	}
	if rpc.Error != nil {
		return nil, &domainerrors.UpstreamError{
			Category: domainerrors.CategoryUnknown,
			Message:  strings.TrimSpace(rpc.Error.Message),
		}
	}

	results := &searchResults{}
	if rpc.Result != nil {
		for _, content := range rpc.Result.Content {
			if content.Type != "text" || strings.TrimSpace(content.Text) == "" {
				continue
			}
			if err := json.Unmarshal([]byte(content.Text), results); err == nil {
				break
			}
		}
	}
	return results, nil
}

func (c *Client) writeStream(req SearchRequest, results *searchResults, w http.ResponseWriter) error {
	headers := w.Header()
	headers.Set("Content-Type", "text/event-stream")
	headers.Set("Cache-Control", "no-cache")
	headers.Set("Connection", "keep-alive")

	flusher, _ := w.(http.Flusher)
	write := func(event string, payload any) error {
		body, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, body); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}

	toolUseID := fmt.Sprintf("srvtoolu_%d", time.Now().UnixNano())
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	searchContent := buildSearchContent(req.MaxUses, results)
	summary := buildSummary(req.Query, req.MaxUses, results)

	if err := write("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            msgID,
			"type":          "message",
			"role":          "assistant",
			"model":         req.Model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]int{
				"input_tokens":  req.InputTokens,
				"output_tokens": 0,
			},
		},
	}); err != nil {
		return err
	}
	if err := write("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": 0,
		"content_block": map[string]any{
			"id":    toolUseID,
			"type":  "server_tool_use",
			"name":  "web_search",
			"input": map[string]any{},
		},
	}); err != nil {
		return err
	}
	inputJSON, _ := json.Marshal(map[string]string{"query": req.Query})
	if err := write("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{
			"type":         "input_json_delta",
			"partial_json": string(inputJSON),
		},
	}); err != nil {
		return err
	}
	if err := write("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}); err != nil {
		return err
	}
	if err := write("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": 1,
		"content_block": map[string]any{
			"type":        "web_search_tool_result",
			"tool_use_id": toolUseID,
			"content":     searchContent,
		},
	}); err != nil {
		return err
	}
	if err := write("content_block_stop", map[string]any{"type": "content_block_stop", "index": 1}); err != nil {
		return err
	}
	if err := write("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": 2,
		"content_block": map[string]any{
			"type": "text",
			"text": "",
		},
	}); err != nil {
		return err
	}
	if err := write("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": 2,
		"delta": map[string]any{
			"type": "text_delta",
			"text": summary,
		},
	}); err != nil {
		return err
	}
	if err := write("content_block_stop", map[string]any{"type": "content_block_stop", "index": 2}); err != nil {
		return err
	}
	if err := write("message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
		},
		"usage": map[string]int{
			"output_tokens": tokenutil.CountText(summary),
		},
	}); err != nil {
		return err
	}
	return write("message_stop", map[string]any{"type": "message_stop"})
}

func (c *Client) writeJSON(req SearchRequest, results *searchResults, w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")

	toolUseID := fmt.Sprintf("srvtoolu_%d", time.Now().UnixNano())
	summary := buildSummary(req.Query, req.MaxUses, results)
	content := []map[string]any{
		{
			"id":    toolUseID,
			"type":  "server_tool_use",
			"name":  "web_search",
			"input": map[string]any{"query": req.Query},
		},
		{
			"type":        "web_search_tool_result",
			"tool_use_id": toolUseID,
			"content":     buildSearchContent(req.MaxUses, results),
		},
		{
			"type": "text",
			"text": summary,
		},
	}

	return json.NewEncoder(w).Encode(map[string]any{
		"id":          fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		"type":        "message",
		"role":        "assistant",
		"model":       req.Model,
		"content":     content,
		"stop_reason": "end_turn",
		"usage": map[string]int{
			"input_tokens":  req.InputTokens,
			"output_tokens": tokenutil.CountText(summary),
		},
	})
}

func buildSearchContent(maxUses int, results *searchResults) []map[string]any {
	if results == nil || len(results.Results) == 0 {
		return nil
	}

	limit := len(results.Results)
	if maxUses > 0 && maxUses < limit {
		limit = maxUses
	}

	content := make([]map[string]any, 0, limit)
	for i := 0; i < limit; i++ {
		item := results.Results[i]
		content = append(content, map[string]any{
			"type":              "web_search_result",
			"title":             item.Title,
			"url":               item.URL,
			"encrypted_content": item.Snippet,
			"page_age":          nil,
		})
	}
	return content
}

func buildSummary(query string, maxUses int, results *searchResults) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("Here are the search results for %q:\n\n", query))

	if results == nil || len(results.Results) == 0 {
		builder.WriteString("No results found.\n")
		return builder.String()
	}

	limit := len(results.Results)
	if maxUses > 0 && maxUses < limit {
		limit = maxUses
	}

	for i := 0; i < limit; i++ {
		item := results.Results[i]
		builder.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, item.Title))
		if snippet := strings.TrimSpace(item.Snippet); snippet != "" {
			if len(snippet) > 200 {
				snippet = snippet[:200] + "..."
			}
			builder.WriteString(fmt.Sprintf("   %s\n", snippet))
		}
		builder.WriteString(fmt.Sprintf("   Source: %s\n\n", item.URL))
	}

	return builder.String()
}

func classifyMCPHTTPError(statusCode int, body []byte) error {
	message := strings.TrimSpace(string(body))
	category := domainerrors.CategoryUnknown
	retryable := false

	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		category = domainerrors.CategoryAuth
	case http.StatusTooManyRequests:
		category = domainerrors.CategoryQuota
		retryable = true
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		category = domainerrors.CategoryNetwork
		retryable = true
	}

	if detected, signal, ok := domainerrors.DetectSignal(message); ok {
		category = detected
		if detected == domainerrors.CategoryQuota {
			retryable = true
		}
		return &domainerrors.UpstreamError{
			Category:   category,
			Signal:     signal,
			StatusCode: statusCode,
			Message:    message,
			Retryable:  retryable,
		}
	}

	return &domainerrors.UpstreamError{
		Category:   category,
		StatusCode: statusCode,
		Message:    message,
		Retryable:  retryable,
	}
}

func mapCategoryToFailureReason(err error) account.FailureReason {
	upstreamErr, ok := err.(*domainerrors.UpstreamError)
	if !ok {
		return account.FailureReasonUnknown
	}

	switch upstreamErr.Category {
	case domainerrors.CategoryAuth:
		return account.FailureReasonAuth
	case domainerrors.CategoryQuota:
		return account.FailureReasonQuota
	case domainerrors.CategoryBan:
		return account.FailureReasonBan
	case domainerrors.CategoryNetwork:
		return account.FailureReasonNetwork
	default:
		return account.FailureReasonUnknown
	}
}
