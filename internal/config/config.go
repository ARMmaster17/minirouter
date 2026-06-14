package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ARMmaster17/minirouter/internal/domain"
)

type Config struct {
	Server    ServerConfig         `json:"server"`
	Routing   domain.RoutingConfig `json:"routing"`
	Providers []ProviderConfig     `json:"providers"`
}

type ServerConfig struct {
	Addr            string `json:"addr"`
	FrontendEnabled bool   `json:"frontendEnabled"`
	IncomingAPIKey  string `json:"incomingAPIKey"`
}

type ModelConfig struct {
	ID              string   `json:"id"`
	ContextLimit    *int     `json:"contextLimit,omitempty"`
	TokenInputCost  *float64 `json:"tokenInputCost,omitempty"`
	TokenOutputCost *float64 `json:"tokenOutputCost,omitempty"`
}

func (m *ModelConfig) UnmarshalJSON(data []byte) error {
	var rawString string
	if err := json.Unmarshal(data, &rawString); err == nil {
		m.ID = strings.TrimSpace(rawString)
		m.ContextLimit = nil
		return nil
	}
	var asObject struct {
		ID              string   `json:"id"`
		ContextLimit    *int     `json:"contextLimit,omitempty"`
		TokenInputCost  *float64 `json:"tokenInputCost,omitempty"`
		TokenOutputCost *float64 `json:"tokenOutputCost,omitempty"`
	}
	if err := json.Unmarshal(data, &asObject); err != nil {
		return err
	}
	m.ID = strings.TrimSpace(asObject.ID)
	m.ContextLimit = asObject.ContextLimit
	m.TokenInputCost = asObject.TokenInputCost
	m.TokenOutputCost = asObject.TokenOutputCost
	return nil
}

type ProviderConfig struct {
	Kind          string        `json:"kind"`
	Name          string        `json:"name"`
	APIKey        string        `json:"apiKey"`
	URL           string        `json:"url"`
	Models        []ModelConfig `json:"models"`
	ThrashLimit   *int          `json:"thrashLimit,omitempty"`
	ParallelLimit *int          `json:"parallelLimit,omitempty"`
	Enabled       bool          `json:"enabled"`
}

func Default() Config {
	scoring := domain.ScoringConfig{
		TokenCountThresholds:   domain.TokenCountThresholds{Simple: 400, Complex: 1600},
		CodeKeywords:           []string{"code", "function", "type", "interface", "api", "compile", "error", "bug"},
		ReasoningKeywords:      []string{"reason", "explain", "analyze", "why", "tradeoff"},
		SimpleKeywords:         []string{"simple", "quick", "brief", "summarize"},
		TechnicalKeywords:      []string{"go", "http", "json", "grpc", "sql", "docker", "kubernetes"},
		CreativeKeywords:       []string{"write", "story", "poem", "brainstorm", "design"},
		ImperativeVerbs:        []string{"build", "create", "implement", "fix", "add"},
		ConstraintIndicators:   []string{"must", "should", "only", "exactly", "without"},
		OutputFormatKeywords:   []string{"json", "yaml", "table", "code", "markdown"},
		ReferenceKeywords:      []string{"this", "that", "above", "below", "file"},
		NegationKeywords:       []string{"not", "never", "without", "except"},
		DomainSpecificKeywords: []string{"router", "model", "provider", "streaming", "fallback"},
		AgenticTaskKeywords:    []string{"plan", "iterate", "refactor", "deploy", "test"},
		DimensionWeights: map[string]float64{
			"tokenCount":          0.30,
			"codePresence":        0.35,
			"reasoningMarkers":    0.30,
			"technicalTerms":      0.20,
			"creativeMarkers":     0.12,
			"simpleIndicators":    0.18,
			"multiStepPatterns":   0.12,
			"questionComplexity":  0.10,
			"imperativeVerbs":     0.10,
			"constraintCount":     0.15,
			"outputFormat":        0.12,
			"referenceComplexity": 0.08,
			"negationComplexity":  0.08,
			"domainSpecificity":   0.12,
			"agenticTask":         0.20,
		},
		TierBoundaries:      domain.TierBoundaries{SimpleMedium: -0.10, MediumComplex: 0.18, ComplexReasoning: 0.42},
		ConfidenceSteepness: 8,
		ConfidenceThreshold: 0.65,
	}

	return Config{
		Server: ServerConfig{Addr: ":8080", FrontendEnabled: true},
		Routing: domain.RoutingConfig{
			Version: "v1",
			Debug:   false,
			Scoring: scoring,
			Tiers: map[domain.Tier]domain.TierConfig{
				domain.TierSimple:    {Models: []string{"openai:default"}},
				domain.TierMedium:    {Models: []string{"openai:default"}},
				domain.TierComplex:   {Models: []string{"openai:default"}},
				domain.TierReasoning: {Models: []string{"openai:default"}},
			},
			Failures: domain.FailureRoutingConfig{
				Default:     domain.FailurePolicy{TierSwitch: domain.TierSwitchNone},
				Timeout:     domain.FailurePolicy{Retry: 1, TierSwitch: domain.TierSwitchNone},
				RateLimit:   domain.FailurePolicy{Retry: 1, TierSwitch: domain.TierSwitchNone},
				Unavailable: domain.FailurePolicy{TierSwitch: domain.TierSwitchNone},
				ServerError: domain.FailurePolicy{TierSwitch: domain.TierSwitchNone},
			},
			Overrides: domain.OverridesConfig{MaxTokensForceComplex: 8000, StructuredOutputMinTier: domain.TierMedium, AmbiguousDefaultTier: domain.TierMedium},
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return Config{}, err
		}
		defer file.Close()

		decoder := json.NewDecoder(file)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&cfg); err != nil {
			return Config{}, err
		}
	}
	applyEnvOverrides(&cfg)
	if cfg.Server.Addr == "" {
		cfg.Server.Addr = ":8080"
	}
	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if value := os.Getenv("MINIROUTER_SERVER_ADDR"); value != "" {
		cfg.Server.Addr = value
	}
	if value := os.Getenv("MINIROUTER_FRONTEND_ENABLED"); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			cfg.Server.FrontendEnabled = parsed
		}
	}
	if value := os.Getenv("MINIROUTER_INCOMING_API_KEY"); value != "" {
		cfg.Server.IncomingAPIKey = value
	}
	if value := os.Getenv("MINIROUTER_ROUTING_CONFIDENCE_THRESHOLD"); value != "" {
		if parsed, err := strconv.ParseFloat(value, 64); err == nil {
			cfg.Routing.Scoring.ConfidenceThreshold = parsed
		}
	}
	if value := os.Getenv("MINIROUTER_ROUTING_DEBUG"); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			cfg.Routing.Debug = parsed
		}
	}
	if value := os.Getenv("MINIROUTER_PAYLOAD_DEBUG"); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			cfg.Routing.PayloadDebug = parsed
		}
	}
	for index := range cfg.Providers {
		provider := &cfg.Providers[index]
		prefix := strings.ToUpper(provider.Name)
		if prefix == "" {
			prefix = strings.ToUpper(provider.Kind) + fmt.Sprintf("_%d", index)
		}
		if value := os.Getenv("MINIROUTER_" + prefix + "_API_KEY"); value != "" {
			provider.APIKey = value
		}
		if value := os.Getenv("MINIROUTER_" + prefix + "_URL"); value != "" {
			provider.URL = value
		}
	}
}
