package domain

import "testing"

func TestClassifyByRulesReasoningOverride(t *testing.T) {
	cfg := ScoringConfig{
		TokenCountThresholds:   TokenCountThresholds{Simple: 10, Complex: 100},
		ReasoningKeywords:      []string{"reason", "explain", "why"},
		CodeKeywords:           []string{"code"},
		SimpleKeywords:         []string{"simple"},
		TechnicalKeywords:      []string{"technical"},
		CreativeKeywords:       []string{"creative"},
		ImperativeVerbs:        []string{"build"},
		ConstraintIndicators:   []string{"must"},
		OutputFormatKeywords:   []string{"json"},
		ReferenceKeywords:      []string{"file"},
		NegationKeywords:       []string{"not"},
		DomainSpecificKeywords: []string{"router"},
		AgenticTaskKeywords:    []string{"plan"},
		DimensionWeights: map[string]float64{
			"tokenCount":          0.30,
			"reasoningMarkers":    0.30,
			"codePresence":        0.35,
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
		TierBoundaries:      TierBoundaries{SimpleMedium: -0.1, MediumComplex: 0.2, ComplexReasoning: 0.4},
		ConfidenceSteepness: 8,
		ConfidenceThreshold: 0.65,
	}

	result := ClassifyByRules("why explain the tradeoff", nil, 20, cfg)
	if result.Tier != TierReasoning {
		t.Fatalf("expected REASONING tier, got %s", result.Tier)
	}
	if result.Confidence < 0.85 {
		t.Fatalf("expected high confidence override, got %.3f", result.Confidence)
	}
}

func TestClassifyByRulesAmbiguousBelowThreshold(t *testing.T) {
	cfg := ScoringConfig{
		TokenCountThresholds:   TokenCountThresholds{Simple: 10, Complex: 100},
		ReasoningKeywords:      []string{"reason"},
		CodeKeywords:           []string{"code"},
		SimpleKeywords:         []string{"simple"},
		TechnicalKeywords:      []string{"technical"},
		CreativeKeywords:       []string{"creative"},
		ImperativeVerbs:        []string{"build"},
		ConstraintIndicators:   []string{"must"},
		OutputFormatKeywords:   []string{"json"},
		ReferenceKeywords:      []string{"file"},
		NegationKeywords:       []string{"not"},
		DomainSpecificKeywords: []string{"router"},
		AgenticTaskKeywords:    []string{"plan"},
		DimensionWeights: map[string]float64{
			"tokenCount":          0.30,
			"reasoningMarkers":    0.30,
			"codePresence":        0.35,
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
		TierBoundaries:      TierBoundaries{SimpleMedium: 0.9, MediumComplex: 1.1, ComplexReasoning: 1.3},
		ConfidenceSteepness: 8,
		ConfidenceThreshold: 0.999999,
	}

	result := ClassifyByRules("hello", nil, 1, cfg)
	if !result.Ambiguous {
		t.Fatalf("expected ambiguous result")
	}
	if result.Tier != "" {
		t.Fatalf("expected no tier when ambiguous, got %s", result.Tier)
	}
}
