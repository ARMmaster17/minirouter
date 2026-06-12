package domain

import (
	"math"
	"regexp"
	"strings"
)

type dimensionScore struct {
	name   string
	score  float64
	signal string
}

func scoreTokenCount(estimatedTokens int, thresholds TokenCountThresholds) dimensionScore {
	if estimatedTokens < thresholds.Simple {
		return dimensionScore{name: "tokenCount", score: -1.0, signal: "short"}
	}
	if estimatedTokens > thresholds.Complex {
		return dimensionScore{name: "tokenCount", score: 1.0, signal: "long"}
	}
	return dimensionScore{name: "tokenCount", score: 0}
}

func scoreKeywordMatch(text string, keywords []string, name, signalLabel string, thresholds struct{ low, high int }, scores struct{ none, low, high float64 }) dimensionScore {
	matches := make([]string, 0, len(keywords))
	for _, keyword := range keywords {
		if strings.Contains(text, strings.ToLower(keyword)) {
			matches = append(matches, keyword)
		}
	}
	if len(matches) >= thresholds.high {
		return dimensionScore{name: name, score: scores.high, signal: signalLabel}
	}
	if len(matches) >= thresholds.low {
		return dimensionScore{name: name, score: scores.low, signal: signalLabel}
	}
	return dimensionScore{name: name, score: scores.none}
}

func scoreMultiStep(text string) dimensionScore {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`first.*then`),
		regexp.MustCompile(`step \d`),
		regexp.MustCompile(`\d\.\s`),
	}
	for _, pattern := range patterns {
		if pattern.MatchString(text) {
			return dimensionScore{name: "multiStepPatterns", score: 0.5, signal: "multi-step"}
		}
	}
	return dimensionScore{name: "multiStepPatterns", score: 0}
}

func scoreQuestionComplexity(prompt string) dimensionScore {
	count := strings.Count(prompt, "?")
	if count > 3 {
		return dimensionScore{name: "questionComplexity", score: 0.5, signal: "questions"}
	}
	return dimensionScore{name: "questionComplexity", score: 0}
}

func scoreAgenticTask(text string, keywords []string) (dimensionScore, float64) {
	matchCount := 0
	signals := make([]string, 0, 3)
	for _, keyword := range keywords {
		if strings.Contains(text, strings.ToLower(keyword)) {
			matchCount++
			if len(signals) < 3 {
				signals = append(signals, keyword)
			}
		}
	}
	switch {
	case matchCount >= 4:
		return dimensionScore{name: "agenticTask", score: 1.0, signal: strings.Join(signals, ", ")}, 1.0
	case matchCount >= 3:
		return dimensionScore{name: "agenticTask", score: 0.6, signal: strings.Join(signals, ", ")}, 0.6
	case matchCount >= 1:
		return dimensionScore{name: "agenticTask", score: 0.2, signal: strings.Join(signals, ", ")}, 0.2
	default:
		return dimensionScore{name: "agenticTask", score: 0}, 0
	}
}

