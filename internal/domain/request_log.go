package domain

import "time"

type RequestStatus string

const (
	RequestStatusSuccess RequestStatus = "success"
	RequestStatusError   RequestStatus = "error"
)

type TokenSource string

const (
	TokenSourceProvider  TokenSource = "provider"
	TokenSourceTokenizer TokenSource = "estimated"
	TokenSourceHeuristic TokenSource = "heuristic"
	TokenSourceNone      TokenSource = "none"
)

type RequestLogEntry struct {
	ID               string        `json:"id"`
	CreatedAt        time.Time     `json:"createdAt"`
	Method           string        `json:"method"`
	Path             string        `json:"path"`
	RequestedModel   string        `json:"requestedModel"`
	ResolvedModel    string        `json:"resolvedModel"`
	ProviderID       string        `json:"providerId"`
	Tier             Tier          `json:"tier"`
	Confidence       float64       `json:"confidence"`
	Status           RequestStatus `json:"status"`
	HTTPStatus       int           `json:"httpStatus"`
	Error            string        `json:"error,omitempty"`
	PromptTokens     int           `json:"promptTokens"`
	CompletionTokens int           `json:"completionTokens"`
	TotalTokens      int           `json:"totalTokens"`
	TokenSource      TokenSource   `json:"tokenSource"`
	RequestBytes     int           `json:"requestBytes"`
	ResponseBytes    int           `json:"responseBytes"`
	Duration         time.Duration `json:"duration"`
}

type RequestAggregateStats struct {
	TotalRequests      int            `json:"totalRequests"`
	SuccessRequests    int            `json:"successRequests"`
	ErrorRequests      int            `json:"errorRequests"`
	TotalPromptTokens  int            `json:"totalPromptTokens"`
	TotalOutputTokens  int            `json:"totalOutputTokens"`
	TotalTokens        int            `json:"totalTokens"`
	TotalRequestBytes  int            `json:"totalRequestBytes"`
	TotalResponseBytes int            `json:"totalResponseBytes"`
	AverageLatencyMS   float64        `json:"averageLatencyMs"`
	ByModel            map[string]int `json:"byModel"`
	ByTier             map[Tier]int   `json:"byTier"`
}
