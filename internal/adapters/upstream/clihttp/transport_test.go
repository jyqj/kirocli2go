package clihttp

import (
	"fmt"
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
			Metadata: message.RequestMetadata{
				ConversationID:   "conv-fixed",
				WorkingDirectory: "/tmp/project",
			},
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
	if state["conversationId"].(string) != "conv-fixed" {
		t.Fatalf("expected fixed conversation id, got %v", state["conversationId"])
	}
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
	envState := current["userInputMessageContext"].(map[string]any)["envState"].(map[string]any)
	if envState["currentWorkingDirectory"].(string) != "/tmp/project" {
		t.Fatalf("expected working directory /tmp/project, got %v", envState["currentWorkingDirectory"])
	}
	if _, ok := envState["environmentVariables"].([]map[string]string); !ok {
		t.Fatalf("expected environmentVariables to be present, got %T", envState["environmentVariables"])
	}
	if payload["conversationState"].(map[string]any)["workspaceId"] == "" {
		t.Fatal("expected workspaceId to be populated")
	}
	spec := tools[0]["toolSpecification"].(map[string]any)
	name := spec["name"].(string)
	if len(name) > 64 {
		t.Fatalf("expected tool name to be shortened, got len=%d", len(name))
	}
	if spec["description"].(string) != "See tool documentation section." {
		t.Fatalf("expected long tool description placeholder, got %q", spec["description"])
	}
}

func TestBuildPayloadUsesStickyFullStrategy(t *testing.T) {
	client := New(Config{Origin: "KIRO_CLI"})
	messages := make([]message.UnifiedMessage, 0, 11)
	for i := 0; i < 11; i++ {
		role := message.RoleUser
		if i%2 == 1 {
			role = message.RoleAssistant
		}
		messages = append(messages, message.UnifiedMessage{
			Role:  role,
			Parts: []message.Part{{Type: message.PartTypeText, Text: fmt.Sprintf("m-%d", i)}},
		})
	}

	payload, err := client.buildPayload(ports.UpstreamRequest{
		Model: model.ResolvedModel{InternalName: "claude-sonnet-4.5"},
		Request: message.UnifiedRequest{
			Protocol:     message.ProtocolAnthropic,
			SystemPrompt: "system message",
			Messages:     messages,
			Metadata: message.RequestMetadata{
				ConversationID:  "conv-sticky",
				PayloadStrategy: "sticky_full",
			},
		},
	})
	if err != nil {
		t.Fatalf("buildPayload error: %v", err)
	}

	history := payload["conversationState"].(map[string]any)["history"].([]map[string]any)
	if len(history) != 12 {
		t.Fatalf("expected full history len 12 (2 system + 10 history), got %d", len(history))
	}
	lastUser := payload["conversationState"].(map[string]any)["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)["content"].(string)
	if lastUser != "m-10" {
		t.Fatalf("expected current message m-10, got %q", lastUser)
	}
}

func TestBuildPayloadUsesStickyCompactStrategy(t *testing.T) {
	client := New(Config{Origin: "KIRO_CLI"})
	payload, err := client.buildPayload(ports.UpstreamRequest{
		Model: model.ResolvedModel{InternalName: "claude-sonnet-4.5"},
		Request: message.UnifiedRequest{
			Protocol:     message.ProtocolAnthropic,
			SystemPrompt: "system message",
			Messages: []message.UnifiedMessage{
				{Role: message.RoleUser, Parts: []message.Part{{Type: message.PartTypeText, Text: "old"}}},
				{Role: message.RoleAssistant, Parts: []message.Part{{Type: message.PartTypeText, Text: "old-answer"}}},
				{Role: message.RoleUser, Parts: []message.Part{{Type: message.PartTypeText, Text: "new-turn"}}},
			},
			Metadata: message.RequestMetadata{
				ConversationID:  "conv-compact",
				PayloadStrategy: "sticky_compact",
			},
		},
	})
	if err != nil {
		t.Fatalf("buildPayload error: %v", err)
	}

	history := payload["conversationState"].(map[string]any)["history"].([]map[string]any)
	if len(history) != 2 {
		t.Fatalf("expected compact history to keep only system pair, got %d", len(history))
	}
	state := payload["conversationState"].(map[string]any)
	if state["workspaceId"] == "" {
		t.Fatal("expected compact payload workspaceId")
	}
	if state["agentContinuationId"] == "" {
		t.Fatal("expected compact payload agentContinuationId")
	}
	if state["agentTaskType"] != "vibe" {
		t.Fatalf("expected compact payload agentTaskType vibe, got %v", state["agentTaskType"])
	}
	ctx := state["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)["userInputMessageContext"].(map[string]any)
	if _, ok := ctx["gitState"].(map[string]any); !ok {
		t.Fatalf("expected compact payload gitState skeleton, got %T", ctx["gitState"])
	}
	if _, ok := ctx["editorStates"].([]map[string]any); !ok {
		t.Fatalf("expected compact payload editorStates skeleton, got %T", ctx["editorStates"])
	}
	if shellState, ok := ctx["shellState"].(map[string]any); !ok || shellState["currentWorkingDirectory"] == "" {
		t.Fatalf("expected compact payload shellState cwd, got %#v", ctx["shellState"])
	}
	if _, ok := ctx["diagnostic"].(map[string]any); !ok {
		t.Fatalf("expected compact payload diagnostic skeleton, got %T", ctx["diagnostic"])
	}
	if _, ok := ctx["consoleState"].(map[string]any); !ok {
		t.Fatalf("expected compact payload consoleState skeleton, got %T", ctx["consoleState"])
	}
	if _, ok := ctx["userSettings"].(map[string]any); !ok {
		t.Fatalf("expected compact payload userSettings skeleton, got %T", ctx["userSettings"])
	}
	if _, ok := ctx["additionalContext"].([]map[string]any); !ok {
		t.Fatalf("expected compact payload additionalContext skeleton, got %T", ctx["additionalContext"])
	}
	appStudio, ok := ctx["appStudioContext"].(map[string]any)
	if !ok {
		t.Fatalf("expected compact payload appStudioContext skeleton, got %T", ctx["appStudioContext"])
	}
	if _, ok := appStudio["cursorState"].(map[string]any); !ok {
		t.Fatalf("expected appStudio cursorState skeleton, got %T", appStudio["cursorState"])
	}
	if _, ok := appStudio["relevantDocuments"].([]map[string]any); !ok {
		t.Fatalf("expected appStudio relevantDocuments skeleton, got %T", appStudio["relevantDocuments"])
	}
	current := payload["conversationState"].(map[string]any)["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)["content"].(string)
	if current != "new-turn" {
		t.Fatalf("expected compact current message new-turn, got %q", current)
	}
}
