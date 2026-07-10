package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

type ChatContentPart struct {
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
}

type ChatMessageContent struct {
	Text  string
	Parts []ChatContentPart
	Raw   json.RawMessage
}

func (c *ChatMessageContent) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	c.Raw = append(c.Raw[:0], data...)
	if trimmed == "" || trimmed == "null" {
		c.Text = ""
		c.Parts = nil
		return nil
	}
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		c.Text = asString
		c.Parts = nil
		return nil
	}
	var asParts []ChatContentPart
	if err := json.Unmarshal(data, &asParts); err == nil {
		c.Text = ""
		c.Parts = asParts
		return nil
	}
	return fmt.Errorf("unsupported message content format")
}

func (c ChatMessageContent) MarshalJSON() ([]byte, error) {
	if len(c.Raw) > 0 {
		return c.Raw, nil
	}
	if len(c.Parts) > 0 {
		return json.Marshal(c.Parts)
	}
	return json.Marshal(c.Text)
}

func (c ChatMessageContent) TextValue() string {
	if c.Text != "" {
		return c.Text
	}
	if len(c.Parts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(c.Parts))
	for _, part := range c.Parts {
		if part.Text == "" {
			continue
		}
		parts = append(parts, part.Text)
	}
	return strings.Join(parts, "\n")
}

type ChatMessage struct {
	Role       string             `json:"role"`
	Content    ChatMessageContent `json:"content"`
	Name       string             `json:"name,omitempty"`
	ToolCallID string             `json:"tool_call_id,omitempty"`
}

type ChatFunctionDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

type ChatTool struct {
	Type     string                  `json:"type"`
	Function *ChatFunctionDefinition `json:"function,omitempty"`
}

type ChatResponseFormatJSONSchema struct {
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

type ChatResponseFormat struct {
	Type       string                        `json:"type,omitempty"`
	JSONSchema *ChatResponseFormatJSONSchema `json:"json_schema,omitempty"`
}

type ChatStopSequences struct {
	Values []string
}

func (s *ChatStopSequences) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		s.Values = nil
		return nil
	}
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		if strings.TrimSpace(single) == "" {
			s.Values = nil
			return nil
		}
		s.Values = []string{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err == nil {
		s.Values = many
		return nil
	}
	return fmt.Errorf("unsupported stop format")
}

func (s ChatStopSequences) MarshalJSON() ([]byte, error) {
	if len(s.Values) == 0 {
		return []byte("null"), nil
	}
	if len(s.Values) == 1 {
		return json.Marshal(s.Values[0])
	}
	return json.Marshal(s.Values)
}

type ChatRequest struct {
	Model                string                     `json:"model"`
	Prompt               string                     `json:"prompt"`
	Stream               bool                       `json:"stream"`
	Messages             []ChatMessage              `json:"messages"`
	Temperature          *float32                   `json:"temperature,omitempty"`
	TopP                 *float32                   `json:"top_p,omitempty"`
	MaxTokens            *int                       `json:"max_tokens,omitempty"`
	MaxCompletionTokens  *int                       `json:"max_completion_tokens,omitempty"`
	Stop                 ChatStopSequences          `json:"stop,omitempty"`
	Tools                []ChatTool                 `json:"tools,omitempty"`
	ToolChoice           json.RawMessage            `json:"tool_choice,omitempty"`
	ResponseFormat       *ChatResponseFormat        `json:"response_format,omitempty"`
	ParallelToolCalls    *bool                      `json:"parallel_tool_calls,omitempty"`
	EstimatedInputTokens *int                       `json:"-"`
	RawFields            map[string]json.RawMessage `json:"-"`
}

func (r *ChatRequest) UnmarshalJSON(data []byte) error {
	type alias struct {
		Model               string              `json:"model"`
		Prompt              string              `json:"prompt"`
		Stream              bool                `json:"stream"`
		Messages            []ChatMessage       `json:"messages"`
		Temperature         *float32            `json:"temperature,omitempty"`
		TopP                *float32            `json:"top_p,omitempty"`
		MaxTokens           *int                `json:"max_tokens,omitempty"`
		MaxCompletionTokens *int                `json:"max_completion_tokens,omitempty"`
		Stop                ChatStopSequences   `json:"stop,omitempty"`
		Tools               []ChatTool          `json:"tools,omitempty"`
		ToolChoice          json.RawMessage     `json:"tool_choice,omitempty"`
		ResponseFormat      *ChatResponseFormat `json:"response_format,omitempty"`
		ParallelToolCalls   *bool               `json:"parallel_tool_calls,omitempty"`
	}
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.Model = decoded.Model
	r.Prompt = decoded.Prompt
	r.Stream = decoded.Stream
	r.Messages = decoded.Messages
	r.Temperature = decoded.Temperature
	r.TopP = decoded.TopP
	r.MaxTokens = decoded.MaxTokens
	r.MaxCompletionTokens = decoded.MaxCompletionTokens
	r.Stop = decoded.Stop
	r.Tools = decoded.Tools
	r.ToolChoice = decoded.ToolChoice
	r.ResponseFormat = decoded.ResponseFormat
	r.ParallelToolCalls = decoded.ParallelToolCalls
	r.RawFields = raw
	return nil
}

