package providers

import "github.com/ARMmaster17/minirouter/internal/app"

type DeepseekProvider struct {
	*OpenAIProvider
}

func NewDeepseekProvider(providerID, baseURL, apiKey string, configured []app.Model) *DeepseekProvider {
	url := baseURL
	if url == "" {
		url = "https://api.deepseek.com/v1"
	}
	return &DeepseekProvider{OpenAIProvider: NewOpenAIProvider(providerID, url, apiKey, configured)}
}

var _ app.Provider = (*DeepseekProvider)(nil)
