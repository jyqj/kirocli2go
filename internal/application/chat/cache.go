package chat

import (
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"kirocli-go/internal/domain/message"
	"kirocli-go/internal/tokenutil"
)

const fakeCacheTTL = 5 * time.Minute

type FakeCache struct {
	mu    sync.Mutex
	items map[uint64]time.Time
}

func NewFakeCache() *FakeCache {
	fc := &FakeCache{
		items: make(map[uint64]time.Time),
	}
	go fc.cleanup()
	return fc
}

func ComputeCacheKey(rawBody []byte) uint64 {
	h := fnv.New64a()
	cutoff := len(rawBody) * 4 / 5
	if cutoff < 128 {
		cutoff = len(rawBody)
	}
	if cutoff > len(rawBody) {
		cutoff = len(rawBody)
	}
	_, _ = h.Write(rawBody[:cutoff])
	return h.Sum64()
}

func (fc *FakeCache) Lookup(key uint64) bool {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	if ts, ok := fc.items[key]; ok && time.Since(ts) < fakeCacheTTL {
		fc.items[key] = time.Now()
		return true
	}
	fc.items[key] = time.Now()
	return false
}

func (fc *FakeCache) cleanup() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		fc.mu.Lock()
		now := time.Now()
		for key, ts := range fc.items {
			if now.Sub(ts) > fakeCacheTTL*2 {
				delete(fc.items, key)
			}
		}
		fc.mu.Unlock()
	}
}

func ComputeCacheUsage(inputTokens int, cacheHit bool) (int, int, int) {
	if inputTokens < 1024 {
		return 0, 0, inputTokens
	}

	cacheable := inputTokens * 9 / 10
	remaining := inputTokens - cacheable
	if cacheHit {
		return 0, cacheable, remaining
	}
	return cacheable, 0, remaining
}

func EstimateAnthropicInputTokens(req message.UnifiedRequest) int {
	var builder strings.Builder

	if req.SystemPrompt != "" {
		builder.WriteString(req.SystemPrompt)
		builder.WriteString("\n")
	}

	for _, msg := range req.Messages {
		for _, part := range msg.Parts {
			if part.Type == message.PartTypeText && part.Text != "" {
				builder.WriteString(part.Text)
				builder.WriteString("\n")
			}
		}
		for _, call := range msg.ToolCalls {
			builder.WriteString(call.Name)
			builder.WriteString("\n")
			builder.Write(call.Arguments)
			builder.WriteString("\n")
		}
		for _, result := range msg.ToolResults {
			for _, part := range result.Content {
				if part.Type == message.PartTypeText && part.Text != "" {
					builder.WriteString(part.Text)
					builder.WriteString("\n")
				}
			}
			if len(result.Payload) > 0 {
				builder.Write(result.Payload)
				builder.WriteString("\n")
			}
		}
	}

	for _, tool := range req.Tools {
		builder.WriteString(tool.Name)
		builder.WriteString("\n")
		builder.WriteString(tool.Description)
		builder.WriteString("\n")
		builder.Write(tool.InputSchema)
		builder.WriteString("\n")
	}

	return tokenutil.CountText(builder.String())
}