func (r ChatRequest) ForwardedOpenAIFields(model string, stream bool) (map[string]json.RawMessage, error) {
	if len(r.RawFields) == 0 {
		out := map[string]json.RawMessage{}
		messages, err := json.Marshal(r.defaultOpenAIMessages())
		if err != nil {
			return nil, err
		}
		modelValue, err := json.Marshal(model)
		if err != nil {
			return nil, err
		}
		streamValue, err := json.Marshal(stream)
		if err != nil {
			return nil, err
		}
		out["model"] = modelValue
		out["messages"] = messages
		out["stream"] = streamValue
		if r.Temperature != nil {
			value, err := json.Marshal(*r.Temperature)
			if err != nil {
				return nil, err
			}
			out["temperature"] = value
		}
		if r.TopP != nil {
			value, err := json.Marshal(*r.TopP)
			if err != nil {
				return nil, err
			}
			out["top_p"] = value
		}
		if r.MaxCompletionTokens != nil {
			value, err := json.Marshal(*r.MaxCompletionTokens)
			if err != nil {
				return nil, err
			}
			out["max_completion_tokens"] = value
		} else if r.MaxTokens != nil {
			value, err := json.Marshal(*r.MaxTokens)
			if err != nil {
				return nil, err
			}
			out["max_tokens"] = value
		}
		if len(r.Stop.Values) > 0 {
			value, err := json.Marshal(r.Stop)
			if err != nil {
				return nil, err
			}
			out["stop"] = value
		}
		if len(r.Tools) > 0 {
			value, err := json.Marshal(r.Tools)
			if err != nil {
				return nil, err
			}
			out["tools"] = value
		}
		if len(r.ToolChoice) > 0 {
			choice := make(json.RawMessage, len(r.ToolChoice))
			copy(choice, r.ToolChoice)
			out["tool_choice"] = choice
		}
		if r.ResponseFormat != nil {
			value, err := json.Marshal(r.ResponseFormat)
			if err != nil {
				return nil, err
			}
			out["response_format"] = value
		}
		if r.ParallelToolCalls != nil {
			value, err := json.Marshal(*r.ParallelToolCalls)
			if err != nil {
				return nil, err
			}
			out["parallel_tool_calls"] = value
		}
		return out, nil
	}
	out := make(map[string]json.RawMessage, len(r.RawFields)+1)
	for key, value := range r.RawFields {
		copied := make(json.RawMessage, len(value))
		copy(copied, value)
		out[key] = copied
	}
	modelValue, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	streamValue, err := json.Marshal(stream)
	if err != nil {
		return nil, err
	}
	out["model"] = modelValue
	out["stream"] = streamValue
	if _, ok := out["messages"]; !ok && len(r.Messages) > 0 {
		messages, err := json.Marshal(r.Messages)
		if err != nil {
			return nil, err
		}
		out["messages"] = messages
	}
	return out, nil
}

func (r ChatRequest) defaultOpenAIMessages() []map[string]any {
	messages := make([]map[string]any, 0, len(r.Messages))
	for _, msg := range r.Messages {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "user"
		}
		messages = append(messages, map[string]any{
			"role":    role,
			"content": msg.Content,
		})
	}
	if len(messages) == 0 {
		messages = append(messages, map[string]any{
			"role":    "user",
			"content": r.Prompt,
		})
	}
	return messages
}

type ChatResponse struct {
	Model      string
	Completion *OpenAIChatCompletionResponse
	Usage      *ChatUsage
}

type ChatUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type OpenAIChatToolFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type OpenAIChatToolCall struct {
	ID       string                 `json:"id,omitempty"`
	Type     string                 `json:"type,omitempty"`
	Function OpenAIChatToolFunction `json:"function"`
}

type OpenAIChatMessage struct {
	Role      string               `json:"role,omitempty"`
	Content   ChatMessageContent   `json:"content,omitempty"`
	ToolCalls []OpenAIChatToolCall `json:"tool_calls,omitempty"`
}

type OpenAIChatChoice struct {
	Index        int               `json:"index"`
	Message      OpenAIChatMessage `json:"message"`
	FinishReason string            `json:"finish_reason,omitempty"`
}

type OpenAIChatCompletionUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type OpenAIChatCompletionResponse struct {
	ID      string                     `json:"id,omitempty"`
	Object  string                     `json:"object,omitempty"`
	Created int64                      `json:"created,omitempty"`
	Model   string                     `json:"model,omitempty"`
	Choices []OpenAIChatChoice         `json:"choices,omitempty"`
	Usage   *OpenAIChatCompletionUsage `json:"usage,omitempty"`
}

type OpenAIChatDelta struct {
	Role      string               `json:"role,omitempty"`
	Content   string               `json:"content,omitempty"`
	ToolCalls []OpenAIChatToolCall `json:"tool_calls,omitempty"`
}

