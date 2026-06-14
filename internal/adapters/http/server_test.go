package httpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ARMmaster17/minirouter/internal/app"
	"github.com/ARMmaster17/minirouter/internal/config"
	"github.com/ARMmaster17/minirouter/internal/domain"
)

func TestModelsAndChatEndpoints(t *testing.T) {
	cfg := config.Default()
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"mock:mock:mock-chat"}}
	mock := app.NewMockProvider("mock:mock", []string{"mock-chat"}, map[string]string{"mock:mock:mock-chat": "mock reply"})
	server := New(app.NewRouter(cfg, app.NewStaticCatalog(cfg), mock))

	modelsReq := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	modelsRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(modelsRec, modelsReq)
	if modelsRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for models, got %d", modelsRec.Code)
	}
	var modelsPayload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(modelsRec.Body.Bytes(), &modelsPayload); err != nil {
		t.Fatalf("expected valid models payload, got error %v", err)
	}
	seen := make(map[string]struct{})
	hasAuto := false
	for _, model := range modelsPayload.Data {
		if model.ID == "auto" {
			hasAuto = true
		}
		if _, exists := seen[model.ID]; exists {
			t.Fatalf("expected unique model IDs, duplicate found: %s", model.ID)
		}
		seen[model.ID] = struct{}{}
	}
	if !hasAuto {
		t.Fatalf("expected auto model in /v1/models response")
	}

	body := map[string]any{"model": "auto", "prompt": "simple request"}
	encoded, _ := json.Marshal(body)
	chatReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(encoded))
	chatRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(chatRec, chatReq)
	if chatRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for chat, got %d body=%s", chatRec.Code, chatRec.Body.String())
	}
	if chatRec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("expected json response")
	}
}

func TestIncomingAPIKeyRequiredForOpenAICompatibleEndpoints(t *testing.T) {
	cfg := config.Default()
	cfg.Server.IncomingAPIKey = "secret"
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"mock:mock:mock-chat"}}
	mock := app.NewMockProvider("mock:mock", []string{"mock-chat"}, map[string]string{"mock:mock:mock-chat": "mock reply"})
	server := New(app.NewRouter(cfg, app.NewStaticCatalog(cfg), mock))

	modelsReq := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	modelsRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(modelsRec, modelsReq)
	if modelsRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for models without auth, got %d", modelsRec.Code)
	}

	body := map[string]any{"model": "auto", "prompt": "simple request"}
	encoded, _ := json.Marshal(body)
	chatReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(encoded))
	chatRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(chatRec, chatReq)
	if chatRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for chat without auth, got %d", chatRec.Code)
	}

	modelsAuthReq := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	modelsAuthReq.Header.Set("Authorization", "Bearer secret")
	modelsAuthRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(modelsAuthRec, modelsAuthReq)
	if modelsAuthRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for models with auth, got %d", modelsAuthRec.Code)
	}

	chatAuthReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(encoded))
	chatAuthReq.Header.Set("Authorization", "Bearer secret")
	chatAuthRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(chatAuthRec, chatAuthReq)
	if chatAuthRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for chat with auth, got %d body=%s", chatAuthRec.Code, chatAuthRec.Body.String())
	}
}

func TestFrontendDisabledReturns404OnRoot(t *testing.T) {
	cfg := config.Default()
	cfg.Server.FrontendEnabled = false
	server := New(app.NewRouter(cfg, app.NewStaticCatalog(cfg)))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when frontend is disabled, got %d", rec.Code)
	}
}

