package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ARMmaster17/minirouter/internal/app"
)

func TestOpenAIProviderModelsAndChat(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "gpt-4o-mini"}}})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "gpt-4o-mini", "choices": []map[string]any{{"message": map[string]any{"content": "ok"}}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	provider := NewOpenAIProvider("openai:test", server.URL+"/v1", "", []app.Model{{ID: "openai:test:gpt-4o-mini"}})
	models, err := provider.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "openai:test:gpt-4o-mini" {
		t.Fatalf("unexpected models: %+v", models)
	}
	response, err := provider.ChatCompletions(context.Background(), app.ChatRequest{Model: "openai:test:gpt-4o-mini", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "ok" {
		t.Fatalf("unexpected content: %s", response.Content)
	}
}

func TestOllamaProviderModelsAndChat(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]any{{"name": "llama3.1"}}})
	})
	mux.HandleFunc("/api/chat", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "llama3.1", "message": map[string]any{"content": "ollama-ok"}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	provider := NewOllamaProvider("ollama:local", server.URL, "", []app.Model{{ID: "ollama:local:llama3.1"}})
	models, err := provider.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "ollama:local:llama3.1" {
		t.Fatalf("unexpected models: %+v", models)
	}
	response, err := provider.ChatCompletions(context.Background(), app.ChatRequest{Model: "ollama:local:llama3.1", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "ollama-ok" {
		t.Fatalf("unexpected content: %s", response.Content)
	}
}

func TestGeminiProviderModelsAndChat(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1beta/models", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]any{{"name": "models/gemini-2.5-flash"}}})
	})
	mux.HandleFunc("/v1beta/models/gemini-2.5-flash:generateContent", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"candidates": []map[string]any{{"content": map[string]any{"parts": []map[string]any{{"text": "gemini-ok"}}}}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	provider := NewGeminiProvider("gemini:cloud", server.URL+"/v1beta", "", []app.Model{{ID: "gemini:cloud:gemini-2.5-flash"}})
	models, err := provider.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "gemini:cloud:gemini-2.5-flash" {
		t.Fatalf("unexpected models: %+v", models)
	}
	response, err := provider.ChatCompletions(context.Background(), app.ChatRequest{Model: "gemini:cloud:gemini-2.5-flash", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "gemini-ok" {
		t.Fatalf("unexpected content: %s", response.Content)
	}
}

func TestLMStudioProviderUsesOpenAICompatibility(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "local-model", "choices": []map[string]any{{"message": map[string]any{"content": "lmstudio-ok"}}}})
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "local-model"}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	provider := NewLMStudioProvider("lmstudio:dev", server.URL+"/v1", "", []app.Model{{ID: "lmstudio:dev:local-model"}})
	response, err := provider.ChatCompletions(context.Background(), app.ChatRequest{Model: "lmstudio:dev:local-model", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "lmstudio-ok" {
		t.Fatalf("unexpected content: %s", response.Content)
	}
}

func TestOpenAIProviderModelMetadataUsesConfigOverrides(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{
			"id":                      "gpt-4o-mini",
			"context_length":          32000,
			"input_cost_per_million":  1.25,
			"output_cost_per_million": 2.5,
		}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	contextLimitOverride := 128000
	inputOverride := 0.10
	configured := []app.Model{{
		ID:              "openai:test:gpt-4o-mini",
		ContextLimit:    &contextLimitOverride,
		TokenInputCost:  &inputOverride,
		TokenOutputCost: nil,
	}}

	provider := NewOpenAIProvider("openai:test", server.URL+"/v1", "", configured)
	models, err := provider.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("expected one model, got %+v", models)
	}
	if models[0].ContextLimit == nil || *models[0].ContextLimit != 128000 {
		t.Fatalf("expected context override to win, got %+v", models[0].ContextLimit)
	}
	if models[0].TokenInputCost == nil || *models[0].TokenInputCost != 0.10 {
		t.Fatalf("expected input cost override to win, got %+v", models[0].TokenInputCost)
	}
	if models[0].TokenOutputCost == nil || *models[0].TokenOutputCost != 2.5 {
		t.Fatalf("expected output cost from upstream when no override, got %+v", models[0].TokenOutputCost)
	}
}

func TestOpenAIProviderConfiguredModelsAreAllowlist(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "model-a"}, {"id": "model-b"}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	provider := NewOpenAIProvider("openai:test", server.URL+"/v1", "", []app.Model{{ID: "openai:test:model-b"}})
	models, err := provider.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "openai:test:model-b" {
		t.Fatalf("expected allowlist to include only configured model, got %+v", models)
	}
}

func TestOpenAIProviderErrorsWhenConfiguredModelMissingUpstream(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "model-a"}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	provider := NewOpenAIProvider("openai:test", server.URL+"/v1", "", []app.Model{{ID: "openai:test:model-missing"}})
	_, err := provider.Models(context.Background())
	if err == nil {
		t.Fatalf("expected error when configured model is missing upstream")
	}
	if !strings.Contains(err.Error(), "missing configured models") {
		t.Fatalf("expected missing configured models error, got %v", err)
	}
}

func TestGeminiProviderConfiguredModelsAreAllowlist(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1beta/models", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]any{{"name": "models/gemini-a"}, {"name": "models/gemini-b"}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	provider := NewGeminiProvider("gemini:test", server.URL+"/v1beta", "", []app.Model{{ID: "gemini:test:gemini-b"}})
	models, err := provider.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "gemini:test:gemini-b" {
		t.Fatalf("expected allowlist to include only configured model, got %+v", models)
	}
}

func TestOllamaProviderConfiguredModelsAreAllowlist(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]any{{"name": "model-a"}, {"name": "model-b"}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	provider := NewOllamaProvider("ollama:test", server.URL, "", []app.Model{{ID: "ollama:test:model-b"}})
	models, err := provider.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "ollama:test:model-b" {
		t.Fatalf("expected allowlist to include only configured model, got %+v", models)
	}
}

func TestOpenAIProviderForwardsFullChatPayload(t *testing.T) {
	var captured map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if err := json.Unmarshal(bodyBytes, &captured); err != nil {
			t.Fatalf("decode forwarded body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-1",
			"object": "chat.completion",
			"model":  "gpt-4o-mini",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []map[string]any{{
						"id":       "call_1",
						"type":     "function",
						"function": map[string]any{"name": "hello", "arguments": "{}"},
					}},
				},
				"finish_reason": "tool_calls",
			}},
		})
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "gpt-4o-mini"}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	var req app.ChatRequest
	body := []byte(`{"model":"openai:test:gpt-4o-mini","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],"tools":[{"type":"function","function":{"name":"hello","description":"say hi","parameters":{"type":"object"}}}],"tool_choice":"auto","temperature":0.2}`)
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}

	provider := NewOpenAIProvider("openai:test", server.URL+"/v1", "", []app.Model{{ID: "openai:test:gpt-4o-mini"}})
	response, err := provider.ChatCompletions(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := captured["tools"]; !ok {
		t.Fatalf("expected forwarded body to include tools, got %+v", captured)
	}
	messages, ok := captured["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("expected forwarded messages, got %+v", captured["messages"])
	}
	if response.RawJSON == nil || !strings.Contains(string(response.RawJSON), "tool_calls") {
		t.Fatalf("expected raw response to preserve tool_calls, got %s", string(response.RawJSON))
	}
	if response.Model != "openai:test:gpt-4o-mini" {
		t.Fatalf("expected provider-prefixed response model, got %s", response.Model)
	}
}
