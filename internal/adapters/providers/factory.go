package providers

import (
	"fmt"
	"strings"

	"github.com/ARMmaster17/minirouter/internal/app"
	"github.com/ARMmaster17/minirouter/internal/config"
)

func Build(cfg config.Config) ([]app.Provider, error) {
	providers := make([]app.Provider, 0, len(cfg.Providers))
	for _, providerCfg := range cfg.Providers {
		if !providerCfg.Enabled {
			continue
		}

		providerID := providerCfg.Kind + ":" + providerCfg.Name
		configuredModels := make([]app.Model, 0, len(providerCfg.Models))
		for _, modelCfg := range providerCfg.Models {
			if strings.TrimSpace(modelCfg.ID) == "" {
				continue
			}
			configuredModels = append(configuredModels, app.Model{
				ID:              app.ProviderModelID(providerID, modelCfg.ID),
				Object:          "model",
				OwnedBy:         providerID,
				Provider:        providerID,
				ContextLimit:    modelCfg.ContextLimit,
				TokenInputCost:  modelCfg.TokenInputCost,
				TokenOutputCost: modelCfg.TokenOutputCost,
			})
		}
		kind := strings.ToLower(strings.TrimSpace(providerCfg.Kind))
		switch kind {
		case "openai":
			providers = append(providers, NewOpenAIProvider(providerID, providerCfg.URL, providerCfg.APIKey, configuredModels))
		case "lmstudio", "lm_studio", "lm-studio":
			providers = append(providers, NewLMStudioProvider(providerID, providerCfg.URL, providerCfg.APIKey, configuredModels))
		case "gemini", "google", "google-gemini":
			providers = append(providers, NewGeminiProvider(providerID, providerCfg.URL, providerCfg.APIKey, configuredModels))
		case "ollama":
			providers = append(providers, NewOllamaProvider(providerID, providerCfg.URL, providerCfg.APIKey, configuredModels))
		case "deepseek":
			providers = append(providers, NewDeepseekProvider(providerID, providerCfg.URL, providerCfg.APIKey, configuredModels))
		default:
			return nil, fmt.Errorf("unsupported provider kind: %s", providerCfg.Kind)
		}
	}
	return providers, nil
}
