package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"

	"github.com/ARMmaster17/minirouter/internal/app"
)

type GeminiProvider struct {
	providerID string
	baseURL    string
	apiKey     string
	configured []app.Model
	client     *genai.Client
	clientErr  error
	mu         sync.RWMutex
	cached     []app.Model
}

func NewGeminiProvider(providerID, baseURL, apiKey string, configured []app.Model) *GeminiProvider {
	url := normalizeGeminiBaseURL(baseURL)
	if url == "" {
		url = "https://generativelanguage.googleapis.com/"
	}
	normalizedConfigured := normalizeGeminiConfiguredModels(providerID, configured)
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  strings.TrimSpace(apiKey),
		Backend: genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{
			BaseURL: url,
		},
	})
	return &GeminiProvider{
		providerID: providerID,
		baseURL:    strings.TrimRight(url, "/"),
		apiKey:     strings.TrimSpace(apiKey),
		configured: normalizedConfigured,
		client:     client,
		clientErr:  err,
	}
}

func (p *GeminiProvider) ID() string { return p.providerID }

func (p *GeminiProvider) CanHandle(modelID string) bool {
	return strings.HasPrefix(modelID, p.providerID+":")
}

func (p *GeminiProvider) Models(ctx context.Context) ([]app.Model, error) {
	if p.clientErr != nil {
		return nil, p.clientErr
	}

	p.mu.RLock()
	if len(p.cached) > 0 {
		cached := make([]app.Model, len(p.cached))
		copy(cached, p.cached)
		p.mu.RUnlock()
		return cached, nil
	}
	p.mu.RUnlock()

	page, err := p.client.Models.List(ctx, &genai.ListModelsConfig{PageSize: 1000})
	if err != nil {
		if len(p.configured) > 0 {
			return nil, fmt.Errorf("hydrate configured models for %s: %w", p.providerID, err)
		}
		return nil, err
	}

	models := make([]app.Model, 0)
	for {
		for _, model := range page.Items {
			if model == nil {
				continue
			}
			modelID := normalizeGeminiModelID(model.Name)
			if strings.TrimSpace(modelID) == "" {
				continue
			}
			var contextLimit *int
			if model.InputTokenLimit > 0 {
				value := int(model.InputTokenLimit)
				contextLimit = &value
			}
			models = append(models, app.Model{
				ID:           app.ProviderModelID(p.providerID, modelID),
				Object:       "model",
				OwnedBy:      p.providerID,
				Provider:     p.providerID,
				ContextLimit: contextLimit,
			})
		}
		nextPage, pageErr := page.Next(ctx)
		if pageErr != nil {
			if pageErr == genai.ErrPageDone {
				break
			}
			return nil, pageErr
		}
		page = nextPage
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
	if p.clientErr != nil {
		return app.ChatResponse{}, p.clientErr
	}

	model := normalizeGeminiModelID(splitModelID(p.providerID, req.Model))
	contents, config := geminiRequestToContent(req)
	response, err := p.client.Models.GenerateContent(ctx, model, contents, config)
	if err != nil {
		return app.ChatResponse{}, err
	}

	providerModel := app.ProviderModelID(p.providerID, model)
	openAIResponse := geminiResponseToOpenAI(providerModel, response)

	usage := geminiUsageToChatUsage(response)
	return app.ChatResponse{Model: providerModel, Completion: &openAIResponse, Usage: usage}, nil
}

func (p *GeminiProvider) ChatCompletionsStream(ctx context.Context, req app.ChatRequest) (app.ChatStreamResponse, error) {
	if p.clientErr != nil {
		return app.ChatStreamResponse{}, p.clientErr
	}

	model := normalizeGeminiModelID(splitModelID(p.providerID, req.Model))
	providerModel := app.ProviderModelID(p.providerID, model)
	contents, config := geminiRequestToContent(req)
	stream := p.client.Models.GenerateContentStream(ctx, model, contents, config)

	reader, writer := io.Pipe()
	go func() {
		defer writer.Close()

		created := time.Now().Unix()
		id := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
		wroteRole := false
		for chunk, err := range stream {
			if err != nil {
				_ = writeSSE(writer, map[string]any{"error": map[string]any{"message": err.Error(), "type": "invalid_request_error"}})
				_, _ = io.WriteString(writer, "data: [DONE]\n\n")
				return
			}
			if chunk == nil {
				continue
			}
			if !wroteRole {
				roleChunk := app.OpenAIChatCompletionChunk{
					ID:      id,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   providerModel,
					Choices: []app.OpenAIChatChunkChoice{{
						Index:        0,
						Delta:        app.OpenAIChatDelta{Role: "assistant"},
						FinishReason: nil,
					}},
				}
				if err := writeSSE(writer, roleChunk); err != nil {
					return
				}
				wroteRole = true
			}

			if len(chunk.Candidates) == 0 || chunk.Candidates[0] == nil {
				continue
			}
			candidate := chunk.Candidates[0]
			if candidate.Content == nil {
				continue
			}
			delta := app.OpenAIChatDelta{}
			parts := []app.OpenAIChatToolCall{}
			toolCallIndex := 0
			for _, part := range candidate.Content.Parts {
				if part == nil {
					continue
				}
				if strings.TrimSpace(part.Text) != "" {
					textChunk := app.OpenAIChatCompletionChunk{
						ID:      id,
						Object:  "chat.completion.chunk",
						Created: created,
						Model:   providerModel,
						Choices: []app.OpenAIChatChunkChoice{{
							Index:        0,
							Delta:        app.OpenAIChatDelta{Content: part.Text},
							FinishReason: nil,
						}},
					}
					if err := writeSSE(writer, textChunk); err != nil {
						return
					}
				}
				if part.FunctionCall != nil {
					arguments, _ := json.Marshal(part.FunctionCall.Args)
					idx := toolCallIndex
					parts = append(parts, app.OpenAIChatToolCall{
						Index: &idx,
						ID:   part.FunctionCall.ID,
						Type: "function",
						Function: app.OpenAIChatToolFunction{
							Name:      part.FunctionCall.Name,
							Arguments: string(arguments),
						},
					})
					toolCallIndex++
				}
			}
			if len(parts) > 0 {
				delta.ToolCalls = parts
			}
			if len(delta.ToolCalls) > 0 {
				toolChunk := app.OpenAIChatCompletionChunk{
					ID:      id,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   providerModel,
					Choices: []app.OpenAIChatChunkChoice{{
						Index:        0,
						Delta:        delta,
						FinishReason: nil,
					}},
				}
				if err := writeSSE(writer, toolChunk); err != nil {
					return
				}
			}

			finishReason := geminiFinishReasonToOpenAI(candidate.FinishReason)
			if finishReason == "" {
				continue
			}
			stopChunk := app.OpenAIChatCompletionChunk{
				ID:      id,
				Object:  "chat.completion.chunk",
				Created: created,
				Model:   providerModel,
				Choices: []app.OpenAIChatChunkChoice{{
					Index:        0,
					Delta:        app.OpenAIChatDelta{},
					FinishReason: finishReason,
				}},
			}
			if err := writeSSE(writer, stopChunk); err != nil {
				return
			}
		}
		_, _ = io.WriteString(writer, "data: [DONE]\n\n")
	}()

	return app.ChatStreamResponse{
		Body:        reader,
		ContentType: "text/event-stream",
		Model:       providerModel,
	}, nil
}

func normalizeGeminiBaseURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.TrimRight(trimmed, "/")
	trimmed = strings.TrimSuffix(trimmed, "/v1beta/openai")
	trimmed = strings.TrimSuffix(trimmed, "/v1/openai")
	trimmed = strings.TrimSuffix(trimmed, "/openai")
	trimmed = strings.TrimSuffix(trimmed, "/v1beta")
	trimmed = strings.TrimSuffix(trimmed, "/v1")
	if !strings.HasSuffix(trimmed, "/") {
		trimmed += "/"
	}
	return trimmed
}

