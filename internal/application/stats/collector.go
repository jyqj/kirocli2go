package stats

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"kirocli-go/internal/domain/account"
)

type Snapshot struct {
	UptimeSeconds         int64            `json:"uptime_seconds"`
	TotalRequests         int64            `json:"total_requests"`
	SuccessRequests       int64            `json:"success_requests"`
	FailedRequests        int64            `json:"failed_requests"`
	AttemptFailedRequests int64            `json:"attempt_failed_requests"`
	TotalRetries          int64            `json:"total_retries"`
	TotalInputTokens      int64            `json:"total_input_tokens"`
	TotalOutputTokens     int64            `json:"total_output_tokens"`
	TotalTokens           int64            `json:"total_tokens"`
	TotalCredits          float64          `json:"total_credits"`
	CompactTriggers       map[string]int64 `json:"compact_triggers,omitempty"`
}

type Collector struct {
	startTime time.Time

	totalRequests         int64
	successRequests       int64
	failedRequests        int64
	attemptFailedRequests int64
	totalRetries          int64
	totalInputTokens      int64
	totalOutputTokens     int64
	totalTokens           int64

	creditsMu    sync.RWMutex
	totalCredits float64
	compactMu    sync.RWMutex
	compactStats map[string]int64
}

func NewCollector() *Collector {
	return &Collector{
		startTime:    time.Now(),
		compactStats: make(map[string]int64),
	}
}

func (c *Collector) RecordRequest() {
	atomic.AddInt64(&c.totalRequests, 1)
}

func (c *Collector) RecordSuccess(meta account.SuccessMeta) {
	atomic.AddInt64(&c.successRequests, 1)
	atomic.AddInt64(&c.totalInputTokens, int64(meta.InputTokens))
	atomic.AddInt64(&c.totalOutputTokens, int64(meta.OutputTokens))
	atomic.AddInt64(&c.totalTokens, int64(meta.Tokens))

	if meta.Attempts > 1 {
		atomic.AddInt64(&c.attemptFailedRequests, int64(meta.Attempts-1))
		atomic.AddInt64(&c.totalRetries, int64(meta.Attempts-1))
	}

	if meta.Credits > 0 {
		c.creditsMu.Lock()
		c.totalCredits += meta.Credits
		c.creditsMu.Unlock()
	}
}

func (c *Collector) RecordFailure(meta account.FailureMeta) {
	atomic.AddInt64(&c.failedRequests, 1)

	attempts := meta.Attempts
	if attempts <= 0 {
		attempts = 1
	}
	atomic.AddInt64(&c.attemptFailedRequests, int64(attempts))
	if attempts > 1 {
		atomic.AddInt64(&c.totalRetries, int64(attempts-1))
	}
}

func (c *Collector) RecordCompact(reason string) {
	if strings.TrimSpace(reason) == "" {
		return
	}
	c.compactMu.Lock()
	defer c.compactMu.Unlock()
	if c.compactStats == nil {
		c.compactStats = make(map[string]int64)
	}
	c.compactStats[reason]++
}

func (c *Collector) Snapshot() Snapshot {
	c.creditsMu.RLock()
	totalCredits := c.totalCredits
	c.creditsMu.RUnlock()
	c.compactMu.RLock()
	compactTriggers := make(map[string]int64, len(c.compactStats))
	for key, value := range c.compactStats {
		compactTriggers[key] = value
	}
	c.compactMu.RUnlock()

	return Snapshot{
		UptimeSeconds:         int64(time.Since(c.startTime).Seconds()),
		TotalRequests:         atomic.LoadInt64(&c.totalRequests),
		SuccessRequests:       atomic.LoadInt64(&c.successRequests),
		FailedRequests:        atomic.LoadInt64(&c.failedRequests),
		AttemptFailedRequests: atomic.LoadInt64(&c.attemptFailedRequests),
		TotalRetries:          atomic.LoadInt64(&c.totalRetries),
		TotalInputTokens:      atomic.LoadInt64(&c.totalInputTokens),
		TotalOutputTokens:     atomic.LoadInt64(&c.totalOutputTokens),
		TotalTokens:           atomic.LoadInt64(&c.totalTokens),
		TotalCredits:          totalCredits,
		CompactTriggers:       compactTriggers,
	}
}

func (c *Collector) ApplySnapshot(snapshot Snapshot) {
	c.creditsMu.Lock()
	c.compactMu.Lock()
	c.startTime = time.Now().Add(-time.Duration(snapshot.UptimeSeconds) * time.Second)
	atomic.StoreInt64(&c.totalRequests, snapshot.TotalRequests)
	atomic.StoreInt64(&c.successRequests, snapshot.SuccessRequests)
	atomic.StoreInt64(&c.failedRequests, snapshot.FailedRequests)
	atomic.StoreInt64(&c.attemptFailedRequests, snapshot.AttemptFailedRequests)
	atomic.StoreInt64(&c.totalRetries, snapshot.TotalRetries)
	atomic.StoreInt64(&c.totalInputTokens, snapshot.TotalInputTokens)
	atomic.StoreInt64(&c.totalOutputTokens, snapshot.TotalOutputTokens)
	atomic.StoreInt64(&c.totalTokens, snapshot.TotalTokens)
	c.totalCredits = snapshot.TotalCredits
	c.compactStats = make(map[string]int64, len(snapshot.CompactTriggers))
	for key, value := range snapshot.CompactTriggers {
		c.compactStats[key] = value
	}
	c.compactMu.Unlock()
	c.creditsMu.Unlock()
}
