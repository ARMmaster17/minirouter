package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/ARMmaster17/minirouter/internal/app"
)

type OpenAIProvider struct {
	providerID string
	baseURL    string
	apiKey     string
	configured []app.Model
	client     *http.Client
	mu         sync.RWMutex
	cached     []app.Model
}

func NewOpenAIProvider(providerID, baseURL, apiKey string, configured []app.Model) *OpenAIProvider {
	url := strings.TrimSpace(baseURL)
	if url == "" {
		url = "https://api.openai.com/v1"
	}
	return &OpenAIProvider{
		providerID: providerID,
		baseURL:    strings.TrimRight(url, "/"),
		apiKey:     strings.TrimSpace(apiKey),
		configured: configured,
		client:     defaultClient(),
	}
}

func (p *OpenAIProvider) ID() string { return p.providerID }

func (p *OpenAIProvider) CanHandle(modelID string) bool {
	return strings.HasPrefix(modelID, p.providerID+":")
}

func (p *OpenAIProvider) Models(ctx context.Context) ([]app.Model, error) {
	type modelListResponse struct {
		Data []map[string]any `json:"data"`
	}
	p.mu.RLock()
	if len(p.cached) > 0 {
		cached := make([]app.Model, len(p.cached))
		copy(cached, p.cached)
		p.mu.RUnlock()
		return cached, nil
	}
	p.mu.RUnlock()

	endpoint := joinURL(p.baseURL, "models")
	var response modelListResponse
	err := doJSON(ctx, p.client, http.MethodGet, endpoint, p.apiKey, nil, nil, &response)
	if err != nil {
		if len(p.configured) > 0 {
			return nil, fmt.Errorf("hydrate configured models for %s: %w", p.providerID, err)
		}
		return nil, err
	}
	models := make([]app.Model, 0, len(response.Data))
	for _, model := range response.Data {
		modelID, _ := model["id"].(string)
		if strings.TrimSpace(modelID) == "" {
			continue
		}
		inputCost := readCostField(model, "input_cost_per_million", "input_price_per_million", "prompt_price_per_million")
		if inputCost == nil {
			inputCost = readPricingMapCost(model, []string{"pricing"}, []string{"input", "prompt", "input_per_million", "prompt_per_million"})
		}
		outputCost := readCostField(model, "output_cost_per_million", "output_price_per_million", "completion_price_per_million")
		if outputCost == nil {
			outputCost = readPricingMapCost(model, []string{"pricing"}, []string{"output", "completion", "output_per_million", "completion_per_million"})
		}
		models = append(models, app.Model{
			ID:              app.ProviderModelID(p.providerID, modelID),
			Object:          "model",
			OwnedBy:         p.providerID,
			Provider:        p.providerID,
			ContextLimit:    readIntField(model, "context_length", "context_window", "contextLimit", "max_context_tokens", "max_input_tokens", "input_token_limit"),
			TokenInputCost:  inputCost,
			TokenOutputCost: outputCost,
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

func (p *OpenAIProvider) ChatCompletions(ctx context.Context, req app.ChatRequest) (app.ChatResponse, error) {
	type chatChoice struct {
		Message struct {
			Content app.ChatMessageContent `json:"content"`
		} `json:"message"`
	}
	type chatResponse struct {
		Model   string       `json:"model"`
		Choices []chatChoice `json:"choices"`
		Usage   struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	model := splitModelID(p.providerID, req.Model)
	body, err := req.ForwardedOpenAIFields(model, false)
	if err != nil {
		return app.ChatResponse{}, err
	}
	endpoint := joinURL(p.baseURL, "chat/completions")
	rawResponse, err := doJSONRaw(ctx, p.client, http.MethodPost, endpoint, p.apiKey, body, nil)
	if err != nil {
		return app.ChatResponse{}, err
	}
	var response chatResponse
	if err := json.Unmarshal(rawResponse, &response); err != nil {
		return app.ChatResponse{}, err
	}
	content := ""
	if len(response.Choices) > 0 {
		content = response.Choices[0].Message.Content.TextValue()
	}
	if strings.TrimSpace(response.Model) == "" {
		response.Model = model
	}
	usage := &app.ChatUsage{
		PromptTokens:     response.Usage.PromptTokens,
		CompletionTokens: response.Usage.CompletionTokens,
		TotalTokens:      response.Usage.TotalTokens,
	}
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0 {
		usage = nil
	}
	return app.ChatResponse{Model: app.ProviderModelID(p.providerID, response.Model), Content: content, Usage: usage, RawJSON: rawResponse}, nil
}

func (p *OpenAIProvider) ChatCompletionsStream(ctx context.Context, req app.ChatRequest) (app.ChatStreamResponse, error) {
	model := splitModelID(p.providerID, req.Model)
	body, err := req.ForwardedOpenAIFields(model, true)
	if err != nil {
		return app.ChatStreamResponse{}, err
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return app.ChatStreamResponse{}, err
	}
	endpoint := joinURL(p.baseURL, "chat/completions")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return app.ChatStreamResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	response, err := p.client.Do(request)
	if err != nil {
		return app.ChatStreamResponse{}, err
	}
	contentType := response.Header.Get("Content-Type")
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
		return app.ChatStreamResponse{}, fmt.Errorf("upstream status %d: %s", response.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}
	if !strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		_ = response.Body.Close()
		return app.ChatStreamResponse{}, fmt.Errorf("upstream stream unsupported content-type: %s", contentType)
	}

	return app.ChatStreamResponse{
		Body:        response.Body,
		ContentType: contentType,
		Model:       app.ProviderModelID(p.providerID, model),
	}, nil
}
