package app

import (
	"sort"
)

type TierModelScoringContext struct {
	EstimatedInputTokens int
	ThrashLimits         map[string]int
	ParallelLimits       map[string]int
	RecentByProvider     map[string]map[string]int
	ActiveByProvider     map[string]int
	ConfigModelOrder     map[string]int
}

type TierCandidateRuleScore struct {
	Rule  string  `json:"rule"`
	Score float64 `json:"score"`
}

type TierCandidateEvaluation struct {
	Model         string                   `json:"model"`
	OriginalIndex int                      `json:"originalIndex"`
	TotalScore    float64                  `json:"totalScore"`
	RuleScores    []TierCandidateRuleScore `json:"ruleScores"`
}

type TierModelScoringRule interface {
	Name() string
	Score(candidateID string, candidate Model, index int, ctx TierModelScoringContext) float64
}

type baseOrderRule struct{}

type pricePreferenceRule struct{}

type thrashLimitRule struct{}

type parallelLimitRule struct{}

type configModelOrderRule struct{}

func (baseOrderRule) Name() string { return "baseOrder" }

func (baseOrderRule) Score(_ string, _ Model, index int, _ TierModelScoringContext) float64 {
	return -float64(index)
}

func (pricePreferenceRule) Name() string { return "pricePreference" }

func (pricePreferenceRule) Score(_ string, candidate Model, _ int, _ TierModelScoringContext) float64 {
	return pricePreferenceScore(candidate)
}

func (thrashLimitRule) Name() string { return "thrashLimit" }

func (thrashLimitRule) Score(candidateID string, _ Model, _ int, ctx TierModelScoringContext) float64 {
	providerID := modelProviderID(candidateID)
	limit, ok := ctx.ThrashLimits[providerID]
	if !ok || limit <= 0 {
		return 0
	}
	recency, ok := ctx.RecentByProvider[providerID][candidateID]
	if !ok {
		return 0
	}
	return 0.6 + 0.6*(float64(limit-recency)/float64(limit))
}

func (parallelLimitRule) Name() string { return "parallelLimit" }

func (parallelLimitRule) Score(candidateID string, _ Model, _ int, ctx TierModelScoringContext) float64 {
	providerID := modelProviderID(candidateID)
	limit, ok := ctx.ParallelLimits[providerID]
	if !ok || limit <= 0 {
		return 0
	}
	active := ctx.ActiveByProvider[providerID]
	if active < limit {
		return 0
	}
	occupancy := float64(active) / float64(limit)
	penalty := 0.8 + 0.4*(occupancy-1)
	if penalty > 1.6 {
		penalty = 1.6
	}
	return -penalty
}

func (configModelOrderRule) Name() string { return "configModelOrder" }

func (configModelOrderRule) Score(candidateID string, _ Model, _ int, ctx TierModelScoringContext) float64 {
	order, ok := ctx.ConfigModelOrder[candidateID]
	if !ok {
		return 0
	}
	// Earlier config order receives a slightly stronger preference.
	return 0.2 / float64(order+1)
}

func defaultTierScoringRules() []TierModelScoringRule {
	return []TierModelScoringRule{
		baseOrderRule{},
		configModelOrderRule{},
		pricePreferenceRule{},
		thrashLimitRule{},
		parallelLimitRule{},
	}
}

func (r *Router) rankTierCandidates(candidates []string, modelMetadata map[string]Model, estimatedInputTokens int) []string {
	ordered, _ := r.evaluateTierCandidates(candidates, modelMetadata, estimatedInputTokens)
	return ordered
}