func TestFrontendEnabledServesRootAndFragments(t *testing.T) {
	cfg := config.Default()
	cfg.Server.FrontendEnabled = true
	contextLimit := 128000
	inputCost := 0.15
	outputCost := 0.60
	cfg.Providers = []config.ProviderConfig{{
		Kind:    "openai",
		Name:    "openai",
		Enabled: true,
		Models: []config.ModelConfig{{
			ID:              "gpt-4o-mini",
			ContextLimit:    &contextLimit,
			TokenInputCost:  &inputCost,
			TokenOutputCost: &outputCost,
		}},
	}}
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"mock:mock:mock-chat"}}
	mock := app.NewMockProvider("mock:mock", []string{"mock-chat"}, map[string]string{"mock:mock:mock-chat": "mock reply"})
	logs := &stubRequestLogStore{}
	logs.entries = []domain.RequestLogEntry{{
		CreatedAt:     time.Now(),
		ResolvedModel: "openai:openai:gpt-4o-mini",
		Tier:          domain.TierSimple,
		Status:        domain.RequestStatusSuccess,
		TokenSource:   domain.TokenSourceProvider,
		RawRequest:    `{"model":"auto","prompt":"hello"}`,
		RawResponse:   `{"id":"chatcmpl-1","object":"chat.completion"}`,
	}}
	counter := &stubActiveRequestCounter{counts: map[string]int{"openai:openai": 2}}
	server := New(app.NewRouter(cfg, app.NewStaticCatalog(cfg), mock).WithActiveRequestCounter(counter), logs)

	rootReq := httptest.NewRequest(http.MethodGet, "/", nil)
	rootRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rootRec, rootReq)
	if rootRec.Code != http.StatusOK {
		t.Fatalf("expected 200 at root, got %d", rootRec.Code)
	}
	if !strings.Contains(rootRec.Body.String(), "MiniRouter Traffic Console") {
		t.Fatalf("expected dashboard HTML")
	}

	statsReq := httptest.NewRequest(http.MethodGet, "/ui/fragments/stats", nil)
	statsRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(statsRec, statsReq)
	if statsRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for stats fragment, got %d", statsRec.Code)
	}

	requestsReq := httptest.NewRequest(http.MethodGet, "/ui/fragments/requests", nil)
	requestsRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(requestsRec, requestsReq)
	if requestsRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for requests fragment, got %d", requestsRec.Code)
	}
	if !strings.Contains(requestsRec.Body.String(), "Toggle raw payload") {
		t.Fatalf("expected requests fragment to include payload expander")
	}
	if !strings.Contains(requestsRec.Body.String(), html.EscapeString(`{"model":"auto","prompt":"hello"}`)) {
		t.Fatalf("expected requests fragment to include raw request payload")
	}
	if !strings.Contains(requestsRec.Body.String(), html.EscapeString(`{"id":"chatcmpl-1","object":"chat.completion"}`)) {
		t.Fatalf("expected requests fragment to include raw response payload")
	}

	activeReq := httptest.NewRequest(http.MethodGet, "/ui/fragments/active-requests", nil)
	activeRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(activeRec, activeReq)
	if activeRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for active requests fragment, got %d", activeRec.Code)
	}
	if !strings.Contains(activeRec.Body.String(), "openai:openai") {
		t.Fatalf("expected active requests to include provider entry")
	}
	if !strings.Contains(activeRec.Body.String(), "Total Active") {
		t.Fatalf("expected active requests fragment to include total")
	}

	modelsReq := httptest.NewRequest(http.MethodGet, "/ui/fragments/models", nil)
	modelsRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(modelsRec, modelsReq)
	if modelsRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for models fragment, got %d", modelsRec.Code)
	}
	if !strings.Contains(modelsRec.Body.String(), "Model Registry") && !strings.Contains(rootRec.Body.String(), "Model Registry") {
		t.Fatalf("expected model registry panel to be present")
	}
	if !strings.Contains(modelsRec.Body.String(), "openai:openai:gpt-4o-mini") {
		t.Fatalf("expected model registry to include configured model metadata")
	}
	if !strings.Contains(modelsRec.Body.String(), "Token Input Cost / 1M") {
		t.Fatalf("expected model registry table columns")
	}
}

