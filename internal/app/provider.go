package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
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

type ChatRequest struct {
	Model                string                     `json:"model"`
	Prompt               string                     `json:"prompt"`
	Stream               bool                       `json:"stream"`
	Messages             []ChatMessage              `json:"messages"`
	EstimatedInputTokens *int                       `json:"-"`
	RawFields            map[string]json.RawMessage `json:"-"`
}

func (r *ChatRequest) UnmarshalJSON(data []byte) error {
	type alias struct {
		Model    string        `json:"model"`
		Prompt   string        `json:"prompt"`
		Stream   bool          `json:"stream"`
		Messages []ChatMessage `json:"messages"`
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
	r.RawFields = raw
	return nil
}

func (r ChatRequest) ForwardedOpenAIFields(model string, stream bool) (map[string]json.RawMessage, error) {
	if len(r.RawFields) == 0 {
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
		return map[string]json.RawMessage{
			"model":    modelValue,
			"messages": messages,
			"stream":   streamValue,
		}, nil
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
	Model   string
	Content string
	Usage   *ChatUsage
	RawJSON json.RawMessage
}

type ChatUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
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
		return ChatResponse{Model: req.Model, Content: content}, nil
	}
	prompt := req.Prompt
	if prompt == "" && len(req.Messages) > 0 {
		prompt = req.Messages[len(req.Messages)-1].Content.TextValue()
	}
	return ChatResponse{Model: req.Model, Content: fmt.Sprintf("mock:%s:%s", req.Model, prompt)}, nil
}
