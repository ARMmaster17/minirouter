package providers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/ARMmaster17/minirouter/internal/app"
	"google.golang.org/genai"
)

type GeminiProvider struct {
	providerID string
	baseURL    string
	apiKey     string
	configured []app.Model
	client     *genai.Client
	mu         sync.RWMutex
	cached     []app.Model
}

func NewGeminiProvider(providerID, baseURL, apiKey string, configured []app.Model) *GeminiProvider {
	url := strings.TrimSpace(baseURL)
	if url == "" {
		url = "https://generativelanguage.googleapis.com/v1beta"
	}
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create Gemini API client: %v", err))
	}
	return &GeminiProvider{
		providerID: providerID,
		baseURL:    strings.TrimRight(url, "/"),
		apiKey:     strings.TrimSpace(apiKey),
		configured: configured,
		client:     client,
	}
}

func (p *GeminiProvider) ID() string { return p.providerID }

func (p *GeminiProvider) CanHandle(modelID string) bool {
	return strings.HasPrefix(modelID, p.providerID+":")
}

func (p *GeminiProvider) Models(ctx context.Context) ([]app.Model, error) {
	type geminiListResponse struct {
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

	endpoint := joinURL(p.baseURL, "models")
	if p.apiKey != "" {
		endpoint = addQueryParam(endpoint, "key", p.apiKey)
	}
	var response geminiListResponse
	err := doJSON(ctx, p.client, http.MethodGet, endpoint, "", nil, nil, &response)
	if err != nil {
		if len(p.configured) > 0 {
			return nil, fmt.Errorf("hydrate configured models for %s: %w", p.providerID, err)
		}
		return nil, err
	}
	models := make([]app.Model, 0, len(response.Models))
	for _, model := range response.Models {
		name, _ := model["name"].(string)
		name = strings.TrimPrefix(strings.TrimSpace(name), "models/")
		if name == "" {
			continue
		}
		inputLimit := readIntField(model, "inputTokenLimit", "input_token_limit", "context_limit")
		outputLimit := readIntField(model, "outputTokenLimit", "output_token_limit")
		contextLimit := inputLimit
		if contextLimit == nil {
			contextLimit = outputLimit
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

func (p *GeminiProvider) ChatCompletions(ctx context.Context, req app.ChatRequest) (app.ChatResponse, error) {
	type geminiPart struct {
		Text string `json:"text"`
	}
	type geminiContent struct {
		Role  string       `json:"role,omitempty"`
		Parts []geminiPart `json:"parts"`
	}
	type geminiRequest struct {
		Contents []geminiContent `json:"contents"`
	}
	type geminiResponse struct {
		Candidates []struct {
			Content geminiContent `json:"content"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}

	contents := make([]geminiContent, 0, len(req.Messages))
	for _, message := range req.Messages {
		role := strings.TrimSpace(message.Role)
		if role == "assistant" {
			role = "model"
		}
		if role == "" {
			role = "user"
		}
		contents = append(contents, geminiContent{Role: role, Parts: []geminiPart{{Text: message.Content.TextValue()}}})
	}
	if len(contents) == 0 {
		contents = append(contents, geminiContent{Role: "user", Parts: []geminiPart{{Text: extractPrompt(req)}}})
	}

	model := splitModelID(p.providerID, req.Model)
	endpoint := joinURL(p.baseURL, "models/"+model+":generateContent")
	if p.apiKey != "" {
		endpoint = addQueryParam(endpoint, "key", p.apiKey)
	}
	var response geminiResponse
	if err := doJSON(ctx, p.client, http.MethodPost, endpoint, "", geminiRequest{Contents: contents}, nil, &response); err != nil {
		return app.ChatResponse{}, err
	}
	content := ""
	if len(response.Candidates) > 0 && len(response.Candidates[0].Content.Parts) > 0 {
		content = response.Candidates[0].Content.Parts[0].Text
	}
	usage := &app.ChatUsage{
		PromptTokens:     response.UsageMetadata.PromptTokenCount,
		CompletionTokens: response.UsageMetadata.CandidatesTokenCount,
		TotalTokens:      response.UsageMetadata.TotalTokenCount,
	}
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0 {
		usage = nil
	}
	return app.ChatResponse{Model: app.ProviderModelID(p.providerID, model), Content: content, Usage: usage}, nil
}

func (p *GeminiProvider) ChatCompletionsStream(ctx context.Context, req app.ChatRequest) (app.ChatStreamResponse, error) {
	chat, err := p.client.Chats.Create(ctx, splitModelID(p.providerID, req.Model), nil, genai.WithChatMessages(toGeminiMessages(req)))
}

// Takes a request and converts it to the Gemini API's expected message format. The most recent user message returned seperately as the prompt.
func (p *GeminiProvider) getHistoryFromRequest(req app.ChatRequest) ([]*genai.Content, *genai.Content, error) {
	var prompt *genai.Content
	history := make([]*genai.Content, 0, len(req.Messages))
	for _, msg := range req.Messages {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "user"
		} else if role == "assistant" {
			role = "model"
		}
		content := &genai.Content{
			Parts: []genai.Part{
				{Text: msg.Content.TextValue()},
			},
			Role: role,
		}

	}
}
