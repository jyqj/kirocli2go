package truncation

import (
	"strings"
	"sync"
	"testing"
)

func TestToolTruncation_SaveAndGet(t *testing.T) {
	c := NewCache()

	c.SaveToolTruncation("tool-1", "write_file")

	info := c.GetToolTruncation("tool-1")
	if info == nil {
		t.Fatal("expected tool truncation info, got nil")
	}
	if info.ToolCallID != "tool-1" {
		t.Fatalf("expected tool call ID 'tool-1', got %q", info.ToolCallID)
	}
	if info.ToolName != "write_file" {
		t.Fatalf("expected tool name 'write_file', got %q", info.ToolName)
	}

	// Second read should return nil (one-shot)
	if again := c.GetToolTruncation("tool-1"); again != nil {
		t.Fatal("expected nil on second read (one-shot consumed)")
	}
}

func TestToolTruncation_MissReturnsNil(t *testing.T) {
	c := NewCache()
	if info := c.GetToolTruncation("nonexistent"); info != nil {
		t.Fatal("expected nil for unknown tool call ID")
	}
}

func TestContentTruncation_SaveAndGet(t *testing.T) {
	c := NewCache()

	content := strings.Repeat("hello world ", 100)
	hash := c.SaveContentTruncation(content)

	if hash == "" {
		t.Fatal("expected non-empty hash")
	}

	info := c.GetContentTruncation(content)
	if info == nil {
		t.Fatal("expected content truncation info, got nil")
	}
	if info.MessageHash != hash {
		t.Fatalf("hash mismatch: %q != %q", info.MessageHash, hash)
	}
	if len(info.ContentPreview) > 200 {
		t.Fatalf("preview too long: %d chars", len(info.ContentPreview))
	}

	// One-shot — second read returns nil
	if again := c.GetContentTruncation(content); again != nil {
		t.Fatal("expected nil on second read")
	}
}

func TestContentTruncation_ShortContent(t *testing.T) {
	c := NewCache()

	short := "short"
	hash := c.SaveContentTruncation(short)
	info := c.GetContentTruncation(short)

	if info == nil {
		t.Fatal("expected info for short content")
	}
	if info.ContentPreview != short {
		t.Fatalf("preview should equal original for short content, got %q", info.ContentPreview)
	}
	_ = hash
}

func TestStats(t *testing.T) {
	c := NewCache()

	c.SaveToolTruncation("t1", "tool_a")
	c.SaveToolTruncation("t2", "tool_b")
	c.SaveContentTruncation("some content")

	stats := c.Stats()
	if stats["tool_truncations"] != 2 {
		t.Fatalf("expected 2 tool truncations, got %d", stats["tool_truncations"])
	}
	if stats["content_truncations"] != 1 {
		t.Fatalf("expected 1 content truncation, got %d", stats["content_truncations"])
	}
	if stats["total"] != 3 {
		t.Fatalf("expected 3 total, got %d", stats["total"])
	}
}

func TestConcurrentAccess(t *testing.T) {
	c := NewCache()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(2)
		id := "tool-" + strings.Repeat("x", i%10+1)
		go func(id string) {
			defer wg.Done()
			c.SaveToolTruncation(id, "tool")
		}(id)
		go func(id string) {
			defer wg.Done()
			c.GetToolTruncation(id)
		}(id)
	}

	wg.Wait()
	// No race condition — test passes if it doesn't panic/deadlock
}

func TestGenerateToolResultContent(t *testing.T) {
	content := GenerateToolResultContent("write_file")
	if !strings.Contains(content, "[API Limitation]") {
		t.Fatal("expected [API Limitation] marker")
	}
	if !strings.Contains(content, "truncated") {
		t.Fatal("expected 'truncated' keyword")
	}
}

func TestGenerateUserMessage(t *testing.T) {
	msg := GenerateUserMessage()
	if !strings.Contains(msg, "[System Notice]") {
		t.Fatal("expected [System Notice] marker")
	}
}

func TestGetSystemPromptAddition(t *testing.T) {
	addition := GetSystemPromptAddition()
	if !strings.Contains(addition, "Output Truncation Handling") {
		t.Fatal("expected heading in system prompt addition")
	}
	if !strings.Contains(addition, "[System Notice]") {
		t.Fatal("expected [System Notice] mention")
	}
	if !strings.Contains(addition, "[API Limitation]") {
		t.Fatal("expected [API Limitation] mention")
	}
}
