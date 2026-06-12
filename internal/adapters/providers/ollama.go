package providers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/ARMmaster17/minirouter/internal/app"
)

type OllamaProvider struct {
	providerID string
	baseURL    string
	apiKey     string
	configured []app.Model
	client     *http.Client
	mu         sync.RWMutex
	cached     []app.Model
}

func NewOllamaProvider(providerID, baseURL, apiKey string, configured []app.Model) *OllamaProvider {
	url := strings.TrimSpace(baseURL)
	if url == "" {
		url = "http://localhost:11434"
	}
	return &OllamaProvider{
		providerID: providerID,
		baseURL:    strings.TrimRight(url, "/"),
		apiKey:     strings.TrimSpace(apiKey),
		configured: configured,
		client:     defaultClient(),
	}
}

func (p *OllamaProvider) ID() string { return p.providerID }

func (p *OllamaProvider) CanHandle(modelID string) bool {
	return strings.HasPrefix(modelID, p.providerID+":")
}

func (p *OllamaProvider) Models(ctx context.Context) ([]app.Model, error) {
	type tagResponse struct {
		Models []map[string]any `json:"models"`
	}

	p.mu.RLock()
	if len(p.cached) > 0 {
		cached := make([]app.Model, len(p.cached))
		copy(cached, p.cached)
		p.mu.RUnlock()
		return cached, nil
	}
	p.mu.RUnlock()

	endpoint := joinURL(p.baseURL, "api/tags")
	var response tagResponse
	err := doJSON(ctx, p.client, http.MethodGet, endpoint, p.apiKey, nil, nil, &response)
	if err != nil {
		if len(p.configured) > 0 {
			return nil, fmt.Errorf("hydrate configured models for %s: %w", p.providerID, err)
		}
		return nil, err
	}
	models := make([]app.Model, 0, len(response.Models))
	for _, model := range response.Models {
		name, _ := model["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		contextLimit := readIntField(model, "context_length", "contextLength")
		if contextLimit == nil {
			if details, ok := model["details"].(map[string]any); ok {
				contextLimit = readIntField(details, "context_length", "contextLength")
			}
		}
		models = append(models, app.Model{
			ID:           app.ProviderModelID(p.providerID, name),
			Object:       "model",
			OwnedBy:      p.providerID,
			Provider:     p.providerID,
			ContextLimit: contextLimit,
		})
	}
	if len(models) == 0 && len(p.configured) > 0 {
		return nil, &ConfiguredModelsMissingError{ProviderID: p.providerID, Missing: configuredModelIDs(p.configured)}
	}
	selected, err := selectConfiguredModels(p.providerID, models, p.configured)
	if err != nil {
		return nil, err
	}
	merged := applyConfiguredMetadata(selected, p.configured)
	p.mu.Lock()
	p.cached = merged
	p.mu.Unlock()
	out := make([]app.Model, len(merged))
	copy(out, merged)
	return out, nil
}

func (p *OllamaProvider) ChatCompletions(ctx context.Context, req app.ChatRequest) (app.ChatResponse, error) {
	type ollamaMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type chatBody struct {
		Model    string          `json:"model"`
		Messages []ollamaMessage `json:"messages"`
		Stream   bool            `json:"stream"`
	}
	type chatResponse struct {
		Model           string `json:"model"`
		EvalCount       int    `json:"eval_count"`
		PromptEvalCount int    `json:"prompt_eval_count"`
		Message         struct {
			Content string `json:"content"`
		} `json:"message"`
	}

	messages := make([]ollamaMessage, 0, len(req.Messages))
	for _, message := range req.Messages {
		role := strings.TrimSpace(message.Role)
		if role == "" {
			role = "user"
		}
		messages = append(messages, ollamaMessage{Role: role, Content: message.Content.TextValue()})
	}
	if len(messages) == 0 {
		messages = append(messages, ollamaMessage{Role: "user", Content: extractPrompt(req)})
	}
	body := chatBody{Model: splitModelID(p.providerID, req.Model), Messages: messages, Stream: false}
	endpoint := joinURL(p.baseURL, "api/chat")
	var response chatResponse
	if err := doJSON(ctx, p.client, http.MethodPost, endpoint, p.apiKey, body, nil, &response); err != nil {
		return app.ChatResponse{}, err
	}
	if strings.TrimSpace(response.Model) == "" {
		response.Model = body.Model
	}
	usage := &app.ChatUsage{
		PromptTokens:     response.PromptEvalCount,
		CompletionTokens: response.EvalCount,
		TotalTokens:      response.PromptEvalCount + response.EvalCount,
	}
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0 {
		usage = nil
	}
	return app.ChatResponse{Model: app.ProviderModelID(p.providerID, response.Model), Content: response.Message.Content, Usage: usage}, nil
}
