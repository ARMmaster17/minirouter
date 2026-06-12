package logs

import (
	"context"
	"sync"

	"github.com/ARMmaster17/minirouter/internal/domain"
)

const defaultMaxEntries = 1000

type InMemoryRequestLogStore struct {
	mu          sync.RWMutex
	maxEntries  int
	entries     []domain.RequestLogEntry
	stats       domain.RequestAggregateStats
	subscribers map[int]chan struct{}
	nextSubID   int
}

func NewInMemoryRequestLogStore(maxEntries int) *InMemoryRequestLogStore {
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}
	return &InMemoryRequestLogStore{
		maxEntries: maxEntries,
		entries:    make([]domain.RequestLogEntry, 0, maxEntries),
		stats: domain.RequestAggregateStats{
			ByModel: make(map[string]int),
			ByTier:  make(map[domain.Tier]int),
		},
		subscribers: make(map[int]chan struct{}),
	}
}

func (s *InMemoryRequestLogStore) Append(_ context.Context, entry domain.RequestLogEntry) error {
	s.mu.Lock()
	if len(s.entries) >= s.maxEntries {
		s.entries = s.entries[1:]
		s.recomputeStatsLocked()
	}
	s.entries = append(s.entries, entry)
	s.applyEntryLocked(entry)
	subs := make([]chan struct{}, 0, len(s.subscribers))
	for _, ch := range s.subscribers {
		subs = append(subs, ch)
	}
	s.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	return nil
}

func (s *InMemoryRequestLogStore) Recent(limit int) []domain.RequestLogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.entries) {
		limit = len(s.entries)
	}
	out := make([]domain.RequestLogEntry, 0, limit)
	start := len(s.entries) - limit
	for idx := len(s.entries) - 1; idx >= start; idx-- {
		out = append(out, s.entries[idx])
	}
	return out
}

func (s *InMemoryRequestLogStore) Stats() domain.RequestAggregateStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.stats
	out.ByModel = cloneModelMap(s.stats.ByModel)
	out.ByTier = cloneTierMap(s.stats.ByTier)
	return out
}

func (s *InMemoryRequestLogStore) Subscribe() (<-chan struct{}, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextSubID
	s.nextSubID++
	ch := make(chan struct{}, 1)
	s.subscribers[id] = ch
	unsubscribe := func() {
		s.mu.Lock()
		if existing, ok := s.subscribers[id]; ok {
			delete(s.subscribers, id)
			close(existing)
		}
		s.mu.Unlock()
	}
	return ch, unsubscribe
}

func (s *InMemoryRequestLogStore) applyEntryLocked(entry domain.RequestLogEntry) {
	s.stats.TotalRequests++
	if entry.Status == domain.RequestStatusSuccess {
		s.stats.SuccessRequests++
	} else {
		s.stats.ErrorRequests++
	}
	s.stats.TotalPromptTokens += entry.PromptTokens
	s.stats.TotalOutputTokens += entry.CompletionTokens
	s.stats.TotalTokens += entry.TotalTokens
	s.stats.TotalRequestBytes += entry.RequestBytes
	s.stats.TotalResponseBytes += entry.ResponseBytes
	if entry.ResolvedModel != "" {
		s.stats.ByModel[entry.ResolvedModel]++
	}
	s.stats.ByTier[entry.Tier]++
	totalLatency := s.stats.AverageLatencyMS*float64(s.stats.TotalRequests-1) + float64(entry.Duration.Milliseconds())
	s.stats.AverageLatencyMS = totalLatency / float64(s.stats.TotalRequests)
}

func (s *InMemoryRequestLogStore) recomputeStatsLocked() {
	s.stats = domain.RequestAggregateStats{
		ByModel: make(map[string]int),
		ByTier:  make(map[domain.Tier]int),
	}
	for _, entry := range s.entries {
		s.applyEntryLocked(entry)
	}
}

func cloneModelMap(input map[string]int) map[string]int {
	out := make(map[string]int, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func cloneTierMap(input map[domain.Tier]int) map[domain.Tier]int {
	out := make(map[domain.Tier]int, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
