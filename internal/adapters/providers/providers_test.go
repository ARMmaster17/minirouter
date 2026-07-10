package providers

import (
	"context"
	"encoding/json"
	"fmt"
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
	if response.Completion == nil {
		t.Fatalf("expected typed completion response")
	}
	if response.AssistantText() != "ok" {
		t.Fatalf("unexpected content: %s", response.AssistantText())
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
	if response.Completion == nil {
		t.Fatalf("expected typed completion response")
	}
	if response.AssistantText() != "ollama-ok" {
		t.Fatalf("unexpected content: %s", response.AssistantText())
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

	provider := NewGeminiProvider("gemini:cloud", server.URL+"/v1beta", "test-key", []app.Model{{ID: "gemini:cloud:models/gemini-2.5-flash"}})
	models, err := provider.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "gemini:cloud:models/gemini-2.5-flash" {
		t.Fatalf("unexpected models: %+v", models)
	}
	response, err := provider.ChatCompletions(context.Background(), app.ChatRequest{Model: "gemini:cloud:models/gemini-2.5-flash", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Completion == nil {
		t.Fatalf("expected typed completion response")
	}
	if response.AssistantText() != "gemini-ok" {
		t.Fatalf("unexpected content: %s", response.AssistantText())
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
	if response.Completion == nil {
		t.Fatalf("expected typed completion response")
	}
	if response.AssistantText() != "lmstudio-ok" {
		t.Fatalf("unexpected content: %s", response.AssistantText())
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

	provider := NewGeminiProvider("gemini:test", server.URL+"/v1beta", "test-key", []app.Model{{ID: "gemini:test:models/gemini-b"}})
	models, err := provider.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "gemini:test:models/gemini-b" {
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
	if response.Completion == nil {
		t.Fatalf("expected typed completion response")
	}
	if len(response.Completion.Choices) != 1 {
		t.Fatalf("expected one choice in response, got %+v", response.Completion.Choices)
	}
	if len(response.Completion.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("expected typed response to preserve tool_calls, got %+v", response.Completion.Choices[0].Message.ToolCalls)
	}
	if response.Model != "openai:test:gpt-4o-mini" {
		t.Fatalf("expected provider-prefixed response model, got %s", response.Model)
	}
}

func TestGeminiProviderTranslatesToolChoiceAndResponseFormat(t *testing.T) {
	var captured map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/v1beta/models/gemini-2.5-flash:generateContent", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if err := json.Unmarshal(bodyBytes, &captured); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content":      map[string]any{"parts": []map[string]any{{"text": "ok"}}},
				"finishReason": "STOP",
			}},
			"usageMetadata": map[string]any{
				"promptTokenCount":     10,
				"candidatesTokenCount": 4,
				"totalTokenCount":      14,
			},
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	var req app.ChatRequest
	body := []byte(`{"model":"gemini:test:models/gemini-2.5-flash","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup_weather","description":"Get weather","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}],"tool_choice":"required","response_format":{"type":"json_schema","json_schema":{"name":"weather_response","schema":{"type":"object","properties":{"temperature":{"type":"number"}}}}}}`)
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}

	provider := NewGeminiProvider("gemini:test", server.URL+"/v1beta/openai", "test-key", []app.Model{{ID: "gemini:test:models/gemini-2.5-flash"}})
	response, err := provider.ChatCompletions(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if response.Completion == nil {
		t.Fatalf("expected typed completion response")
	}
	if response.AssistantText() != "ok" {
		t.Fatalf("unexpected response content: %s", response.AssistantText())
	}

	toolsValue, ok := captured["tools"].([]any)
	if !ok || len(toolsValue) != 1 {
		t.Fatalf("expected one translated Gemini tool, got %+v", captured["tools"])
	}
	toolConfig, ok := captured["toolConfig"].(map[string]any)
	if !ok {
		t.Fatalf("expected translated toolConfig, got %+v", captured["toolConfig"])
	}
	functionCallingConfig, ok := toolConfig["functionCallingConfig"].(map[string]any)
	if !ok {
		t.Fatalf("expected functionCallingConfig, got %+v", toolConfig)
	}
	mode, _ := functionCallingConfig["mode"].(string)
	if mode != "ANY" {
		t.Fatalf("expected tool_choice required to map to ANY mode, got %q", mode)
	}
	generationConfig, ok := captured["generationConfig"].(map[string]any)
	if !ok {
		t.Fatalf("expected generationConfig, got %+v", captured["generationConfig"])
	}
	mimeType, _ := generationConfig["responseMimeType"].(string)
	if mimeType != "application/json" {
		t.Fatalf("expected response format json_schema to request JSON mime type, got %q", mimeType)
	}
	if _, exists := generationConfig["responseJsonSchema"]; !exists {
		t.Fatalf("expected responseJsonSchema in generationConfig, got %+v", generationConfig)
	}
}

func TestGeminiProviderNormalizesUnsupportedMessageRoles(t *testing.T) {
	var captured map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/v1beta/models/gemini-2.5-flash:generateContent", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if err := json.Unmarshal(bodyBytes, &captured); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{"parts": []map[string]any{{"text": "ok"}}},
			}},
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	var req app.ChatRequest
	body := []byte(`{"model":"gemini:test:models/gemini-2.5-flash","messages":[{"role":"system","content":"system instruction"},{"role":"assistant","content":"calling tool"},{"role":"tool","tool_call_id":"call_42","content":"{\"temperature\":18}"},{"role":"developer","content":"developer note"},{"role":"user","content":"final user turn"}]}`)
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}

	provider := NewGeminiProvider("gemini:test", server.URL+"/v1beta/openai", "test-key", []app.Model{{ID: "gemini:test:models/gemini-2.5-flash"}})
	if _, err := provider.ChatCompletions(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	contents, ok := captured["contents"].([]any)
	if !ok || len(contents) == 0 {
		t.Fatalf("expected contents in Gemini request, got %+v", captured)
	}
	roles := make([]string, 0, len(contents))
	foundToolMarker := false
	for _, entry := range contents {
		contentEntry, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("unexpected content entry type: %+v", entry)
		}
		role, _ := contentEntry["role"].(string)
		roles = append(roles, role)
		if role == "tool" || role == "system" || role == "developer" {
			t.Fatalf("found unsupported role %q in translated Gemini contents: %+v", role, contents)
		}
		parts, _ := contentEntry["parts"].([]any)
		for _, part := range parts {
			partMap, _ := part.(map[string]any)
			text, _ := partMap["text"].(string)
			if strings.Contains(text, "[tool_result tool_call_id=call_42]") {
				foundToolMarker = true
			}
		}
	}

	if !foundToolMarker {
		t.Fatalf("expected tool message marker with tool_call_id in Gemini request contents, got %+v", contents)
	}
	if !strings.Contains(strings.Join(roles, ","), "model") {
		t.Fatalf("expected assistant role to map to model, got roles %v", roles)
	}
}

func TestGeminiProviderStreamingToolCallSchemaIncludesIndexAndFinishReason(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1beta/models/gemini-2.5-flash:streamGenerateContent", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"id\":\"call_1\",\"name\":\"lookup_weather\",\"args\":{\"city\":\"London\"}}}]},\"finishReason\":\"MALFORMED_FUNCTION_CALL\"}]}\n\n")
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	provider := NewGeminiProvider("gemini:test", server.URL+"/v1beta/openai", "test-key", []app.Model{{ID: "gemini:test:models/gemini-2.5-flash"}})
	streamResp, err := provider.ChatCompletionsStream(context.Background(), app.ChatRequest{Model: "gemini:test:models/gemini-2.5-flash", Messages: []app.ChatMessage{{Role: "user", Content: app.ChatMessageContent{Text: "hello"}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer streamResp.Body.Close()

	bodyBytes, err := io.ReadAll(streamResp.Body)
	if err != nil {
		t.Fatalf("read stream body: %v", err)
	}

	dataLines := make([]string, 0)
	for _, line := range strings.Split(string(bodyBytes), "\n") {
		if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		}
	}
	if len(dataLines) < 3 {
		t.Fatalf("expected role/tool/finish chunks and done marker, got lines=%d body=%s", len(dataLines), string(bodyBytes))
	}
	if dataLines[len(dataLines)-1] != "[DONE]" {
		t.Fatalf("expected [DONE] last, got %q", dataLines[len(dataLines)-1])
	}

	toolChunkFound := false
	finishChunkFound := false
	for _, payload := range dataLines {
		if payload == "[DONE]" {
			continue
		}
		var chunk app.OpenAIChatCompletionChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("decode streamed chunk: %v payload=%s", err, payload)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if len(choice.Delta.ToolCalls) > 0 {
			toolChunkFound = true
			for i, toolCall := range choice.Delta.ToolCalls {
				if toolCall.Index == nil {
					t.Fatalf("expected tool call index for tool %d", i)
				}
				if *toolCall.Index != i {
					t.Fatalf("expected tool call index %d, got %d", i, *toolCall.Index)
				}
			}
		}
		if reason, ok := choice.FinishReason.(string); ok && strings.TrimSpace(reason) != "" {
			if reason != "tool_calls" {
				t.Fatalf("expected finish reason tool_calls, got %q", reason)
			}
			finishChunkFound = true
		}
	}

	if !toolChunkFound {
		t.Fatalf("expected streamed tool-call chunk, body=%s", string(bodyBytes))
	}
	if !finishChunkFound {
		t.Fatalf("expected streamed finish chunk, body=%s", string(bodyBytes))
	}
}