func (r *Router) evaluateTierCandidates(candidates []string, modelMetadata map[string]Model, estimatedInputTokens int) ([]string, []TierCandidateEvaluation) {
	ordered := make([]string, len(candidates))
	copy(ordered, candidates)
	if len(ordered) <= 1 {
		evaluations := make([]TierCandidateEvaluation, 0, len(ordered))
		for idx, candidateID := range ordered {
			evaluations = append(evaluations, TierCandidateEvaluation{
				Model:         candidateID,
				OriginalIndex: idx,
				TotalScore:    0,
			})
		}
		return ordered, evaluations
	}
	ctx := r.buildTierScoringContext(ordered, estimatedInputTokens)
	rules := r.TierScoringRules
	if len(rules) == 0 {
		rules = defaultTierScoringRules()
	}

	type candidateScore struct {
		model string
		index int
		score float64
		rules []TierCandidateRuleScore
	}
	scored := make([]candidateScore, 0, len(ordered))
	for idx, candidateID := range ordered {
		candidate := modelMetadata[candidateID]
		score := 0.0
		ruleScores := make([]TierCandidateRuleScore, 0, len(rules))
		for _, rule := range rules {
			ruleScore := rule.Score(candidateID, candidate, idx, ctx)
			score += ruleScore
			ruleScores = append(ruleScores, TierCandidateRuleScore{Rule: rule.Name(), Score: ruleScore})
		}
		scored = append(scored, candidateScore{model: candidateID, index: idx, score: score, rules: ruleScores})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].index < scored[j].index
		}
		return scored[i].score > scored[j].score
	})
	out := make([]string, 0, len(scored))
	evaluations := make([]TierCandidateEvaluation, 0, len(scored))
	for _, item := range scored {
		out = append(out, item.model)
		evaluations = append(evaluations, TierCandidateEvaluation{
			Model:         item.model,
			OriginalIndex: item.index,
			TotalScore:    item.score,
			RuleScores:    item.rules,
		})
	}
	return out, evaluations
}

func (r *Router) buildTierScoringContext(candidates []string, estimatedInputTokens int) TierModelScoringContext {
	ctx := TierModelScoringContext{
		EstimatedInputTokens: estimatedInputTokens,
		ThrashLimits:         r.providerThrashLimits(),
		ParallelLimits:       r.providerParallelLimits(),
		RecentByProvider:     map[string]map[string]int{},
		ActiveByProvider:     map[string]int{},
		ConfigModelOrder:     r.configModelOrder(),
	}
	maxLimit := maxProviderThrashLimit(ctx.ThrashLimits)
	if len(ctx.ThrashLimits) > 0 && maxLimit > 0 && r.RequestLogs != nil {
		ctx.RecentByProvider = r.recentModelsByProvider(maxLimit)
	}
	if r.ActiveRequests != nil {
		for _, candidateID := range candidates {
			providerID := modelProviderID(candidateID)
			if providerID == "" {
				continue
			}
			if _, exists := ctx.ActiveByProvider[providerID]; exists {
				continue
			}
			ctx.ActiveByProvider[providerID] = r.ActiveRequests.Count(providerID)
		}
	}
	return ctx
}

func (r *Router) configModelOrder() map[string]int {
	order := make(map[string]int)
	index := 0
	for _, provider := range r.Config.Providers {
		providerID := normalizeModelID(provider.Kind, provider.Name, "")
		if providerID == "" {
			continue
		}
		for _, model := range provider.Models {
			modelID := normalizeModelID(provider.Kind, provider.Name, model.ID)
			if modelID == "" {
				continue
			}
			if _, exists := order[modelID]; exists {
				continue
			}
			order[modelID] = index
			index++
		}
	}
	return order
}

func pricePreferenceScore(model Model) float64 {
	cost := 0.0
	if model.TokenInputCost != nil {
		cost += *model.TokenInputCost
	}
	if model.TokenOutputCost != nil {
		cost += *model.TokenOutputCost
	}
	if cost <= 0 {
		return 1.2
	}
	if cost < 1 {
		return 1.0
	}
	if cost < 5 {
		return 0.5
	}
	if cost < 15 {
		return 0.1
	}
	return -0.3
}