func normalizeGeminiModelID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "models/") || strings.Contains(trimmed, "/") {
		return trimmed
	}
	return "models/" + trimmed
}

func normalizeGeminiConfiguredModels(providerID string, configured []app.Model) []app.Model {
	if len(configured) == 0 {
		return configured
	}
	out := make([]app.Model, 0, len(configured))
	for _, model := range configured {
		normalized := model
		suffix := splitModelID(providerID, model.ID)
		normalized.ID = app.ProviderModelID(providerID, normalizeGeminiModelID(suffix))
		out = append(out, normalized)
	}
	return out
}

func geminiRequestToContent(req app.ChatRequest) ([]*genai.Content, *genai.GenerateContentConfig) {
	contents := make([]*genai.Content, 0, len(req.Messages))
	for _, msg := range req.Messages {
		role, marker := normalizeGeminiInboundRole(msg.Role)
		if strings.EqualFold(strings.TrimSpace(msg.Role), "tool") && strings.TrimSpace(msg.ToolCallID) != "" {
			marker = "[tool_result tool_call_id=" + strings.TrimSpace(msg.ToolCallID) + "]"
		}
		text := strings.TrimSpace(msg.Content.TextValue())
		if marker != "" {
			if text == "" {
				text = marker
			} else {
				text = marker + "\n" + text
			}
		}
		parts := make([]*genai.Part, 0, 1)
		if text != "" {
			parts = append(parts, genai.NewPartFromText(text))
		}
		if len(parts) == 0 {
			continue
		}
		contents = append(contents, &genai.Content{Role: role, Parts: parts})
	}
	if len(contents) == 0 {
		prompt := strings.TrimSpace(extractPrompt(req))
		if prompt != "" {
			contents = append(contents, genai.NewContentFromText(prompt, genai.RoleUser))
		}
	}
	config := &genai.GenerateContentConfig{}
	if req.Temperature != nil {
		value := *req.Temperature
		config.Temperature = &value
	}
	if req.TopP != nil {
		value := *req.TopP
		config.TopP = &value
	}
	if req.MaxCompletionTokens != nil {
		config.MaxOutputTokens = int32(*req.MaxCompletionTokens)
	} else if req.MaxTokens != nil {
		config.MaxOutputTokens = int32(*req.MaxTokens)
	}
	if len(req.Stop.Values) > 0 {
		config.StopSequences = append([]string(nil), req.Stop.Values...)
	}
	applyGeminiTools(config, req)
	applyGeminiResponseFormat(config, req)
	return contents, config
}

