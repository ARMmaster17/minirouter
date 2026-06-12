package requests

import (
	"strings"
	"sync"
)

// InMemoryActiveRequestCounter stores active request counts by provider.
type InMemoryActiveRequestCounter struct {
	mu     sync.RWMutex
	counts map[string]int
}

func NewInMemoryActiveRequestCounter() *InMemoryActiveRequestCounter {
	return &InMemoryActiveRequestCounter{counts: make(map[string]int)}
}

func (c *InMemoryActiveRequestCounter) Increment(providerID string) int {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[providerID]++
	return c.counts[providerID]
}

func (c *InMemoryActiveRequestCounter) Decrement(providerID string) int {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	current := c.counts[providerID]
	if current <= 1 {
		delete(c.counts, providerID)
		return 0
	}
	c.counts[providerID] = current - 1
	return c.counts[providerID]
}

func (c *InMemoryActiveRequestCounter) Count(providerID string) int {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.counts[providerID]
}
