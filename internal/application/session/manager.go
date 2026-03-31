package session

import (
	"context"
	"crypto/rand"
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	defaultTTL                     = 30 * time.Minute
	defaultSweepInterval           = 2 * time.Minute
	defaultMaxEntries              = 1024
	defaultContextCompactThreshold = 0.85
)

type Config struct {
	Enabled                 bool
	TTL                     time.Duration
	SweepInterval           time.Duration
	MaxEntries              int
	ContextCompactThreshold float64
	Now                     func() time.Time
}

type Binding struct {
	SessionKey          string
	ConversationID      string
	AccountID           string
	Epoch               int
	WorkingDirectory    string
	LastModel           string
	LastContextUsagePct float64
	LastInputTokens     int
	LastMessageCount    int
	CreatedAt           time.Time
	LastSeenAt          time.Time
}

type Snapshot struct {
	Enabled        bool             `json:"enabled"`
	TTLSeconds     int64            `json:"ttl_seconds"`
	SweepSeconds   int64            `json:"sweep_seconds"`
	MaxEntries     int              `json:"max_entries"`
	ActiveSessions int              `json:"active_sessions"`
	CreatedTotal   int64            `json:"created_total"`
	RotatedTotal   int64            `json:"rotated_total"`
	ExpiredTotal   int64            `json:"expired_total"`
	EvictedTotal   int64            `json:"evicted_total"`
	RotateReasons  map[string]int64 `json:"rotate_reasons,omitempty"`
}

type Update struct {
	ConversationID   string
	AccountID        string
	WorkingDirectory string
	Model            string
	ContextUsagePct  float64
	InputTokens      int
	MessageCount     int
	Touch            bool
}

type Manager struct {
	mu                      sync.Mutex
	enabled                 bool
	ttl                     time.Duration
	sweepInterval           time.Duration
	maxEntries              int
	contextCompactThreshold float64
	now                     func() time.Time
	items                   map[string]*Binding
	createdTotal            int64
	rotatedTotal            int64
	expiredTotal            int64
	evictedTotal            int64
	rotateReasons           map[string]int64
}

func New(cfg Config) *Manager {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = defaultTTL
	}
	sweep := cfg.SweepInterval
	if sweep <= 0 {
		sweep = defaultSweepInterval
	}
	maxEntries := cfg.MaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}
	threshold := cfg.ContextCompactThreshold
	if threshold <= 0 {
		threshold = defaultContextCompactThreshold
	}

	return &Manager{
		enabled:                 cfg.Enabled,
		ttl:                     ttl,
		sweepInterval:           sweep,
		maxEntries:              maxEntries,
		contextCompactThreshold: threshold,
		now:                     now,
		items:                   make(map[string]*Binding),
		rotateReasons:           make(map[string]int64),
	}
}

func (m *Manager) Enabled() bool {
	return m != nil && m.enabled
}

func (m *Manager) ContextCompactThreshold() float64 {
	if m == nil {
		return defaultContextCompactThreshold
	}
	return m.contextCompactThreshold
}

func (m *Manager) TTL() time.Duration {
	if m == nil {
		return defaultTTL
	}
	return m.ttl
}

func (m *Manager) Start(ctx context.Context) {
	if !m.Enabled() {
		return
	}
	ticker := time.NewTicker(m.sweepInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.DeleteExpired()
			}
		}
	}()
}

