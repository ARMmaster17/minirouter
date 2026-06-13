package app

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	logadapters "github.com/ARMmaster17/minirouter/internal/adapters/logs"
	"github.com/ARMmaster17/minirouter/internal/config"
	"github.com/ARMmaster17/minirouter/internal/domain"
)

func TestStaticCatalogAndAutoRouting(t *testing.T) {
	cfg := config.Default()
	contextLimit := 128000
	cfg.Providers = []config.ProviderConfig{{Kind: "openai", Name: "openai", Enabled: true, Models: []config.ModelConfig{{ID: "gpt-4o-mini", ContextLimit: &contextLimit}}}}
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"openai:openai:gpt-4o-mini"}}

	catalog := NewStaticCatalog(cfg)
	if !catalog.HasModel("openai:openai:gpt-4o-mini") {
		t.Fatalf("expected catalog to contain provider model")
	}
	models := catalog.Models()
	if len(models) < 2 || models[1].ContextLimit == nil || *models[1].ContextLimit != 128000 {
		t.Fatalf("expected model context limit in catalog")
	}
	router := NewRouter(cfg, catalog)
	model, _, err := router.ResolveModel("auto", "simple request", 4)
	if err != nil {
		t.Fatal(err)
	}
	if model != "openai:openai:gpt-4o-mini" {
		t.Fatalf("expected routed model, got %s", model)
	}
}

func TestResolveModelThrashLimitPrefersRecentAdjacentCandidate(t *testing.T) {
	cfg := config.Default()
	limit := 1
	cfg.Providers = []config.ProviderConfig{{
		Kind:        "lmstudio",
		Name:        "local",
		Enabled:     true,
		ThrashLimit: &limit,
		Models:      []config.ModelConfig{{ID: "model-a"}, {ID: "model-b"}},
	}}
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"lmstudio:local:model-a", "lmstudio:local:model-b"}}

	requestLogs := logadapters.NewInMemoryRequestLogStore(100)
	_ = requestLogs.Append(context.Background(), domain.RequestLogEntry{
		CreatedAt:     time.Now(),
		ResolvedModel: "lmstudio:local:model-b",
		ProviderID:    "lmstudio:local",
		Status:        domain.RequestStatusSuccess,
	})

	router := NewRouter(cfg, NewStaticCatalog(cfg)).WithRequestLogStore(requestLogs)
	model, _, err := router.ResolveModel("auto", "simple request", 4)
	if err != nil {
		t.Fatal(err)
	}
	if model != "lmstudio:local:model-b" {
		t.Fatalf("expected recent adjacent model to be preferred, got %s", model)
	}
}

func TestResolveModelThrashLimitDoesNotOverrideLargeRankGap(t *testing.T) {
	cfg := config.Default()
	limit := 1
	cfg.Providers = []config.ProviderConfig{{
		Kind:        "lmstudio",
		Name:        "local",
		Enabled:     true,
		ThrashLimit: &limit,
		Models:      []config.ModelConfig{{ID: "model-a"}, {ID: "model-c"}, {ID: "model-b"}},
	}}
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"lmstudio:local:model-a", "lmstudio:local:model-c", "lmstudio:local:model-b"}}

	requestLogs := logadapters.NewInMemoryRequestLogStore(100)
	_ = requestLogs.Append(context.Background(), domain.RequestLogEntry{
		CreatedAt:     time.Now(),
		ResolvedModel: "lmstudio:local:model-b",
		ProviderID:    "lmstudio:local",
		Status:        domain.RequestStatusSuccess,
	})

	router := NewRouter(cfg, NewStaticCatalog(cfg)).WithRequestLogStore(requestLogs)
	model, _, err := router.ResolveModel("auto", "simple request", 4)
	if err != nil {
		t.Fatal(err)
	}
	if model != "lmstudio:local:model-a" {
		t.Fatalf("expected top-ranked candidate to remain selected when not close, got %s", model)
	}
}

