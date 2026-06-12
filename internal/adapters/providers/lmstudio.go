package providers

import "github.com/ARMmaster17/minirouter/internal/app"

type LMStudioProvider struct {
	*OpenAIProvider
}

func NewLMStudioProvider(providerID, baseURL, apiKey string, configured []app.Model) *LMStudioProvider {
	url := baseURL
	if url == "" {
		url = "http://localhost:1234/v1"
	}
	return &LMStudioProvider{OpenAIProvider: NewOpenAIProvider(providerID, url, apiKey, configured)}
}

var _ app.Provider = (*LMStudioProvider)(nil)
