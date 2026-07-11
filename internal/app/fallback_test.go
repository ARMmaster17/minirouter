package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ARMmaster17/minirouter/internal/config"
	"github.com/ARMmaster17/minirouter/internal/domain"
)

func TestChatFallsBackToBackupModel(t *testing.T) {
	cfg := config.Default()
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"mock:primary:primary-chat", "mock:backup:backup-chat"}}
	cfg.Routing.Failures.Default = domain.FailurePolicy{Retry: 0, TierSwitch: domain.TierSwitchNone}

	primary := NewMockProviderWithFailures("mock:primary", []string{"primary-chat"}, nil, map[string]string{"mock:primary:primary-chat": "primary failed"})
	backup := NewMockProvider("mock:backup", []string{"backup-chat"}, map[string]string{"mock:backup:backup-chat": "backup response"})
	router := NewRouter(cfg, NewStaticCatalog(cfg), primary, backup)

	response, _, err := router.Chat(context.Background(), ChatRequest{Model: "auto", Prompt: "simple request"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Model != "mock:backup:backup-chat" {
		t.Fatalf("expected fallback model, got %s", response.Model)
	}
	if response.AssistantText() != "backup response" {
		t.Fatalf("expected fallback response, got %q", response.AssistantText())
	}
}

func TestChatTierSwitchesDownRecursively(t *testing.T) {
	cfg := config.Default()
	tierNext := false
	cfg.Routing.Failures.Default = domain.FailurePolicy{Retry: 0, TierNext: &tierNext, TierSwitch: domain.TierSwitchDown}
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"mock:simple:simple"}}
	cfg.Routing.Tiers[domain.TierMedium] = domain.TierConfig{Models: []string{"mock:medium:medium"}}
	cfg.Routing.Tiers[domain.TierComplex] = domain.TierConfig{Models: []string{"mock:complex:complex"}}
	cfg.Routing.Tiers[domain.TierReasoning] = domain.TierConfig{Models: []string{"mock:reasoning:reasoning"}}

	reasoning := NewMockProviderWithFailures("mock:reasoning", []string{"reasoning"}, nil, map[string]string{"mock:reasoning:reasoning": "reasoning failed"})
	complex := NewMockProviderWithFailures("mock:complex", []string{"complex"}, nil, map[string]string{"mock:complex:complex": "complex failed"})
	medium := NewMockProviderWithFailures("mock:medium", []string{"medium"}, nil, map[string]string{"mock:medium:medium": "medium failed"})
	simple := NewMockProvider("mock:simple", []string{"simple"}, map[string]string{"mock:simple:simple": "simple response"})
	router := NewRouter(cfg, NewStaticCatalog(cfg), reasoning, complex, medium, simple)

	response, _, err := router.Chat(context.Background(), ChatRequest{Model: "mock:reasoning:reasoning", Prompt: "fallback down"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Model != "mock:simple:simple" {
		t.Fatalf("expected recursive tier switch to SIMPLE model, got %s", response.Model)
	}
}

func TestChatReturnsErrorWhenTierNextDisabledAndNoTierSwitch(t *testing.T) {
	cfg := config.Default()
	tierNext := false
	cfg.Routing.Failures.Default = domain.FailurePolicy{Retry: 0, TierNext: &tierNext, TierSwitch: domain.TierSwitchNone}
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"mock:primary:primary-chat", "mock:backup:backup-chat"}}

	primary := NewMockProviderWithFailures("mock:primary", []string{"primary-chat"}, nil, map[string]string{"mock:primary:primary-chat": "primary failed"})
	backup := NewMockProvider("mock:backup", []string{"backup-chat"}, map[string]string{"mock:backup:backup-chat": "backup response"})
	router := NewRouter(cfg, NewStaticCatalog(cfg), primary, backup)

	_, _, err := router.Chat(context.Background(), ChatRequest{Model: "mock:primary:primary-chat", Prompt: "no switch"})
	if err == nil {
		t.Fatalf("expected error when tierNext is false and tierSwitch is none")
	}
}

func TestChatSkipsPreviouslyFailedModelsAcrossTierSwitches(t *testing.T) {
	cfg := config.Default()
	tierNext := false
	cfg.Routing.Failures.Default = domain.FailurePolicy{Retry: 0, TierNext: &tierNext, TierSwitch: domain.TierSwitchDown}
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"mock:shared:shared"}}
	cfg.Routing.Tiers[domain.TierMedium] = domain.TierConfig{Models: []string{"mock:shared:shared"}}
	cfg.Routing.Tiers[domain.TierComplex] = domain.TierConfig{Models: []string{"mock:reasoning:reasoning"}}
	cfg.Routing.Tiers[domain.TierReasoning] = domain.TierConfig{Models: []string{"mock:reasoning:reasoning"}}

	reasoning := NewMockProviderWithFailures("mock:reasoning", []string{"reasoning"}, nil, map[string]string{"mock:reasoning:reasoning": "reasoning failed"})
	shared := NewMockProviderWithFailures("mock:shared", []string{"shared"}, nil, map[string]string{"mock:shared:shared": "shared failed"})
	router := NewRouter(cfg, NewStaticCatalog(cfg), reasoning, shared)

	_, _, err := router.Chat(context.Background(), ChatRequest{Model: "mock:reasoning:reasoning", Prompt: "skip failed"})
	if err == nil {
		t.Fatalf("expected failure when all models fail")
	}
	if strings.Contains(strings.ToLower(err.Error()), "cycle") {
		t.Fatalf("expected no cycle; failed models should be skipped across tiers, got %v", err)
	}
}

