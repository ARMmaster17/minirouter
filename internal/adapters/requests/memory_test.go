package requests

import (
	"sync"
	"testing"
)

func TestInMemoryActiveRequestCounterIncrementDecrement(t *testing.T) {
	counter := NewInMemoryActiveRequestCounter()

	if got := counter.Increment("openai:default"); got != 1 {
		t.Fatalf("expected increment to 1, got %d", got)
	}
	if got := counter.Increment("openai:default"); got != 2 {
		t.Fatalf("expected increment to 2, got %d", got)
	}
	if got := counter.Count("openai:default"); got != 2 {
		t.Fatalf("expected count 2, got %d", got)
	}
	if got := counter.Decrement("openai:default"); got != 1 {
		t.Fatalf("expected decrement to 1, got %d", got)
	}
	if got := counter.Decrement("openai:default"); got != 0 {
		t.Fatalf("expected decrement to 0, got %d", got)
	}
	if got := counter.Count("openai:default"); got != 0 {
		t.Fatalf("expected count 0, got %d", got)
	}
}

func TestInMemoryActiveRequestCounterIsProviderScoped(t *testing.T) {
	counter := NewInMemoryActiveRequestCounter()
	counter.Increment("openai:default")
	counter.Increment("gemini:default")
	counter.Increment("gemini:default")

	if got := counter.Count("openai:default"); got != 1 {
		t.Fatalf("expected openai count 1, got %d", got)
	}
	if got := counter.Count("gemini:default"); got != 2 {
		t.Fatalf("expected gemini count 2, got %d", got)
	}
}

func TestInMemoryActiveRequestCounterConcurrentAccess(t *testing.T) {
	counter := NewInMemoryActiveRequestCounter()
	providerID := "lmstudio:local"

	var wg sync.WaitGroup
	for idx := 0; idx < 64; idx++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			counter.Increment(providerID)
		}()
	}
	wg.Wait()

	if got := counter.Count(providerID); got != 64 {
		t.Fatalf("expected count 64 after increments, got %d", got)
	}

	for idx := 0; idx < 64; idx++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			counter.Decrement(providerID)
		}()
	}
	wg.Wait()

	if got := counter.Count(providerID); got != 0 {
		t.Fatalf("expected count 0 after decrements, got %d", got)
	}
}
