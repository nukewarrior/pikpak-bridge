package app

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/nukewarrior/pikpak-bridge/internal/config"
)

const (
	quotaRefreshInterval = 5 * time.Minute
	quotaPollInterval    = time.Second
	quotaRequestTimeout  = 20 * time.Second
	quotaMaxConcurrent   = 3
)

// QuotaSummary is an in-memory view, never a database record.
// Failed accounts are excluded from Remaining, not silently treated as zero.
type QuotaSummary struct {
	Remaining       int64      `json:"remaining"`
	EnabledAccounts int        `json:"enabled_accounts"`
	CountedAccounts int        `json:"counted_accounts"`
	Complete        bool       `json:"complete"`
	Refreshing      bool       `json:"refreshing"`
	StaleRemaining  int64      `json:"stale_remaining"`
	StaleAccounts   int        `json:"stale_accounts"`
	UpdatedAt       *time.Time `json:"updated_at,omitempty"`
}

type cloudQuotaReader interface {
	CloudDownloadQuota(context.Context, string) (remaining, total int64, err error)
}

type quotaEntry struct {
	remaining int64
	lastOK    time.Time
	valid     bool
}

// QuotaMonitor keeps a small, bounded-concurrency in-memory snapshot.
// Its reader never runs on an HTTP request goroutine.
type QuotaMonitor struct {
	mu        sync.Mutex
	reader    cloudQuotaReader
	accounts  map[string]*quotaEntry
	pending   map[string]time.Time
	followup  map[string]time.Time
	inflight  map[string]bool
	active    int
	wg        sync.WaitGroup
}

func NewQuotaMonitor(reader cloudQuotaReader, accounts []config.PikPakAccount) *QuotaMonitor {
	m := &QuotaMonitor{
		reader:   reader,
		accounts: make(map[string]*quotaEntry),
		pending:  make(map[string]time.Time),
		followup: make(map[string]time.Time),
		inflight: make(map[string]bool),
	}
	for _, a := range accounts {
		if config.Enabled(a.Enabled) {
			m.accounts[a.ID] = &quotaEntry{}
		}
	}
	return m
}

// Summary never performs remote I/O and is safe to poll frequently.
func (m *QuotaMonitor) Summary() QuotaSummary {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := QuotaSummary{
		EnabledAccounts: len(m.accounts),
		Refreshing:      m.active > 0 || len(m.pending) > 0,
	}
	for _, entry := range m.accounts {
		if entry.valid {
			out.Remaining += entry.remaining
			out.CountedAccounts++
		} else if !entry.lastOK.IsZero() {
			out.StaleRemaining += entry.remaining
			out.StaleAccounts++
		}
		if !entry.lastOK.IsZero() && (out.UpdatedAt == nil || entry.lastOK.After(*out.UpdatedAt)) {
			t := entry.lastOK
			out.UpdatedAt = &t
		}
	}
	out.Complete = out.EnabledAccounts > 0 && out.CountedAccounts == out.EnabledAccounts
	return out
}

// RequestRefresh queues a whole-pool refresh, deduplicated by account ID.
func (m *QuotaMonitor) RequestRefresh() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for id := range m.accounts {
		m.pending[id] = now
	}
}

// Submitted runs after PikPak accepted a new remote task and its ID was persisted.
// PikPak may report the old quota briefly, so query once after 2s, then at 12s.
func (m *QuotaMonitor) Submitted(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[id]; !ok {
		return
	}
	now := time.Now()
	m.pending[id] = now.Add(2 * time.Second)
	m.followup[id] = now.Add(12 * time.Second)
}

// Run owns all remote queries and stops with the current runtime on hot reload.
func (m *QuotaMonitor) Run(ctx context.Context) {
	poll := time.NewTicker(quotaPollInterval)
	periodic := time.NewTicker(quotaRefreshInterval)
	defer poll.Stop()
	defer periodic.Stop()
	m.RequestRefresh()
	m.runPending(ctx)
	for {
		select {
		case <-ctx.Done():
			m.wg.Wait()
			return
		case <-poll.C:
			m.runPending(ctx)
		case <-periodic.C:
			m.RequestRefresh()
		}
	}
}

func (m *QuotaMonitor) runPending(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	now := time.Now()
	for id, due := range m.followup {
		if !due.After(now) {
			m.pending[id] = now
			delete(m.followup, id)
		}
	}
	ids := make([]string, 0, len(m.pending))
	for id := range m.pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if m.active >= quotaMaxConcurrent {
			break
		}
		if m.inflight[id] || m.pending[id].After(now) {
			continue
		}
		delete(m.pending, id)
		m.inflight[id] = true
		m.active++
		m.wg.Add(1)
		go m.refreshOne(ctx, id)
	}
}

func (m *QuotaMonitor) refreshOne(ctx context.Context, id string) {
	defer m.wg.Done()
	requestCtx, cancel := context.WithTimeout(ctx, quotaRequestTimeout)
	defer cancel()
	remaining, _, err := m.reader.CloudDownloadQuota(requestCtx, id)

	m.mu.Lock()
	defer m.mu.Unlock()
	if entry := m.accounts[id]; entry != nil {
		if err != nil {
			entry.valid = false
		} else {
			if remaining < 0 {
				remaining = 0
			}
			entry.remaining = remaining
			entry.lastOK = time.Now().UTC()
			entry.valid = true
		}
	}
	delete(m.inflight, id)
	m.active--
}
