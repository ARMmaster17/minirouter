package logs

import (
	"context"
	"testing"
	"time"

	"github.com/ARMmaster17/minirouter/internal/domain"
)

func TestInMemoryRequestLogStoreRetainsNewestEntries(t *testing.T) {
	store := NewInMemoryRequestLogStore(2)
	_ = store.Append(context.Background(), domain.RequestLogEntry{ID: "1", ResolvedModel: "m1", Status: domain.RequestStatusSuccess, Tier: domain.TierSimple, Duration: 10 * time.Millisecond})
	_ = store.Append(context.Background(), domain.RequestLogEntry{ID: "2", ResolvedModel: "m2", Status: domain.RequestStatusSuccess, Tier: domain.TierMedium, Duration: 20 * time.Millisecond})
	_ = store.Append(context.Background(), domain.RequestLogEntry{ID: "3", ResolvedModel: "m3", Status: domain.RequestStatusError, Tier: domain.TierComplex, Duration: 30 * time.Millisecond})

	recent := store.Recent(10)
	if len(recent) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(recent))
	}
	if recent[0].ID != "3" || recent[1].ID != "2" {
		t.Fatalf("expected newest-first entries [3,2], got [%s,%s]", recent[0].ID, recent[1].ID)
	}
}

func TestInMemoryRequestLogStoreStats(t *testing.T) {
	store := NewInMemoryRequestLogStore(10)
	_ = store.Append(context.Background(), domain.RequestLogEntry{ResolvedModel: "a", Status: domain.RequestStatusSuccess, Tier: domain.TierSimple, PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30, RequestBytes: 100, ResponseBytes: 200, Duration: 50 * time.Millisecond})
	_ = store.Append(context.Background(), domain.RequestLogEntry{ResolvedModel: "a", Status: domain.RequestStatusError, Tier: domain.TierSimple, PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8, RequestBytes: 40, ResponseBytes: 10, Duration: 150 * time.Millisecond})

	stats := store.Stats()
	if stats.TotalRequests != 2 || stats.SuccessRequests != 1 || stats.ErrorRequests != 1 {
		t.Fatalf("unexpected request counters: %+v", stats)
	}
	if stats.TotalTokens != 38 {
		t.Fatalf("expected total tokens 38, got %d", stats.TotalTokens)
	}
	if stats.ByModel["a"] != 2 {
		t.Fatalf("expected model count 2 for a, got %d", stats.ByModel["a"])
	}
	if stats.AverageLatencyMS != 100 {
		t.Fatalf("expected average latency 100ms, got %.0f", stats.AverageLatencyMS)
	}
}

func TestInMemoryRequestLogStoreSubscribe(t *testing.T) {
	store := NewInMemoryRequestLogStore(5)
	ch, unsubscribe := store.Subscribe()
	defer unsubscribe()

	_ = store.Append(context.Background(), domain.RequestLogEntry{ResolvedModel: "a", Status: domain.RequestStatusSuccess, Tier: domain.TierSimple})

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected subscriber notification")
	}
}
