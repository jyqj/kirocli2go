package message

import "encoding/json"

type Protocol string

const (
	ProtocolOpenAI    Protocol = "openai"
	ProtocolAnthropic Protocol = "anthropic"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleDeveloper Role = "developer"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type PartType string

const (
	PartTypeText  PartType = "text"
	PartTypeImage PartType = "image"
)

type UnifiedRequest struct {
	Protocol     Protocol
	Model        string
	SystemPrompt string
	Stream       bool
	Messages     []UnifiedMessage
	Tools        []UnifiedTool
	Metadata     RequestMetadata
}

type RequestMetadata struct {
	ClientRequestID          string
	Endpoint                 string
	EstimatedInputTokens     int
	FakeCacheKey             uint64
	CacheHit                 bool
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	RemainingInputTokens     int
}

type UnifiedMessage struct {
	Role        Role
	Parts       []Part
	ToolCalls   []ToolCall
	ToolResults []ToolResult
}

type Part struct {
	Type     PartType
	Text     string
	MimeType string
	Data     []byte
}

type UnifiedTool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type ToolResult struct {
	ToolCallID string
	IsError    bool
	Content    []Part
	Payload    json.RawMessage
}
