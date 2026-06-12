package app

import "testing"

func TestParallelLimitRuleDoesNotPenalizeBelowLimit(t *testing.T) {
	rule := parallelLimitRule{}
	ctx := TierModelScoringContext{
		ParallelLimits:   map[string]int{"openai:primary": 2},
		ActiveByProvider: map[string]int{"openai:primary": 1},
	}

	score := rule.Score("openai:primary:model-a", Model{}, 0, ctx)
	if score != 0 {
		t.Fatalf("expected no penalty below limit, got %v", score)
	}
}

func TestParallelLimitRulePenalizesAtOrAboveLimit(t *testing.T) {
	rule := parallelLimitRule{}
	ctx := TierModelScoringContext{
		ParallelLimits:   map[string]int{"openai:primary": 2},
		ActiveByProvider: map[string]int{"openai:primary": 2},
	}

	score := rule.Score("openai:primary:model-a", Model{}, 0, ctx)
	if score >= 0 {
		t.Fatalf("expected negative penalty at limit, got %v", score)
	}

	ctx.ActiveByProvider["openai:primary"] = 4
	overloadedScore := rule.Score("openai:primary:model-a", Model{}, 0, ctx)
	if overloadedScore >= score {
		t.Fatalf("expected stronger penalty above limit, at limit=%v above limit=%v", score, overloadedScore)
	}
}

func TestConfigModelOrderRuleRewardsEarlierModels(t *testing.T) {
	rule := configModelOrderRule{}
	ctx := TierModelScoringContext{
		ConfigModelOrder: map[string]int{
			"openai:primary:first":  0,
			"openai:primary:second": 1,
		},
	}

	first := rule.Score("openai:primary:first", Model{}, 0, ctx)
	second := rule.Score("openai:primary:second", Model{}, 0, ctx)
	missing := rule.Score("openai:primary:missing", Model{}, 0, ctx)

	if first <= second {
		t.Fatalf("expected earlier config model to receive stronger score, first=%v second=%v", first, second)
	}
	if missing != 0 {
		t.Fatalf("expected no score for model missing from config order map, got %v", missing)
	}
}