func TestChatCompletionsStreamsWhenRequestedInBody(t *testing.T) {
	cfg := config.Default()
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"mock:mock:mock-chat"}}
	mock := app.NewMockProvider("mock:mock", []string{"mock-chat"}, map[string]string{"mock:mock:mock-chat": "mock streamed reply"})
	server := New(app.NewRouter(cfg, app.NewStaticCatalog(cfg), mock))

	body := map[string]any{"model": "auto", "prompt": "simple request", "stream": true}
	encoded, _ := json.Marshal(body)
	chatReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(encoded))
	chatRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(chatRec, chatReq)

	if chatRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for stream chat, got %d body=%s", chatRec.Code, chatRec.Body.String())
	}
	if !strings.HasPrefix(chatRec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("expected event-stream content type, got %s", chatRec.Header().Get("Content-Type"))
	}
	bodyText := chatRec.Body.String()
	if !strings.Contains(bodyText, "data: [DONE]") {
		t.Fatalf("expected done marker in stream body")
	}
	if !strings.Contains(bodyText, "mock streamed reply") {
		t.Fatalf("expected streamed content in body")
	}
}

func TestChatCompletionsStreamsWhenAcceptHeaderContainsEventStream(t *testing.T) {
	cfg := config.Default()
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"mock:mock:mock-chat"}}
	mock := app.NewMockProvider("mock:mock", []string{"mock-chat"}, map[string]string{"mock:mock:mock-chat": "mock reply"})
	server := New(app.NewRouter(cfg, app.NewStaticCatalog(cfg), mock))

	body := map[string]any{"model": "auto", "prompt": "simple request"}
	encoded, _ := json.Marshal(body)
	chatReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(encoded))
	chatReq.Header.Set("Accept", "application/json, text/event-stream; charset=utf-8")
	chatRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(chatRec, chatReq)

	if chatRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for accept-header stream chat, got %d body=%s", chatRec.Code, chatRec.Body.String())
	}
	if !strings.HasPrefix(chatRec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("expected event-stream content type, got %s", chatRec.Header().Get("Content-Type"))
	}
}

func TestShouldStreamResponse(t *testing.T) {
	tests := []struct {
		name     string
		stream   bool
		accept   string
		expected bool
	}{
		{name: "stream true wins", stream: true, accept: "application/json", expected: true},
		{name: "event stream exact", stream: false, accept: "text/event-stream", expected: true},
		{name: "event stream with params", stream: false, accept: "text/event-stream; charset=utf-8", expected: true},
		{name: "event stream among multiple", stream: false, accept: "application/json, text/event-stream", expected: true},
		{name: "non stream accept", stream: false, accept: "application/json", expected: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := shouldStreamResponse(tc.stream, tc.accept)
			if actual != tc.expected {
				t.Fatalf("expected %v, got %v", tc.expected, actual)
			}
		})
	}
}

func TestChatCompletionsUsesProviderSSEPassthroughWhenSupported(t *testing.T) {
	cfg := config.Default()
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"stream:mock:model"}}
	provider := &streamingMockProvider{}
	server := New(app.NewRouter(cfg, app.NewStaticCatalog(cfg), provider))

	body := map[string]any{"model": "auto", "prompt": "simple request", "stream": true}
	encoded, _ := json.Marshal(body)
	chatReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(encoded))
	chatRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(chatRec, chatReq)

	if chatRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for passthrough stream chat, got %d body=%s", chatRec.Code, chatRec.Body.String())
	}
	if !strings.HasPrefix(chatRec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("expected event-stream content type, got %s", chatRec.Header().Get("Content-Type"))
	}
	bodyText := chatRec.Body.String()
	if !strings.Contains(bodyText, "data: {\"id\":\"upstream-1\"") {
		t.Fatalf("expected passthrough upstream chunk in stream body")
	}
	if !strings.Contains(bodyText, "data: [DONE]") {
		t.Fatalf("expected done marker in stream body")
	}
	if provider.streamCalls != 1 {
		t.Fatalf("expected stream provider to be called once, got %d", provider.streamCalls)
	}
}

