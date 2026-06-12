package app

import (
	"context"
	"strings"
	"testing"

	"github.com/ARMmaster17/minirouter/internal/config"
	"github.com/ARMmaster17/minirouter/internal/domain"
)

func TestChatFallsBackToBackupModel(t *testing.T) {
	cfg := config.Default()
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"mock:primary:primary-chat", "mock:backup:backup-chat"}}
	cfg.Routing.Failures.Default = domain.FailurePolicy{Retry: 0, TierSwitch: domain.TierSwitchNone}

	primary := NewMockProviderWithFailures("mock:primary", []string{"primary-chat"}, nil, map[string]string{"mock:primary:primary-chat": "primary failed"})
	backup := NewMockProvider("mock:backup", []string{"backup-chat"}, map[string]string{"mock:backup:backup-chat": "backup response"})
	router := NewRouter(cfg, NewStaticCatalog(cfg), primary, backup)

	response, _, err := router.Chat(context.Background(), ChatRequest{Model: "auto", Prompt: "simple request"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Model != "mock:backup:backup-chat" {
		t.Fatalf("expected fallback model, got %s", response.Model)
	}
	if response.Content != "backup response" {
		t.Fatalf("expected fallback response, got %q", response.Content)
	}
}

func TestChatTierSwitchesDownRecursively(t *testing.T) {
	cfg := config.Default()
	tierNext := false
	cfg.Routing.Failures.Default = domain.FailurePolicy{Retry: 0, TierNext: &tierNext, TierSwitch: domain.TierSwitchDown}
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"mock:simple:simple"}}
	cfg.Routing.Tiers[domain.TierMedium] = domain.TierConfig{Models: []string{"mock:medium:medium"}}
	cfg.Routing.Tiers[domain.TierComplex] = domain.TierConfig{Models: []string{"mock:complex:complex"}}
	cfg.Routing.Tiers[domain.TierReasoning] = domain.TierConfig{Models: []string{"mock:reasoning:reasoning"}}

	reasoning := NewMockProviderWithFailures("mock:reasoning", []string{"reasoning"}, nil, map[string]string{"mock:reasoning:reasoning": "reasoning failed"})
	complex := NewMockProviderWithFailures("mock:complex", []string{"complex"}, nil, map[string]string{"mock:complex:complex": "complex failed"})
	medium := NewMockProviderWithFailures("mock:medium", []string{"medium"}, nil, map[string]string{"mock:medium:medium": "medium failed"})
	simple := NewMockProvider("mock:simple", []string{"simple"}, map[string]string{"mock:simple:simple": "simple response"})
	router := NewRouter(cfg, NewStaticCatalog(cfg), reasoning, complex, medium, simple)

	response, _, err := router.Chat(context.Background(), ChatRequest{Model: "mock:reasoning:reasoning", Prompt: "fallback down"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Model != "mock:simple:simple" {
		t.Fatalf("expected recursive tier switch to SIMPLE model, got %s", response.Model)
	}
}

func TestChatReturnsErrorWhenTierNextDisabledAndNoTierSwitch(t *testing.T) {
	cfg := config.Default()
	tierNext := false
	cfg.Routing.Failures.Default = domain.FailurePolicy{Retry: 0, TierNext: &tierNext, TierSwitch: domain.TierSwitchNone}
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"mock:primary:primary-chat", "mock:backup:backup-chat"}}

	primary := NewMockProviderWithFailures("mock:primary", []string{"primary-chat"}, nil, map[string]string{"mock:primary:primary-chat": "primary failed"})
	backup := NewMockProvider("mock:backup", []string{"backup-chat"}, map[string]string{"mock:backup:backup-chat": "backup response"})
	router := NewRouter(cfg, NewStaticCatalog(cfg), primary, backup)

	_, _, err := router.Chat(context.Background(), ChatRequest{Model: "mock:primary:primary-chat", Prompt: "no switch"})
	if err == nil {
		t.Fatalf("expected error when tierNext is false and tierSwitch is none")
	}
}

func TestChatSkipsPreviouslyFailedModelsAcrossTierSwitches(t *testing.T) {
	cfg := config.Default()
	tierNext := false
	cfg.Routing.Failures.Default = domain.FailurePolicy{Retry: 0, TierNext: &tierNext, TierSwitch: domain.TierSwitchDown}
	cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"mock:shared:shared"}}
	cfg.Routing.Tiers[domain.TierMedium] = domain.TierConfig{Models: []string{"mock:shared:shared"}}
	cfg.Routing.Tiers[domain.TierComplex] = domain.TierConfig{Models: []string{"mock:reasoning:reasoning"}}
	cfg.Routing.Tiers[domain.TierReasoning] = domain.TierConfig{Models: []string{"mock:reasoning:reasoning"}}

	reasoning := NewMockProviderWithFailures("mock:reasoning", []string{"reasoning"}, nil, map[string]string{"mock:reasoning:reasoning": "reasoning failed"})
	shared := NewMockProviderWithFailures("mock:shared", []string{"shared"}, nil, map[string]string{"mock:shared:shared": "shared failed"})
	router := NewRouter(cfg, NewStaticCatalog(cfg), reasoning, shared)

	_, _, err := router.Chat(context.Background(), ChatRequest{Model: "mock:reasoning:reasoning", Prompt: "skip failed"})
	if err == nil {
		t.Fatalf("expected failure when all models fail")
	}
	if strings.Contains(strings.ToLower(err.Error()), "cycle") {
		t.Fatalf("expected no cycle; failed models should be skipped across tiers, got %v", err)
	}
}