func TestChatStreamFallsBackWhenFirstEventIsError(t *testing.T) {
	cfg := config.Default()
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"stream:router:bad", "stream:router:good"}}
	provider := &routerStreamingProvider{}
	router := NewRouter(cfg, NewStaticCatalog(cfg), provider)

	response, _, err := router.ChatStream(context.Background(), ChatRequest{Model: "auto", Prompt: "simple request", Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	body, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		t.Fatal(readErr)
	}
	text := string(body)
	if strings.Contains(text, "startup failed") {
		t.Fatalf("expected startup error event to be hidden by fallback, got %s", text)
	}
	if !strings.Contains(text, "good-router") {
		t.Fatalf("expected fallback stream payload, got %s", text)
	}
	if provider.calls["stream:router:bad"] != 1 {
		t.Fatalf("expected bad model to be attempted once, got %+v", provider.calls)
	}
	if provider.calls["stream:router:good"] != 1 {
		t.Fatalf("expected good model to be attempted once, got %+v", provider.calls)
	}
}

func TestChatStreamDoesNotFallbackAfterFirstCommittedEvent(t *testing.T) {
	cfg := config.Default()
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"stream:router:post-bad", "stream:router:post-good"}}
	provider := &routerStreamingProvider{}
	router := NewRouter(cfg, NewStaticCatalog(cfg), provider)

	response, _, err := router.ChatStream(context.Background(), ChatRequest{Model: "auto", Prompt: "simple request", Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	_, _ = io.ReadAll(response.Body)
	if provider.calls["stream:router:post-bad"] != 1 {
		t.Fatalf("expected post-bad model to be attempted once, got %+v", provider.calls)
	}
	if provider.calls["stream:router:post-good"] != 0 {
		t.Fatalf("expected no fallback after first committed event, got %+v", provider.calls)
	}
}

type routerStreamingProvider struct {
	calls map[string]int
}

func (p *routerStreamingProvider) ID() string { return "stream:router" }

func (p *routerStreamingProvider) Models(_ context.Context) ([]Model, error) {
	return []Model{
		{ID: "stream:router:bad", Provider: "stream:router", OwnedBy: "stream:router", Object: "model"},
		{ID: "stream:router:good", Provider: "stream:router", OwnedBy: "stream:router", Object: "model"},
		{ID: "stream:router:post-bad", Provider: "stream:router", OwnedBy: "stream:router", Object: "model"},
		{ID: "stream:router:post-good", Provider: "stream:router", OwnedBy: "stream:router", Object: "model"},
	}, nil
}

func (p *routerStreamingProvider) ChatCompletions(_ context.Context, req ChatRequest) (ChatResponse, error) {
	return ChatResponse{Model: req.Model, Completion: NewTextCompletion(req.Model, "non-stream")}, nil
}

func (p *routerStreamingProvider) CanHandle(modelID string) bool {
	return strings.HasPrefix(modelID, "stream:router:")
}

func (p *routerStreamingProvider) ChatCompletionsStream(_ context.Context, req ChatRequest) (ChatStreamResponse, error) {
	if p.calls == nil {
		p.calls = map[string]int{}
	}
	p.calls[req.Model]++
	if strings.HasSuffix(req.Model, ":bad") {
		body := "data: {\"error\":{\"message\":\"startup failed\",\"type\":\"invalid_request_error\"}}\n\ndata: [DONE]\n\n"
		return ChatStreamResponse{Body: io.NopCloser(strings.NewReader(body)), ContentType: "text/event-stream", Model: req.Model}, nil
	}
	if strings.HasSuffix(req.Model, ":post-bad") {
		return ChatStreamResponse{Body: &routerFailingStreamBody{payload: []byte("data: {\"id\":\"stream\",\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n")}, ContentType: "text/event-stream", Model: req.Model}, nil
	}
	body := "data: {\"id\":\"stream\",\"choices\":[{\"delta\":{\"content\":\"good-router\"}}]}\n\ndata: [DONE]\n\n"
	return ChatStreamResponse{Body: io.NopCloser(strings.NewReader(body)), ContentType: "text/event-stream", Model: req.Model}, nil
}

type routerFailingStreamBody struct {
	payload []byte
	sent    bool
}

func (b *routerFailingStreamBody) Read(p []byte) (int, error) {
	if !b.sent {
		b.sent = true
		n := copy(p, b.payload)
		return n, nil
	}
	return 0, errors.New("upstream read failed")
}

func (b *routerFailingStreamBody) Close() error { return nil }
