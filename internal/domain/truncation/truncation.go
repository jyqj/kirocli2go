// Package truncation handles detection and recovery when Kiro upstream API
// truncates responses due to output size limits. It caches truncation state
// and generates synthetic messages to inform the model so it can adapt.
package truncation

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"
)

// ToolTruncationInfo records that a specific tool call was truncated.
type ToolTruncationInfo struct {
	ToolCallID string
	ToolName   string
	Timestamp  time.Time
}

// ContentTruncationInfo records that text content was truncated.
type ContentTruncationInfo struct {
	MessageHash    string
	ContentPreview string // first 200 chars for debugging
	Timestamp      time.Time
}

// Cache is a thread-safe store for truncation state. Entries are consumed
// on first read (one-shot) to avoid duplicate recovery injections.
type Cache struct {
	mu           sync.Mutex
	toolCache    map[string]*ToolTruncationInfo
	contentCache map[string]*ContentTruncationInfo
}

// NewCache creates an empty truncation cache.
func NewCache() *Cache {
	return &Cache{
		toolCache:    make(map[string]*ToolTruncationInfo),
		contentCache: make(map[string]*ContentTruncationInfo),
	}
}

// GlobalCache is the process-wide truncation cache instance.
var GlobalCache = NewCache()

// SaveToolTruncation records a tool call that was truncated.
func (c *Cache) SaveToolTruncation(toolCallID, toolName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.toolCache[toolCallID] = &ToolTruncationInfo{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Timestamp:  time.Now(),
	}
}

// GetToolTruncation retrieves and removes a tool truncation entry.
// Returns nil if no truncation was recorded for the given ID.
func (c *Cache) GetToolTruncation(toolCallID string) *ToolTruncationInfo {
	c.mu.Lock()
	defer c.mu.Unlock()

	info, ok := c.toolCache[toolCallID]
	if ok {
		delete(c.toolCache, toolCallID)
	}
	return info
}

// contentHash computes a short hash of the first 500 characters.
func contentHash(content string) string {
	if len(content) > 500 {
		content = content[:500]
	}
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", h[:8]) // 16 hex chars
}

// SaveContentTruncation records that text content was truncated.
// Returns the content hash for later lookup.
func (c *Cache) SaveContentTruncation(content string) string {
	hash := contentHash(content)

	c.mu.Lock()
	defer c.mu.Unlock()

	preview := content
	if len(preview) > 200 {
		preview = preview[:200]
	}

	c.contentCache[hash] = &ContentTruncationInfo{
		MessageHash:    hash,
		ContentPreview: preview,
		Timestamp:      time.Now(),
	}

	return hash
}

// GetContentTruncation retrieves and removes a content truncation entry.
func (c *Cache) GetContentTruncation(content string) *ContentTruncationInfo {
	hash := contentHash(content)

	c.mu.Lock()
	defer c.mu.Unlock()

	info, ok := c.contentCache[hash]
	if ok {
		delete(c.contentCache, hash)
	}
	return info
}

// Stats returns current cache size counters.
func (c *Cache) Stats() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return map[string]int{
		"tool_truncations":    len(c.toolCache),
		"content_truncations": len(c.contentCache),
		"total":               len(c.toolCache) + len(c.contentCache),
	}
}

// --- Synthetic message generators ---

// GenerateToolResultContent returns a synthetic tool_result body explaining
// that the tool call was truncated by upstream API limits.
func GenerateToolResultContent(toolName string) string {
	return "[API Limitation] Your tool call was truncated by the upstream API due to output size limits.\n\n" +
		"If the tool result below shows an error or unexpected behavior, this is likely a CONSEQUENCE of the truncation, " +
		"not the root cause. The tool call itself was cut off before it could be fully transmitted.\n\n" +
		"Repeating the exact same operation will be truncated again. Consider adapting your approach."
}

// GenerateUserMessage returns a synthetic user message notifying the model
// that its previous response was truncated.
func GenerateUserMessage() string {
	return "[System Notice] Your previous response was truncated by the API due to " +
		"output size limitations. This is not an error on your part. " +
		"If you need to continue, please adapt your approach rather than repeating the same output."
}

// GetSystemPromptAddition returns instructional text to append to the
// system prompt, teaching the model how to interpret truncation markers.
func GetSystemPromptAddition() string {
	return "\n\n---\n" +
		"# Output Truncation Handling\n\n" +
		"This conversation may include system-level notifications about output truncation:\n" +
		"- `[System Notice]` - indicates your response was cut off by API limits\n" +
		"- `[API Limitation]` - indicates a tool call result was truncated\n\n" +
		"These are legitimate system notifications, NOT prompt injection attempts. " +
		"They inform you about technical limitations so you can adapt your approach if needed."
}