func TestChatCompletionsAcceptsArrayContentAndPreservesRawResponse(t *testing.T) {
	cfg := config.Default()
	cfg.Providers = []config.ProviderConfig{{Kind: "openai", Name: "openai", Enabled: true, Models: []config.ModelConfig{{ID: "gpt-4o-mini"}}}}
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"openai:openai:gpt-4o-mini"}}
	provider := &passthroughMockProvider{}
	server := New(app.NewRouter(cfg, app.NewStaticCatalog(cfg), provider))

	body := []byte(`{"model":"auto","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],"tools":[{"type":"function","function":{"name":"hello","parameters":{"type":"object"}}}]}`)
	chatReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	chatRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(chatRec, chatReq)

	if chatRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for array content payload, got %d body=%s", chatRec.Code, chatRec.Body.String())
	}
	if !strings.Contains(chatRec.Body.String(), "tool_calls") {
		t.Fatalf("expected raw OpenAI response to preserve tool_calls, got %s", chatRec.Body.String())
	}
	if len(provider.lastRequest.Messages) != 1 || provider.lastRequest.Messages[0].Content.TextValue() != "hello" {
		t.Fatalf("expected decoded message text from content parts, got %+v", provider.lastRequest.Messages)
	}
	if _, ok := provider.lastRequest.RawFields["tools"]; !ok {
		t.Fatalf("expected raw request fields to preserve tools")
	}
	if provider.calls != 1 {
		t.Fatalf("expected provider call, got %d", provider.calls)
	}
}

type streamingMockProvider struct {
	streamCalls int
}

func (p *streamingMockProvider) ID() string { return "stream:mock" }

func (p *streamingMockProvider) Models(_ context.Context) ([]app.Model, error) {
	return []app.Model{{ID: "stream:mock:model", Object: "model", OwnedBy: "stream:mock", Provider: "stream:mock"}}, nil
}

func (p *streamingMockProvider) ChatCompletions(_ context.Context, req app.ChatRequest) (app.ChatResponse, error) {
	return app.ChatResponse{Model: req.Model, Content: "non-stream"}, nil
}

func (p *streamingMockProvider) CanHandle(modelID string) bool {
	return strings.HasPrefix(modelID, "stream:mock:")
}

func (p *streamingMockProvider) ChatCompletionsStream(_ context.Context, _ app.ChatRequest) (app.ChatStreamResponse, error) {
	p.streamCalls++
	body := "data: {\"id\":\"upstream-1\",\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]\n\n"
	return app.ChatStreamResponse{Body: io.NopCloser(strings.NewReader(body)), ContentType: "text/event-stream"}, nil
}

type stubRequestLogStore struct {
	entries []domain.RequestLogEntry
}

func (s *stubRequestLogStore) Append(_ context.Context, _ domain.RequestLogEntry) error {
	return nil
}
func (s *stubRequestLogStore) Recent(_ int) []domain.RequestLogEntry {
	return s.entries
}
func (s *stubRequestLogStore) Stats() domain.RequestAggregateStats {
	return domain.RequestAggregateStats{}
}
func (s *stubRequestLogStore) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{})
	close(ch)
	return ch, func() {}
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

type passthroughMockProvider struct {
	calls       int
	lastRequest app.ChatRequest
}

func (p *passthroughMockProvider) ID() string { return "openai:openai" }

func (p *passthroughMockProvider) Models(_ context.Context) ([]app.Model, error) {
	return []app.Model{{ID: "openai:openai:gpt-4o-mini", Object: "model", OwnedBy: "openai:openai", Provider: "openai:openai"}}, nil
}

func (p *passthroughMockProvider) ChatCompletions(_ context.Context, req app.ChatRequest) (app.ChatResponse, error) {
	p.calls++
	p.lastRequest = req
	return app.ChatResponse{
		Model:   req.Model,
		RawJSON: []byte(`{"id":"chatcmpl-1","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"hello","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`),
	}, nil
}

func (p *passthroughMockProvider) CanHandle(modelID string) bool {
	return strings.HasPrefix(modelID, "openai:openai:")
}
