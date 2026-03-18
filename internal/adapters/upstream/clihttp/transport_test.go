package clihttp

import (
	"strings"
	"testing"

	domainerrors "kirocli-go/internal/domain/errors"
	"kirocli-go/internal/domain/message"
	"kirocli-go/internal/domain/model"
	"kirocli-go/internal/ports"
)

func TestClassifyHTTPErrorDetectsQuotaSignal(t *testing.T) {
	err := classifyHTTPError(400, []byte(`{"reason":"MONTHLY_REQUEST_COUNT"}`))
	upstreamErr, ok := err.(*domainerrors.UpstreamError)
	if !ok {
		t.Fatalf("expected UpstreamError, got %T", err)
	}
	if upstreamErr.Category != domainerrors.CategoryQuota {
		t.Fatalf("expected quota category, got %s", upstreamErr.Category)
	}
	if !upstreamErr.Retryable {
		t.Fatalf("expected quota classification to be retryable")
	}
}

func TestClassifyReasonCategoryDetectsBanSignal(t *testing.T) {
	category := classifyReasonCategory("TEMPORARILY_SUSPENDED", "account paused")
	if category != domainerrors.CategoryBan {
		t.Fatalf("expected ban category, got %s", category)
	}
}

func TestBuildPayloadAppliesThinkingAndTranslatorRules(t *testing.T) {
	client := New(Config{Origin: "KIRO_CLI"})
	longDesc := strings.Repeat("x", 10050)
	longName := "mcp__very_long_server_name_that_should_be_shortened__extremely_verbose_tool_name_that_is_way_too_long"

	payload, err := client.buildPayload(ports.UpstreamRequest{
		Model: model.ResolvedModel{
			InternalName:    "claude-sonnet-4.5",
			ThinkingEnabled: true,
		},
		Request: message.UnifiedRequest{
			Protocol:     message.ProtocolAnthropic,
			SystemPrompt: "system message",
			Tools: []message.UnifiedTool{{
				Name:        longName,
				Description: longDesc,
			}},
			Messages: []message.UnifiedMessage{{
				Role:  message.RoleUser,
				Parts: []message.Part{{Type: message.PartTypeText, Text: "hello"}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("buildPayload error: %v", err)
	}

	state := payload["conversationState"].(map[string]any)
	history := state["history"].([]map[string]any)
	systemItem := history[0]["userInputMessage"].(map[string]any)
	if !strings.Contains(systemItem["content"].(string), "--- SYSTEM PROMPT BEGIN ---") {
		t.Fatalf("expected anthropic system prompt wrapper, got %q", systemItem["content"])
	}

	current := state["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
	content := current["content"].(string)
	if !strings.Contains(content, "--- TOOL DOCUMENTATION BEGIN ---") {
		t.Fatalf("expected long tool docs to be injected")
	}
	if !strings.Contains(content, "<antml:thinking_mode>interleaved</antml:thinking_mode>") {
		t.Fatalf("expected thinking directive to be injected")
	}

	tools := current["userInputMessageContext"].(map[string]any)["tools"].([]map[string]any)
	spec := tools[0]["toolSpecification"].(map[string]any)
	name := spec["name"].(string)
	if len(name) > 64 {
		t.Fatalf("expected tool name to be shortened, got len=%d", len(name))
	}
	if spec["description"].(string) != "See tool documentation section." {
		t.Fatalf("expected long tool description placeholder, got %q", spec["description"])
	}
}
