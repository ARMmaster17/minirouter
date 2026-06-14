package domain

type Tier string

const (
	TierSimple    Tier = "SIMPLE"
	TierMedium    Tier = "MEDIUM"
	TierComplex   Tier = "COMPLEX"
	TierReasoning Tier = "REASONING"
)

type ScoringResult struct {
	Score        float64  `json:"score"`
	Tier         Tier     `json:"tier"`
	Confidence   float64  `json:"confidence"`
	Signals      []string `json:"signals"`
	AgenticScore float64  `json:"agenticScore"`
	Ambiguous    bool     `json:"ambiguous"`
}

type TokenCountThresholds struct {
	Simple  int `json:"simple"`
	Complex int `json:"complex"`
}

type ScoringConfig struct {
	TokenCountThresholds   TokenCountThresholds `json:"tokenCountThresholds"`
	CodeKeywords           []string             `json:"codeKeywords"`
	ReasoningKeywords      []string             `json:"reasoningKeywords"`
	SimpleKeywords         []string             `json:"simpleKeywords"`
	TechnicalKeywords      []string             `json:"technicalKeywords"`
	CreativeKeywords       []string             `json:"creativeKeywords"`
	ImperativeVerbs        []string             `json:"imperativeVerbs"`
	ConstraintIndicators   []string             `json:"constraintIndicators"`
	OutputFormatKeywords   []string             `json:"outputFormatKeywords"`
	ReferenceKeywords      []string             `json:"referenceKeywords"`
	NegationKeywords       []string             `json:"negationKeywords"`
	DomainSpecificKeywords []string             `json:"domainSpecificKeywords"`
	AgenticTaskKeywords    []string             `json:"agenticTaskKeywords"`
	DimensionWeights       map[string]float64   `json:"dimensionWeights"`
	TierBoundaries         TierBoundaries       `json:"tierBoundaries"`
	ConfidenceSteepness    float64              `json:"confidenceSteepness"`
	ConfidenceThreshold    float64              `json:"confidenceThreshold"`
}

type TierBoundaries struct {
	SimpleMedium     float64 `json:"simpleMedium"`
	MediumComplex    float64 `json:"mediumComplex"`
	ComplexReasoning float64 `json:"complexReasoning"`
}

type TierConfig struct {
	Models []string `json:"models"`
}

type OverridesConfig struct {
	MaxTokensForceComplex   int  `json:"maxTokensForceComplex"`
	StructuredOutputMinTier Tier `json:"structuredOutputMinTier"`
	AmbiguousDefaultTier    Tier `json:"ambiguousDefaultTier"`
	AgenticMode             bool `json:"agenticMode"`
}

type FailurePolicy struct {
	Retry      int        `json:"retry"`
	TierNext   *bool      `json:"tierNext,omitempty"`
	TierSwitch TierSwitch `json:"tierSwitch,omitempty"`
}

type TierSwitch string

const (
	TierSwitchNone TierSwitch = "none"
	TierSwitchUp   TierSwitch = "up"
	TierSwitchDown TierSwitch = "down"
)

type FailureRoutingConfig struct {
	Default     FailurePolicy `json:"default"`
	Timeout     FailurePolicy `json:"timeout"`
	RateLimit   FailurePolicy `json:"rateLimit"`
	Unavailable FailurePolicy `json:"unavailable"`
	ServerError FailurePolicy `json:"serverError"`
}

type RoutingConfig struct {
	Version      string               `json:"version"`
	Debug        bool                 `json:"debug"`
	PayloadDebug bool                 `json:"payloadDebug"`
	Scoring      ScoringConfig        `json:"scoring"`
	Tiers        map[Tier]TierConfig  `json:"tiers"`
	AgenticTiers map[Tier]TierConfig  `json:"agenticTiers"`
	Failures     FailureRoutingConfig `json:"failures"`
	Overrides    OverridesConfig      `json:"overrides"`
}

type RoutingDecision struct {
	Model        string  `json:"model"`
	Tier         Tier    `json:"tier"`
	Confidence   float64 `json:"confidence"`
	Method       string  `json:"method"`
	Reasoning    string  `json:"reasoning"`
	CostEstimate float64 `json:"costEstimate"`
	BaselineCost float64 `json:"baselineCost"`
	Savings      float64 `json:"savings"`
}
