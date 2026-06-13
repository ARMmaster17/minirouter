package providers

import (
	"github.com/ARMmaster17/minirouter/internal/app"
)

type GeminiProvider struct {
	*OpenAIProvider
}

func NewGeminiProvider(providerID, baseURL, apiKey string, configured []app.Model) *GeminiProvider {
	url := baseURL
	if url == "" {
		url = "https://generativelanguage.googleapis.com/v1beta/openai/"
	}
	return &GeminiProvider{OpenAIProvider: NewOpenAIProvider(providerID, url, apiKey, configured)}
}

var _ app.Provider = (*GeminiProvider)(nil)