type OpenAIChatChunkChoice struct {
	Index        int             `json:"index"`
	Delta        OpenAIChatDelta `json:"delta"`
	FinishReason any             `json:"finish_reason"`
}

type OpenAIChatCompletionChunk struct {
	ID      string                  `json:"id,omitempty"`
	Object  string                  `json:"object,omitempty"`
	Created int64                   `json:"created,omitempty"`
	Model   string                  `json:"model,omitempty"`
	Choices []OpenAIChatChunkChoice `json:"choices,omitempty"`
}

func (r ChatResponse) AssistantText() string {
	if r.Completion == nil || len(r.Completion.Choices) == 0 {
		return ""
	}
	return r.Completion.Choices[0].Message.Content.TextValue()
}

func NewTextCompletion(model, content string) *OpenAIChatCompletionResponse {
	return &OpenAIChatCompletionResponse{
		ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []OpenAIChatChoice{{
			Index: 0,
			Message: OpenAIChatMessage{
				Role:    "assistant",
				Content: ChatMessageContent{Text: content},
			},
			FinishReason: "stop",
		}},
	}
}

type ChatStreamResponse struct {
	Body        io.ReadCloser
	ContentType string
	Model       string
}

type Provider interface {
	ID() string
	Models(ctx context.Context) ([]Model, error)
	ChatCompletions(ctx context.Context, req ChatRequest) (ChatResponse, error)
	CanHandle(modelID string) bool
}

type StreamingProvider interface {
	ChatCompletionsStream(ctx context.Context, req ChatRequest) (ChatStreamResponse, error)
}

type ProviderRegistry struct {
	providers []Provider
}

func NewProviderRegistry(providers ...Provider) *ProviderRegistry {
	filtered := make([]Provider, 0, len(providers))
	for _, provider := range providers {
		if provider != nil {
			filtered = append(filtered, provider)
		}
	}
	return &ProviderRegistry{providers: filtered}
}

func (r *ProviderRegistry) Providers() []Provider {
	out := make([]Provider, len(r.providers))
	copy(out, r.providers)
	return out
}

func (r *ProviderRegistry) Models(ctx context.Context) ([]Model, error) {
	models := make([]Model, 0)
	for _, provider := range r.providers {
		providerModels, err := provider.Models(ctx)
		if err != nil {
			return nil, err
		}
		models = append(models, providerModels...)
	}
	return models, nil
}

func (r *ProviderRegistry) Resolve(modelID string) (Provider, error) {
	for _, provider := range r.providers {
		if provider.CanHandle(modelID) {
			return provider, nil
		}
	}
	return nil, errors.New("no provider can handle model")
}

func ProviderModelID(providerID, modelID string) string {
	return strings.TrimSpace(providerID) + ":" + strings.TrimSpace(modelID)
}

type MockProvider struct {
	providerID string
	models     []Model
	responses  map[string]string
	failures   map[string]string
}

func NewMockProvider(providerID string, modelIDs []string, responses map[string]string) *MockProvider {
	return NewMockProviderWithFailures(providerID, modelIDs, responses, nil)
}

func NewMockProviderWithFailures(providerID string, modelIDs []string, responses map[string]string, failures map[string]string) *MockProvider {
	models := make([]Model, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		models = append(models, Model{
			ID:       ProviderModelID(providerID, modelID),
			Object:   "model",
			OwnedBy:  providerID,
			Provider: providerID,
		})
	}
	responseMap := make(map[string]string, len(responses))
	for key, value := range responses {
		responseMap[strings.TrimSpace(key)] = value
	}
	failureMap := make(map[string]string, len(failures))
	for key, value := range failures {
		failureMap[strings.TrimSpace(key)] = value
	}
	return &MockProvider{providerID: providerID, models: models, responses: responseMap, failures: failureMap}
}

func (p *MockProvider) ID() string { return p.providerID }

func (p *MockProvider) Models(_ context.Context) ([]Model, error) {
	out := make([]Model, len(p.models))
	copy(out, p.models)
	return out, nil
}

func (p *MockProvider) CanHandle(modelID string) bool {
	return strings.HasPrefix(modelID, p.providerID+":")
}

func (p *MockProvider) ChatCompletions(_ context.Context, req ChatRequest) (ChatResponse, error) {
	if req.Model == "" {
		return ChatResponse{}, fmt.Errorf("model is required")
	}
	if message, ok := p.failures[strings.TrimSpace(req.Model)]; ok {
		return ChatResponse{}, errors.New(message)
	}
	if content, ok := p.responses[strings.TrimSpace(req.Model)]; ok {
		return ChatResponse{Model: req.Model, Completion: NewTextCompletion(req.Model, content)}, nil
	}
	prompt := req.Prompt
	if prompt == "" && len(req.Messages) > 0 {
		prompt = req.Messages[len(req.Messages)-1].Content.TextValue()
	}
	content := fmt.Sprintf("mock:%s:%s", req.Model, prompt)
	return ChatResponse{Model: req.Model, Completion: NewTextCompletion(req.Model, content)}, nil
}