func normalizeGeminiInboundRole(rawRole string) (string, string) {
	role := strings.ToLower(strings.TrimSpace(rawRole))
	switch role {
	case "assistant", string(genai.RoleModel):
		return string(genai.RoleModel), ""
	case "", "user":
		return string(genai.RoleUser), ""
	case "tool":
		return string(genai.RoleUser), "[tool_result]"
	case "system", "system_1", "developer", "context", "user_context":
		return string(genai.RoleUser), "[" + role + "]"
	default:
		return string(genai.RoleUser), "[role:" + role + "]"
	}
}

func applyGeminiTools(config *genai.GenerateContentConfig, req app.ChatRequest) {
	if len(req.Tools) == 0 {
		return
	}
	declarations := make([]*genai.FunctionDeclaration, 0)
	for _, tool := range req.Tools {
		if strings.ToLower(strings.TrimSpace(tool.Type)) != "function" || tool.Function == nil {
			continue
		}
		declaration := &genai.FunctionDeclaration{
			Name:        strings.TrimSpace(tool.Function.Name),
			Description: strings.TrimSpace(tool.Function.Description),
		}
		if len(tool.Function.Parameters) > 0 {
			var schema any
			if err := json.Unmarshal(tool.Function.Parameters, &schema); err == nil {
				declaration.ParametersJsonSchema = schema
			}
		}
		if declaration.Name == "" {
			continue
		}
		declarations = append(declarations, declaration)
	}
	if len(declarations) == 0 {
		return
	}
	config.Tools = []*genai.Tool{{FunctionDeclarations: declarations}}

	mode, allowed := geminiFunctionCallingFromToolChoice(req.ToolChoice, declarations)
	if mode == "" {
		return
	}
	config.ToolConfig = &genai.ToolConfig{
		FunctionCallingConfig: &genai.FunctionCallingConfig{
			Mode:                 mode,
			AllowedFunctionNames: allowed,
		},
	}
}

func geminiFunctionCallingFromToolChoice(raw json.RawMessage, declarations []*genai.FunctionDeclaration) (genai.FunctionCallingConfigMode, []string) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return genai.FunctionCallingConfigModeAuto, nil
	}
	var stringChoice string
	if err := json.Unmarshal(raw, &stringChoice); err == nil {
		switch strings.ToLower(strings.TrimSpace(stringChoice)) {
		case "none":
			return genai.FunctionCallingConfigModeNone, nil
		case "required":
			return genai.FunctionCallingConfigModeAny, functionDeclarationNames(declarations)
		default:
			return genai.FunctionCallingConfigModeAuto, nil
		}
	}
	var objectChoice struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &objectChoice); err != nil {
		return genai.FunctionCallingConfigModeAuto, nil
	}
	if strings.ToLower(strings.TrimSpace(objectChoice.Type)) == "function" && strings.TrimSpace(objectChoice.Function.Name) != "" {
		return genai.FunctionCallingConfigModeAny, []string{strings.TrimSpace(objectChoice.Function.Name)}
	}
	return genai.FunctionCallingConfigModeAuto, nil
}