func ClassifyByRules(prompt string, systemPrompt *string, estimatedTokens int, config ScoringConfig) ScoringResult {
	text := strings.ToLower(strings.TrimSpace(func() string {
		if systemPrompt == nil {
			return prompt
		}
		return *systemPrompt + " " + prompt
	}()))
	userText := strings.ToLower(prompt)

	dimensions := []dimensionScore{
		scoreTokenCount(estimatedTokens, config.TokenCountThresholds),
		scoreKeywordMatch(text, config.CodeKeywords, "codePresence", "code", struct{ low, high int }{1, 2}, struct{ none, low, high float64 }{0, 0.5, 1.0}),
		scoreKeywordMatch(userText, config.ReasoningKeywords, "reasoningMarkers", "reasoning", struct{ low, high int }{1, 2}, struct{ none, low, high float64 }{0, 0.7, 1.0}),
		scoreKeywordMatch(text, config.TechnicalKeywords, "technicalTerms", "technical", struct{ low, high int }{2, 4}, struct{ none, low, high float64 }{0, 0.5, 1.0}),
		scoreKeywordMatch(text, config.CreativeKeywords, "creativeMarkers", "creative", struct{ low, high int }{1, 2}, struct{ none, low, high float64 }{0, 0.5, 0.7}),
		scoreKeywordMatch(text, config.SimpleKeywords, "simpleIndicators", "simple", struct{ low, high int }{1, 2}, struct{ none, low, high float64 }{0, -1.0, -1.0}),
		scoreMultiStep(text),
		scoreQuestionComplexity(prompt),
		scoreKeywordMatch(text, config.ImperativeVerbs, "imperativeVerbs", "imperative", struct{ low, high int }{1, 2}, struct{ none, low, high float64 }{0, 0.3, 0.5}),
		scoreKeywordMatch(text, config.ConstraintIndicators, "constraintCount", "constraints", struct{ low, high int }{1, 3}, struct{ none, low, high float64 }{0, 0.3, 0.7}),
		scoreKeywordMatch(text, config.OutputFormatKeywords, "outputFormat", "format", struct{ low, high int }{1, 2}, struct{ none, low, high float64 }{0, 0.4, 0.7}),
		scoreKeywordMatch(text, config.ReferenceKeywords, "referenceComplexity", "references", struct{ low, high int }{1, 2}, struct{ none, low, high float64 }{0, 0.3, 0.5}),
		scoreKeywordMatch(text, config.NegationKeywords, "negationComplexity", "negation", struct{ low, high int }{2, 3}, struct{ none, low, high float64 }{0, 0.3, 0.5}),
		scoreKeywordMatch(text, config.DomainSpecificKeywords, "domainSpecificity", "domain-specific", struct{ low, high int }{1, 2}, struct{ none, low, high float64 }{0, 0.5, 0.8}),
	}

	agenticDimension, agenticScore := scoreAgenticTask(text, config.AgenticTaskKeywords)
	dimensions = append(dimensions, agenticDimension)

	signals := make([]string, 0, len(dimensions))
	weightedScore := 0.0
	for _, dimension := range dimensions {
		if dimension.signal != "" {
			signals = append(signals, dimension.signal)
		}
		weightedScore += dimension.score * config.DimensionWeights[dimension.name]
	}

	reasoningMatches := make([]string, 0)
	for _, keyword := range config.ReasoningKeywords {
		if strings.Contains(userText, strings.ToLower(keyword)) {
			reasoningMatches = append(reasoningMatches, keyword)
		}
	}

	if len(reasoningMatches) >= 2 {
		confidence := calibrateConfidence(math.Max(weightedScore, 0.3), config.ConfidenceSteepness)
		if confidence < 0.85 {
			confidence = 0.85
		}
		return ScoringResult{Score: weightedScore, Tier: TierReasoning, Confidence: confidence, Signals: signals, AgenticScore: agenticScore}
	}

	boundaries := config.TierBoundaries
	var tier Tier
	var distanceFromBoundary float64

	switch {
	case weightedScore < boundaries.SimpleMedium:
		tier = TierSimple
		distanceFromBoundary = boundaries.SimpleMedium - weightedScore
	case weightedScore < boundaries.MediumComplex:
		tier = TierMedium
		distanceFromBoundary = math.Min(weightedScore-boundaries.SimpleMedium, boundaries.MediumComplex-weightedScore)
	case weightedScore < boundaries.ComplexReasoning:
		tier = TierComplex
		distanceFromBoundary = math.Min(weightedScore-boundaries.MediumComplex, boundaries.ComplexReasoning-weightedScore)
	default:
		tier = TierReasoning
		distanceFromBoundary = weightedScore - boundaries.ComplexReasoning
	}

	confidence := calibrateConfidence(distanceFromBoundary, config.ConfidenceSteepness)
	if confidence < config.ConfidenceThreshold {
		return ScoringResult{Score: weightedScore, Confidence: confidence, Signals: signals, AgenticScore: agenticScore, Ambiguous: true}
	}

	return ScoringResult{Score: weightedScore, Tier: tier, Confidence: confidence, Signals: signals, AgenticScore: agenticScore}
}

func calibrateConfidence(distance, steepness float64) float64 {
	return 1 / (1 + math.Exp(-steepness*distance))
}
