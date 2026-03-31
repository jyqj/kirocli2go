package chat

import (
	"hash/fnv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"kirocli-go/internal/domain/message"
	"kirocli-go/internal/tokenutil"
)

const fakeCacheTTL = 5 * time.Minute

type FakeCache struct {
	mu    sync.Mutex
	items map[uint64]time.Time

	lookups int64
	hits    int64
	misses  int64
}

type FakeCacheSnapshot struct {
	Entries    int     `json:"entries"`
	Lookups    int64   `json:"lookups"`
	Hits       int64   `json:"hits"`
	Misses     int64   `json:"misses"`
	HitRate    float64 `json:"hit_rate"`
	TTLSeconds int64   `json:"ttl_seconds"`
}

func NewFakeCache() *FakeCache {
	fc := &FakeCache{
		items: make(map[uint64]time.Time),
	}
	go fc.cleanup()
	return fc
}

func ComputeCacheKey(rawBody []byte) uint64 {
	return ComputeScopedCacheKey("", rawBody)
}

func ComputeScopedCacheKey(namespace string, rawBody []byte) uint64 {
	h := fnv.New64a()
	if strings.TrimSpace(namespace) != "" {
		_, _ = h.Write([]byte(namespace))
		_, _ = h.Write([]byte{0})
	}
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
	atomic.AddInt64(&fc.lookups, 1)

	if ts, ok := fc.items[key]; ok && time.Since(ts) < fakeCacheTTL {
		fc.items[key] = time.Now()
		atomic.AddInt64(&fc.hits, 1)
		return true
	}
	fc.items[key] = time.Now()
	atomic.AddInt64(&fc.misses, 1)
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

func (fc *FakeCache) Snapshot() FakeCacheSnapshot {
	if fc == nil {
		return FakeCacheSnapshot{}
	}
	fc.mu.Lock()
	defer fc.mu.Unlock()

	lookups := atomic.LoadInt64(&fc.lookups)
	hits := atomic.LoadInt64(&fc.hits)
	misses := atomic.LoadInt64(&fc.misses)
	hitRate := 0.0
	if lookups > 0 {
		hitRate = float64(hits) / float64(lookups)
	}
	return FakeCacheSnapshot{
		Entries:    len(fc.items),
		Lookups:    lookups,
		Hits:       hits,
		Misses:     misses,
		HitRate:    hitRate,
		TTLSeconds: int64(fakeCacheTTL.Seconds()),
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