func functionDeclarationNames(declarations []*genai.FunctionDeclaration) []string {
	names := make([]string, 0, len(declarations))
	for _, declaration := range declarations {
		if declaration == nil || strings.TrimSpace(declaration.Name) == "" {
			continue
		}
		names = append(names, declaration.Name)
	}
	return names
}

func applyGeminiResponseFormat(config *genai.GenerateContentConfig, req app.ChatRequest) {
	if req.ResponseFormat == nil {
		return
	}
	formatType := strings.ToLower(strings.TrimSpace(req.ResponseFormat.Type))
	switch formatType {
	case "json_object":
		config.ResponseMIMEType = "application/json"
	case "json_schema":
		config.ResponseMIMEType = "application/json"
		if req.ResponseFormat.JSONSchema != nil && len(req.ResponseFormat.JSONSchema.Schema) > 0 {
			var schema any
			if err := json.Unmarshal(req.ResponseFormat.JSONSchema.Schema, &schema); err == nil {
				config.ResponseJsonSchema = schema
			}
		}
	}
}

func geminiResponseToOpenAI(model string, response *genai.GenerateContentResponse) app.OpenAIChatCompletionResponse {
	choice := app.OpenAIChatChoice{Index: 0, Message: app.OpenAIChatMessage{Role: "assistant"}}
	if response != nil && len(response.Candidates) > 0 && response.Candidates[0] != nil {
		candidate := response.Candidates[0]
		if candidate.Content != nil {
			partsText := make([]string, 0)
			toolCalls := make([]app.OpenAIChatToolCall, 0)
			toolCallIndex := 0
			for _, part := range candidate.Content.Parts {
				if part == nil {
					continue
				}
				if strings.TrimSpace(part.Text) != "" {
					partsText = append(partsText, part.Text)
				}
				if part.FunctionCall != nil {
					arguments, _ := json.Marshal(part.FunctionCall.Args)
					idx := toolCallIndex
					toolCall := app.OpenAIChatToolCall{
						Index: &idx,
						ID:   part.FunctionCall.ID,
						Type: "function",
						Function: app.OpenAIChatToolFunction{
							Name:      part.FunctionCall.Name,
							Arguments: string(arguments),
						},
					}
					toolCalls = append(toolCalls, toolCall)
					toolCallIndex++
				}
			}
			if len(partsText) > 0 {
				choice.Message.Content = app.ChatMessageContent{Text: strings.Join(partsText, "\n")}
			}
			if len(toolCalls) > 0 {
				choice.Message.ToolCalls = toolCalls
			}
		}
		choice.FinishReason = geminiFinishReasonToOpenAI(candidate.FinishReason)
	}
	out := app.OpenAIChatCompletionResponse{
		ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []app.OpenAIChatChoice{choice},
	}
	if usage := geminiUsageToOpenAIUsage(response); usage != nil {
		out.Usage = usage
	}
	return out
}

func geminiFinishReasonToOpenAI(reason genai.FinishReason) string {
	switch reason {
	case genai.FinishReasonStop:
		return "stop"
	case genai.FinishReasonMaxTokens:
		return "length"
	case genai.FinishReasonMalformedFunctionCall:
		return "tool_calls"
	default:
		if strings.Contains(strings.ToUpper(string(reason)), "FUNCTION") {
			return "tool_calls"
		}
		if strings.TrimSpace(string(reason)) != "" && reason != genai.FinishReasonUnspecified {
			return "stop"
		}
		return ""
	}
}

func geminiUsageToOpenAIUsage(response *genai.GenerateContentResponse) *app.OpenAIChatCompletionUsage {
	usage := geminiUsageToChatUsage(response)
	if usage == nil {
		return nil
	}
	return &app.OpenAIChatCompletionUsage{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
	}
}

func geminiUsageToChatUsage(response *genai.GenerateContentResponse) *app.ChatUsage {
	if response == nil || response.UsageMetadata == nil {
		return nil
	}
	prompt := int(response.UsageMetadata.PromptTokenCount)
	completion := int(response.UsageMetadata.CandidatesTokenCount)
	total := int(response.UsageMetadata.TotalTokenCount)
	if total == 0 {
		total = prompt + completion
	}
	if prompt == 0 && completion == 0 && total == 0 {
		return nil
	}
	return &app.ChatUsage{PromptTokens: prompt, CompletionTokens: completion, TotalTokens: total}
}

func writeSSE(writer io.Writer, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "data: %s\n\n", encoded)
	return err
}

var _ app.Provider = (*GeminiProvider)(nil)
var _ app.StreamingProvider = (*GeminiProvider)(nil)