func TestResolveModelSkipsModelOutsideContextLimit(t *testing.T) {
	cfg := config.Default()
	smallLimit := 100
	largeLimit := 2000
	cfg.Providers = []config.ProviderConfig{{
		Kind:    "openai",
		Name:    "openai",
		Enabled: true,
		Models: []config.ModelConfig{
			{ID: "small", ContextLimit: &smallLimit},
			{ID: "large", ContextLimit: &largeLimit},
		},
	}}
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"openai:openai:small", "openai:openai:large"}}

	router := NewRouter(cfg, NewStaticCatalog(cfg))
	model, _, err := router.ResolveModel("auto", "simple request", 500)
	if err != nil {
		t.Fatal(err)
	}
	if model != "openai:openai:large" {
		t.Fatalf("expected larger context model to be selected, got %s", model)
	}
}

func TestResolveModelPrefersLowerCostWhenCandidatesClose(t *testing.T) {
	cfg := config.Default()
	expensiveIn := 5.0
	expensiveOut := 10.0
	cheapIn := 0.1
	cheapOut := 0.2
	cfg.Providers = []config.ProviderConfig{{
		Kind:    "openai",
		Name:    "openai",
		Enabled: true,
		Models: []config.ModelConfig{
			{ID: "expensive", TokenInputCost: &expensiveIn, TokenOutputCost: &expensiveOut},
			{ID: "cheap", TokenInputCost: &cheapIn, TokenOutputCost: &cheapOut},
		},
	}}
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"openai:openai:expensive", "openai:openai:cheap"}}

	router := NewRouter(cfg, NewStaticCatalog(cfg))
	model, _, err := router.ResolveModel("auto", "simple request", 10)
	if err != nil {
		t.Fatal(err)
	}
	if model != "openai:openai:cheap" {
		t.Fatalf("expected lower cost model to be preferred, got %s", model)
	}
}

func TestListModelsIsUniqueAndIncludesAuto(t *testing.T) {
	cfg := config.Default()
	cfg.Providers = []config.ProviderConfig{{
		Kind:    "openai",
		Name:    "openai",
		Enabled: true,
		Models:  []config.ModelConfig{{ID: "gpt-4o-mini"}},
	}}
	mock := NewMockProvider("openai:openai", []string{"gpt-4o-mini"}, nil)
	router := NewRouter(cfg, NewStaticCatalog(cfg), mock)

	models := router.ListModels()
	if len(models) != 2 {
		t.Fatalf("expected auto + one unique model, got %+v", models)
	}
	if models[0].ID != "auto" {
		t.Fatalf("expected auto model to be present and first, got %+v", models)
	}
}

func TestAutoRoutingSkipsTierModelsNotInRegistry(t *testing.T) {
	cfg := config.Default()
	cfg.Providers = []config.ProviderConfig{{
		Kind:    "openai",
		Name:    "openai",
		Enabled: true,
		Models:  []config.ModelConfig{{ID: "known"}},
	}}
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"openai:openai:missing", "openai:openai:known"}}

	mock := NewMockProvider("openai:openai", []string{"known"}, map[string]string{"openai:openai:known": "ok"})
	router := NewRouter(cfg, NewStaticCatalog(cfg), mock)

	model, _, err := router.ResolveModel("auto", "simple request", 8)
	if err != nil {
		t.Fatal(err)
	}
	if model != "openai:openai:known" {
		t.Fatalf("expected routing to skip missing tier candidate, got %s", model)
	}
}

func TestResolveModelPenalizesProviderAtParallelLimit(t *testing.T) {
	cfg := config.Default()
	limit := 1
	cfg.Providers = []config.ProviderConfig{
		{Kind: "openai", Name: "primary", Enabled: true, ParallelLimit: &limit, Models: []config.ModelConfig{{ID: "model-a"}}},
		{Kind: "openai", Name: "backup", Enabled: true, Models: []config.ModelConfig{{ID: "model-b"}}},
	}
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"openai:primary:model-a", "openai:backup:model-b"}}

	counter := &stubActiveRequestCounter{counts: map[string]int{"openai:primary": 3}}
	router := NewRouter(cfg, NewStaticCatalog(cfg)).WithActiveRequestCounter(counter)

	model, _, err := router.ResolveModel("auto", "simple request", 16)
	if err != nil {
		t.Fatal(err)
	}
	if model != "openai:backup:model-b" {
		t.Fatalf("expected model from less loaded provider, got %s", model)
	}
}