func (m *Manager) Get(sessionKey string) (Binding, bool) {
	if !m.Enabled() || sessionKey == "" {
		return Binding{}, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteExpiredLocked()

	item, ok := m.items[sessionKey]
	if !ok {
		return Binding{}, false
	}
	clone := *item
	return clone, true
}

func (m *Manager) Ensure(sessionKey, workdir string) Binding {
	if !m.Enabled() || sessionKey == "" {
		return Binding{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteExpiredLocked()

	if item, ok := m.items[sessionKey]; ok {
		item.LastSeenAt = m.now()
		if workdir != "" {
			item.WorkingDirectory = workdir
		}
		return *item
	}

	item := &Binding{
		SessionKey:       sessionKey,
		ConversationID:   newConversationID(),
		Epoch:            1,
		WorkingDirectory: workdir,
		CreatedAt:        m.now(),
		LastSeenAt:       m.now(),
	}
	m.items[sessionKey] = item
	m.createdTotal++
	m.evictOverflowLocked()
	return *item
}

func (m *Manager) Rotate(sessionKey, workdir string) Binding {
	return m.RotateWithReason(sessionKey, workdir, "")
}

func (m *Manager) RotateWithReason(sessionKey, workdir, reason string) Binding {
	if !m.Enabled() || sessionKey == "" {
		return Binding{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteExpiredLocked()

	now := m.now()
	item, ok := m.items[sessionKey]
	if !ok {
		item = &Binding{
			SessionKey: sessionKey,
			CreatedAt:  now,
		}
		m.items[sessionKey] = item
	}
	item.Epoch++
	if item.Epoch <= 0 {
		item.Epoch = 1
	}
	item.ConversationID = newConversationID()
	if workdir != "" {
		item.WorkingDirectory = workdir
	}
	item.LastSeenAt = now
	m.rotatedTotal++
	if reason != "" {
		m.rotateReasons[reason]++
	}
	m.evictOverflowLocked()
	return *item
}

func (m *Manager) Update(sessionKey string, update Update) (Binding, bool) {
	if !m.Enabled() || sessionKey == "" {
		return Binding{}, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteExpiredLocked()

	item, ok := m.items[sessionKey]
	if !ok {
		return Binding{}, false
	}

	if update.ConversationID != "" {
		item.ConversationID = update.ConversationID
	}
	if update.AccountID != "" {
		item.AccountID = update.AccountID
	}
	if update.WorkingDirectory != "" {
		item.WorkingDirectory = update.WorkingDirectory
	}
	if update.Model != "" {
		item.LastModel = update.Model
	}
	if update.ContextUsagePct > 0 {
		item.LastContextUsagePct = update.ContextUsagePct
	}
	if update.InputTokens > 0 {
		item.LastInputTokens = update.InputTokens
	}
	if update.MessageCount > 0 {
		item.LastMessageCount = update.MessageCount
	}
	if update.Touch {
		item.LastSeenAt = m.now()
	}

	return *item, true
}

func (m *Manager) Delete(sessionKey string) {
	if !m.Enabled() || sessionKey == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, sessionKey)
}

func (m *Manager) DeleteExpired() {
	if !m.Enabled() {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteExpiredLocked()
}

func (m *Manager) Snapshot() Snapshot {
	if m == nil {
		return Snapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteExpiredLocked()

	reasons := make(map[string]int64, len(m.rotateReasons))
	for key, value := range m.rotateReasons {
		reasons[key] = value
	}

	return Snapshot{
		Enabled:        m.enabled,
		TTLSeconds:     int64(m.ttl.Seconds()),
		SweepSeconds:   int64(m.sweepInterval.Seconds()),
		MaxEntries:     m.maxEntries,
		ActiveSessions: len(m.items),
		CreatedTotal:   m.createdTotal,
		RotatedTotal:   m.rotatedTotal,
		ExpiredTotal:   m.expiredTotal,
		EvictedTotal:   m.evictedTotal,
		RotateReasons:  reasons,
	}
}

func (m *Manager) deleteExpiredLocked() {
	if len(m.items) == 0 {
		return
	}
	cutoff := m.now().Add(-m.ttl)
	for key, item := range m.items {
		if item.LastSeenAt.Before(cutoff) {
			delete(m.items, key)
			m.expiredTotal++
		}
	}
}

func (m *Manager) evictOverflowLocked() {
	if len(m.items) <= m.maxEntries {
		return
	}

	items := make([]*Binding, 0, len(m.items))
	for _, item := range m.items {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].LastSeenAt.Before(items[j].LastSeenAt)
	})

	excess := len(items) - m.maxEntries
	for i := 0; i < excess; i++ {
		delete(m.items, items[i].SessionKey)
		m.evictedTotal++
	}
}

func newConversationID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		now := time.Now().UnixNano()
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			uint32(now>>32),
			uint16(now>>16),
			uint16(now),
			uint16(now>>8),
			uint64(now)&0xFFFFFFFFFFFF,
		)
	}

	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uint32(b[0])<<24|uint32(b[1])<<16|uint32(b[2])<<8|uint32(b[3]),
		uint16(b[4])<<8|uint16(b[5]),
		uint16(b[6])<<8|uint16(b[7]),
		uint16(b[8])<<8|uint16(b[9]),
		uint64(b[10])<<40|uint64(b[11])<<32|uint64(b[12])<<24|uint64(b[13])<<16|uint64(b[14])<<8|uint64(b[15]),
	)
}
