package stats

import (
	"sync"
	"time"
)

type RequestLogEntry struct {
	Time                     int64   `json:"time"`
	RequestID                string  `json:"request_id,omitempty"`
	Protocol                 string  `json:"protocol"`
	Endpoint                 string  `json:"endpoint"`
	Model                    string  `json:"model"`
	AccountID                string  `json:"account_id,omitempty"`
	APIKeyID                 string  `json:"api_key_id,omitempty"`
	StickySession            bool    `json:"sticky_session,omitempty"`
	ConversationID           string  `json:"conversation_id,omitempty"`
	ConversationEpoch        int     `json:"conversation_epoch,omitempty"`
	CompactReason            string  `json:"compact_reason,omitempty"`
	PayloadStrategy          string  `json:"payload_strategy,omitempty"`
	CacheHit                 bool    `json:"cache_hit,omitempty"`
	Success                  bool    `json:"success"`
	Attempts                 int     `json:"attempts"`
	StatusCode               int     `json:"status_code,omitempty"`
	Error                    string  `json:"error,omitempty"`
	FailureReason            string  `json:"failure_reason,omitempty"`
	BodySignal               string  `json:"body_signal,omitempty"`
	InputTokens              int     `json:"input_tokens,omitempty"`
	OutputTokens             int     `json:"output_tokens,omitempty"`
	TotalTokens              int     `json:"total_tokens,omitempty"`
	Credits                  float64 `json:"credits,omitempty"`
	CacheCreationInputTokens int     `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int     `json:"cache_read_input_tokens,omitempty"`
}

type RequestLogRing struct {
	mu      sync.RWMutex
	entries []RequestLogEntry
	head    int
	count   int
}

type RequestLogQuery struct {
	Limit           int
	Offset          int
	Protocol        string
	Endpoint        string
	Model           string
	AccountID       string
	APIKeyID        string
	ConversationID  string
	CompactReason   string
	PayloadStrategy string
	Success         *bool
	FailureReason   string
	BodySignal      string
}

func NewRequestLogRing(size int) *RequestLogRing {
	if size <= 0 {
		size = 200
	}
	return &RequestLogRing{
		entries: make([]RequestLogEntry, size),
	}
}

func (r *RequestLogRing) Add(entry RequestLogEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if entry.Time == 0 {
		entry.Time = time.Now().Unix()
	}

	r.entries[r.head] = entry
	r.head = (r.head + 1) % len(r.entries)
	if r.count < len(r.entries) {
		r.count++
	}
}

func (r *RequestLogRing) List(limit int) []RequestLogEntry {
	return r.Query(RequestLogQuery{Limit: limit})
}

func (r *RequestLogRing) Query(query RequestLogQuery) []RequestLogEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.count == 0 {
		return nil
	}

	limit := query.Limit
	if limit <= 0 || limit > r.count {
		limit = r.count
	}
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}

	result := make([]RequestLogEntry, 0, limit)
	skipped := 0
	for i := 0; i < r.count; i++ {
		idx := (r.head - 1 - i + len(r.entries)) % len(r.entries)
		entry := r.entries[idx]
		if !matchesRequestLogQuery(entry, query) {
			continue
		}
		if skipped < offset {
			skipped++
			continue
		}
		result = append(result, entry)
		if len(result) >= limit {
			break
		}
	}
	return result
}

func matchesRequestLogQuery(entry RequestLogEntry, query RequestLogQuery) bool {
	if query.Protocol != "" && entry.Protocol != query.Protocol {
		return false
	}
	if query.Endpoint != "" && entry.Endpoint != query.Endpoint {
		return false
	}
	if query.Model != "" && entry.Model != query.Model {
		return false
	}
	if query.AccountID != "" && entry.AccountID != query.AccountID {
		return false
	}
	if query.APIKeyID != "" && entry.APIKeyID != query.APIKeyID {
		return false
	}
	if query.ConversationID != "" && entry.ConversationID != query.ConversationID {
		return false
	}
	if query.CompactReason != "" && entry.CompactReason != query.CompactReason {
		return false
	}
	if query.PayloadStrategy != "" && entry.PayloadStrategy != query.PayloadStrategy {
		return false
	}
	if query.Success != nil && entry.Success != *query.Success {
		return false
	}
	if query.FailureReason != "" && entry.FailureReason != query.FailureReason {
		return false
	}
	if query.BodySignal != "" && entry.BodySignal != query.BodySignal {
		return false
	}
	return true
}