func TestChatBalancesActiveRequestCounter(t *testing.T) {
	cfg := config.Default()
	cfg.Providers = []config.ProviderConfig{{Kind: "mock", Name: "mock", Enabled: true, Models: []config.ModelConfig{{ID: "mock-chat"}}}}
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"mock:mock:mock-chat"}}

	counter := newTrackingActiveRequestCounter()
	provider := NewMockProvider("mock:mock", []string{"mock-chat"}, map[string]string{"mock:mock:mock-chat": "ok"})
	router := NewRouter(cfg, NewStaticCatalog(cfg), provider).WithActiveRequestCounter(counter)

	_, _, err := router.Chat(context.Background(), ChatRequest{Model: "auto", Prompt: "simple request"})
	if err != nil {
		t.Fatal(err)
	}
	if counter.Max("mock:mock") != 1 {
		t.Fatalf("expected active request counter to increment during chat, max observed=%d", counter.Max("mock:mock"))
	}
	if counter.Count("mock:mock") != 0 {
		t.Fatalf("expected active request counter to be balanced after chat, got %d", counter.Count("mock:mock"))
	}
}

func TestRoutingDebugPrintsClassificationRankingAndAttempts(t *testing.T) {
	cfg := config.Default()
	cfg.Routing.Debug = true
	cfg.Providers = []config.ProviderConfig{{
		Kind:    "mock",
		Name:    "mock",
		Enabled: true,
		Models:  []config.ModelConfig{{ID: "model-a"}, {ID: "model-b"}},
	}}
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"mock:mock:model-a", "mock:mock:model-b"}}

	provider := NewMockProviderWithFailures(
		"mock:mock",
		[]string{"model-a", "model-b"},
		map[string]string{"mock:mock:model-b": "ok"},
		map[string]string{"mock:mock:model-a": "timeout"},
	)
	router := NewRouter(cfg, NewStaticCatalog(cfg), provider)

	output := captureStdout(t, func() {
		_, _, err := router.Chat(context.Background(), ChatRequest{Model: "auto", Prompt: "simple request"})
		if err != nil {
			t.Fatal(err)
		}
	})

	checks := []string{
		"[routing-debug] classification",
		"[routing-debug] tier_selection",
		"[routing-debug] tier_candidates",
		"[routing-debug] chat_attempt_result",
		"\"rule\": \"baseOrder\"",
		"\"model\": \"mock:mock:model-a\"",
		"\"result\": \"failure\"",
		"\"model\": \"mock:mock:model-b\"",
		"\"result\": \"success\"",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected debug output to contain %q, got:\n%s", check, output)
		}
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	defer func() {
		os.Stdout = original
	}()

	outputCh := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go func() {
		var buffer bytes.Buffer
		_, copyErr := io.Copy(&buffer, reader)
		if copyErr != nil {
			errCh <- copyErr
			return
		}
		outputCh <- buffer.Bytes()
	}()

	fn()

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case copyErr := <-errCh:
		t.Fatal(copyErr)
	case data := <-outputCh:
		return string(data)
	}
	return ""
}

type stubActiveRequestCounter struct {
	counts map[string]int
}

func (s *stubActiveRequestCounter) Increment(providerID string) int {
	if s.counts == nil {
		s.counts = map[string]int{}
	}
	s.counts[providerID]++
	return s.counts[providerID]
}

func (s *stubActiveRequestCounter) Decrement(providerID string) int {
	if s.counts == nil {
		return 0
	}
	current := s.counts[providerID]
	if current <= 1 {
		delete(s.counts, providerID)
		return 0
	}
	s.counts[providerID] = current - 1
	return s.counts[providerID]
}

func (s *stubActiveRequestCounter) Count(providerID string) int {
	if s.counts == nil {
		return 0
	}
	return s.counts[providerID]
}

type trackingActiveRequestCounter struct {
	mu     sync.Mutex
	counts map[string]int
	max    map[string]int
}

func newTrackingActiveRequestCounter() *trackingActiveRequestCounter {
	return &trackingActiveRequestCounter{counts: make(map[string]int), max: make(map[string]int)}
}

func (c *trackingActiveRequestCounter) Increment(providerID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[providerID]++
	if c.counts[providerID] > c.max[providerID] {
		c.max[providerID] = c.counts[providerID]
	}
	return c.counts[providerID]
}

func (c *trackingActiveRequestCounter) Decrement(providerID string) int {
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

func (c *trackingActiveRequestCounter) Count(providerID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[providerID]
}

func (c *trackingActiveRequestCounter) Max(providerID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.max[providerID]
}
