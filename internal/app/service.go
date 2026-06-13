package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ARMmaster17/minirouter/internal/config"
	"github.com/ARMmaster17/minirouter/internal/domain"
)

type Model struct {
	ID              string   `json:"id"`
	Object          string   `json:"object"`
	OwnedBy         string   `json:"owned_by"`
	Provider        string   `json:"provider"`
	Alias           bool     `json:"alias"`
	ContextLimit    *int     `json:"context_limit,omitempty"`
	TokenInputCost  *float64 `json:"token_input_cost,omitempty"`
	TokenOutputCost *float64 `json:"token_output_cost,omitempty"`
}

type Catalog interface {
	Models() []Model
	HasModel(id string) bool
}

type StaticCatalog struct {
	models []Model
}

func NewStaticCatalog(cfg config.Config) *StaticCatalog {
	models := []Model{
		{ID: "auto", Object: "model", OwnedBy: "minirouter", Provider: "router", Alias: true},
		{ID: "auto-reasoning", Object: "model", OwnedBy: "minirouter", Provider: "router", Alias: true},
		{ID: "auto-complex", Object: "model", OwnedBy: "minirouter", Provider: "router", Alias: true},
		{ID: "auto-medium", Object: "model", OwnedBy: "minirouter", Provider: "router", Alias: true},
		{ID: "auto-simple", Object: "model", OwnedBy: "minirouter", Provider: "router", Alias: true},
	}
	for _, provider := range cfg.Providers {
		if !provider.Enabled && provider.Enabled != false {
			continue
		}
		for _, modelConfig := range provider.Models {
			if strings.TrimSpace(modelConfig.ID) == "" {
				continue
			}
			contextLimit := modelConfig.ContextLimit
			models = append(models, Model{
				ID:              normalizeModelID(provider.Kind, provider.Name, modelConfig.ID),
				Object:          "model",
				OwnedBy:         provider.Name,
				Provider:        provider.Kind,
				ContextLimit:    contextLimit,
				TokenInputCost:  modelConfig.TokenInputCost,
				TokenOutputCost: modelConfig.TokenOutputCost,
			})
		}
	}
	sort.SliceStable(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return &StaticCatalog{models: models}
}

func (c *StaticCatalog) Models() []Model {
	out := make([]Model, len(c.models))
	copy(out, c.models)
	return out
}

func (c *StaticCatalog) HasModel(id string) bool {
	for _, model := range c.models {
		if model.ID == id {
			return true
		}
	}
	return false
}

type Router struct {
	Config           config.Config
	Catalog          Catalog
	Providers        *ProviderRegistry
	RequestLogs      RequestLogStore
	ActiveRequests   ActiveRequestCounter
	TierScoringRules []TierModelScoringRule
}

func NewRouter(cfg config.Config, catalog Catalog, providers ...Provider) *Router {
	return &Router{Config: cfg, Catalog: catalog, Providers: NewProviderRegistry(providers...)}
}

func (r *Router) WithRequestLogStore(requestLogs RequestLogStore) *Router {
	r.RequestLogs = requestLogs
	return r
}

func (r *Router) WithActiveRequestCounter(activeRequests ActiveRequestCounter) *Router {
	r.ActiveRequests = activeRequests
	return r
}

func (r *Router) WithTierScoringRules(rules ...TierModelScoringRule) *Router {
	r.TierScoringRules = rules
	return r
}

func (r *Router) ListModels() []Model {
	metadata := r.modelMetadataByID()
	models := make([]Model, 0, len(metadata))
	for _, model := range metadata {
		models = append(models, model)
	}
	sort.SliceStable(models, func(i, j int) bool {
		if models[i].ID == "auto" {
			return true
		}
		if models[j].ID == "auto" {
			return false
		}
		return models[i].ID < models[j].ID
	})
	// Update the context limit for auto to be the maximum context limit among routable models.
	maxContextLimit := 0
	for _, model := range models {
		if model.ContextLimit != nil && *model.ContextLimit > maxContextLimit {
			maxContextLimit = *model.ContextLimit
		}
	}
	for i, model := range models {
		if model.ID == "auto" {
			models[i].ContextLimit = &maxContextLimit
		}
	}
	return models
}

func (r *Router) ModelRegistry() map[string][]Model {
	metadata := r.modelMetadataByID()
	registry := make(map[string][]Model)
	for _, model := range metadata {
		provider := strings.TrimSpace(model.Provider)
		if provider == "" {
			provider = modelProviderID(model.ID)
		}
		if provider == "" {
			provider = "unknown"
		}
		registry[provider] = append(registry[provider], model)
	}
	for provider := range registry {
		models := registry[provider]
		sort.SliceStable(models, func(i, j int) bool {
			return models[i].ID < models[j].ID
		})
		registry[provider] = models
	}
	return registry
}

func (r *Router) ResolveModel(requestModel, prompt string, estimatedInputTokens int) (string, domain.ScoringResult, error) {
	// If the requestModel is specified and not "auto" or auto-*, find it in the catalog
	if requestModel != "" && (requestModel != "auto" && !strings.HasPrefix(requestModel, "auto-")) {
		if metadata := r.modelMetadataByID(); metadata[requestModel].ID != "" {
			r.debugRouting("explicit_model", map[string]any{
				"requestModel":          requestModel,
				"resolvedModel":         requestModel,
				"estimatedInputTokens":  estimatedInputTokens,
				"result":                domain.ScoringResult{Tier: domain.TierReasoning, Confidence: 1},
				"decision":              "request model bypassed auto routing",
			})
			return requestModel, domain.ScoringResult{Tier: domain.TierReasoning, Confidence: 1}, nil
		}
		return "", domain.ScoringResult{}, errors.New("requested model not found")
	}
	if estimatedInputTokens <= 0 {
		estimatedInputTokens = len(prompt)/4 + 1
	}
	// If the requestModel is auto-*, use the suffix to determine the starting tier for selection
	if strings.HasPrefix(requestModel, "auto-") {
		suffix := strings.TrimPrefix(requestModel, "auto-")
		var tier domain.Tier
		switch suffix {
		case "reasoning":
			tier = domain.TierReasoning
		case "complex":
			tier = domain.TierComplex
		case "medium":
			tier = domain.TierMedium
		case "simple":
			tier = domain.TierSimple
		default:
			return "", domain.ScoringResult{}, fmt.Errorf("invalid auto tier suffix: %s", suffix)
		}
		result := domain.ScoringResult{Tier: tier, Confidence: 1}
		selected := r.selectTierModel(result, estimatedInputTokens)
		r.debugRouting("tier_alias", map[string]any{
			"requestModel":         requestModel,
			"selectedTier":         tier,
			"estimatedInputTokens": estimatedInputTokens,
			"resolvedModel":        selected,
			"result":               result,
		})
		return selected, result, nil
	}
	explanation := domain.ExplainClassification(prompt, nil, estimatedInputTokens, r.Config.Routing.Scoring)
	result := explanation.Result
	r.debugRouting("classification", map[string]any{
		"requestModel":         requestModel,
		"promptPreview":        promptPreview(prompt),
		"classification":       explanation,
		"ambiguousDefaultTier": r.Config.Routing.Overrides.AmbiguousDefaultTier,
	})
	selected := r.selectTierModel(result, estimatedInputTokens)
	if selected == "" {
		selected = r.fallbackModel()
		r.debugRouting("fallback_model", map[string]any{
			"reason":               "no eligible tier model selected",
			"estimatedInputTokens": estimatedInputTokens,
			"resolvedModel":        selected,
			"result":               result,
		})
	}
	if selected == "" {
		return "", result, errors.New("no routable model found")
	}
	return selected, result, nil
}

func (r *Router) Chat(ctx context.Context, req ChatRequest) (ChatResponse, domain.ScoringResult, error) {
	estimatedInputTokens := len(req.Prompt)/4 + 1
	if req.EstimatedInputTokens != nil && *req.EstimatedInputTokens > 0 {
		estimatedInputTokens = *req.EstimatedInputTokens
	}
	resolvedModel, result, err := r.ResolveModel(req.Model, req.Prompt, estimatedInputTokens)
	if err != nil {
		return ChatResponse{}, result, err
	}
	req.Model = resolvedModel
	if r.Providers == nil {
		return ChatResponse{Model: resolvedModel, Content: "no provider registry configured"}, result, nil
	}
	startTier := r.resolveRequestTier(resolvedModel, result, estimatedInputTokens)
	response, err := r.chatWithTierFallbacks(ctx, req, startTier, estimatedInputTokens, map[domain.Tier]struct{}{}, map[string]struct{}{})
	if err != nil {
		return ChatResponse{}, result, err
	}
	return response, result, nil
}

func (r *Router) failurePolicyForError(err error) domain.FailurePolicy {
	policy := r.Config.Routing.Failures.Default
	if err == nil {
		return policy
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "timeout"), strings.Contains(message, "deadline"):
		if failurePolicyConfigured(r.Config.Routing.Failures.Timeout) {
			return r.Config.Routing.Failures.Timeout
		}
	case strings.Contains(message, "rate"), strings.Contains(message, "429"):
		if failurePolicyConfigured(r.Config.Routing.Failures.RateLimit) {
			return r.Config.Routing.Failures.RateLimit
		}
	case strings.Contains(message, "unavailable"), strings.Contains(message, "503"), (strings.Contains(message, "400") && strings.Contains(message, "Model is unloaded")):
		if strings.Contains(message, "Model is unloaded") && r.Config.Routing.Failures.Unavailable.Retry > 0 {
			// Wait 5 seconds before trying again, helps with local LLMs like LM Studio and Ollama that return 400 when the model is still loading
			time.Sleep(5 * time.Second)
		}
		if failurePolicyConfigured(r.Config.Routing.Failures.Unavailable) {
			return r.Config.Routing.Failures.Unavailable
		}
	default:
		if failurePolicyConfigured(r.Config.Routing.Failures.ServerError) {
			return r.Config.Routing.Failures.ServerError
		}
	}
	return policy
}

func ptr[T any](value T) *T {
	return &value
}

func (r *Router) resolveRequestTier(modelID string, result domain.ScoringResult, estimatedInputTokens int) domain.Tier {
	tier := result.Tier
	if result.Ambiguous {
		tier = r.Config.Routing.Overrides.AmbiguousDefaultTier
	}
	if tier != "" {
		return tier
	}
	return r.findTierForModel(modelID, estimatedInputTokens)
}

func (r *Router) chatWithTierFallbacks(ctx context.Context, req ChatRequest, tier domain.Tier, estimatedInputTokens int, visited map[domain.Tier]struct{}, failedModels map[string]struct{}) (ChatResponse, error) {
	if tier == "" {
		if _, failed := failedModels[req.Model]; failed {
			return ChatResponse{}, fmt.Errorf("chat request failed: model %s already failed", req.Model)
		}
		response, _, err := r.chatModelWithPolicy(ctx, req)
		if err != nil {
			failedModels[req.Model] = struct{}{}
		}
		return response, err
	}
	if _, seen := visited[tier]; seen {
		return ChatResponse{}, fmt.Errorf("tier traversal cycle detected at %s", tier)
	}
	visited[tier] = struct{}{}

	candidates := r.rankModelsForTier(tier, estimatedInputTokens)
	if len(candidates) == 0 {
		return ChatResponse{}, fmt.Errorf("no routable models in tier %s", tier)
	}
	if req.Model != "" {
		candidates = prioritizeModel(candidates, req.Model)
	}
	orderedCandidates, evaluations, skipped := r.rankModelsForTierWithDetails(tier, estimatedInputTokens)
	if req.Model != "" {
		orderedCandidates = prioritizeModel(orderedCandidates, req.Model)
	}
	r.debugRouting("tier_candidates", map[string]any{
		"tier":                 tier,
		"estimatedInputTokens": estimatedInputTokens,
		"requestedModel":       req.Model,
		"orderedCandidates":    orderedCandidates,
		"evaluations":          evaluations,
		"skipped":              skipped,
	})

	lastPolicy := r.failurePolicyForError(nil)
	var lastErr error
	attemptedAny := false
	for _, candidate := range candidates {
		if _, failed := failedModels[candidate]; failed {
			continue
		}
		attemptedAny = true
		attemptReq := req
		attemptReq.Model = candidate
		r.debugRouting("chat_attempt", map[string]any{
			"tier":      tier,
			"model":     candidate,
			"attemptReq": map[string]any{"stream": attemptReq.Stream, "messages": len(attemptReq.Messages)},
		})
		response, policy, err := r.chatModelWithPolicy(ctx, attemptReq)
		if err == nil {
			r.debugRouting("chat_attempt_result", map[string]any{
				"tier":   tier,
				"model":  candidate,
				"result": "success",
			})
			return response, nil
		}
		failedModels[candidate] = struct{}{}
		lastErr = err
		lastPolicy = policy
		r.debugRouting("chat_attempt_result", map[string]any{
			"tier":         tier,
			"model":        candidate,
			"result":       "failure",
			"error":        err.Error(),
			"failurePolicy": policy,
		})
		if !failurePolicyTierNext(policy) {
			break
		}
	}

	if lastErr == nil {
		if !attemptedAny {
			lastErr = fmt.Errorf("no remaining unfailed models in tier %s", tier)
		} else {
			lastErr = fmt.Errorf("chat request failed")
		}
	}
	switch normalizeTierSwitch(lastPolicy.TierSwitch) {
	case domain.TierSwitchUp, domain.TierSwitchDown:
		nextTier, ok := adjacentTier(tier, normalizeTierSwitch(lastPolicy.TierSwitch))
		if !ok {
			return ChatResponse{}, lastErr
		}
		r.debugRouting("tier_fallback", map[string]any{
			"fromTier":     tier,
			"toTier":       nextTier,
			"error":        lastErr.Error(),
			"failurePolicy": lastPolicy,
		})
		nextReq := req
		nextReq.Model = ""
		return r.chatWithTierFallbacks(ctx, nextReq, nextTier, estimatedInputTokens, visited, failedModels)
	default:
		return ChatResponse{}, lastErr
	}
}

func (r *Router) chatModelWithPolicy(ctx context.Context, req ChatRequest) (ChatResponse, domain.FailurePolicy, error) {
	policy := r.failurePolicyForError(nil)
	attempts := policy.Retry + 1
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		provider, err := r.Providers.Resolve(req.Model)
		if err != nil {
			lastErr = err
			break
		}
		providerID := modelProviderID(req.Model)
		if providerID == "" {
			providerID = strings.TrimSpace(provider.ID())
		}
		if r.ActiveRequests != nil {
			r.ActiveRequests.Increment(providerID)
		}
		response, err := provider.ChatCompletions(ctx, req)
		if r.ActiveRequests != nil {
			r.ActiveRequests.Decrement(providerID)
		}
		if err == nil {
			if response.Model == "" {
				response.Model = req.Model
			}
			return response, policy, nil
		}
		lastErr = err
		policy = r.failurePolicyForError(err)
		attempts = policy.Retry + 1
		if attempts <= 0 {
			attempts = 1
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("chat request failed")
	}
	return ChatResponse{}, policy, lastErr
}

func (r *Router) rankModelsForTier(tier domain.Tier, estimatedInputTokens int) []string {
	ordered, _, _ := r.rankModelsForTierWithDetails(tier, estimatedInputTokens)
	return ordered
}

func (r *Router) findTierForModel(modelID string, estimatedInputTokens int) domain.Tier {
	tiers := []domain.Tier{domain.TierSimple, domain.TierMedium, domain.TierComplex, domain.TierReasoning}
	for _, tier := range tiers {
		for _, candidate := range r.rankModelsForTier(tier, estimatedInputTokens) {
			if candidate == modelID {
				return tier
			}
		}
	}
	return ""
}

func failurePolicyTierNext(policy domain.FailurePolicy) bool {
	if policy.TierNext == nil {
		return true
	}
	return *policy.TierNext
}

func normalizeTierSwitch(direction domain.TierSwitch) domain.TierSwitch {
	switch strings.ToLower(strings.TrimSpace(string(direction))) {
	case string(domain.TierSwitchUp):
		return domain.TierSwitchUp
	case string(domain.TierSwitchDown):
		return domain.TierSwitchDown
	default:
		return domain.TierSwitchNone
	}
}

func failurePolicyConfigured(policy domain.FailurePolicy) bool {
	return policy.Retry != 0 || policy.TierNext != nil || normalizeTierSwitch(policy.TierSwitch) != domain.TierSwitchNone
}

func adjacentTier(current domain.Tier, direction domain.TierSwitch) (domain.Tier, bool) {
	tiers := []domain.Tier{domain.TierSimple, domain.TierMedium, domain.TierComplex, domain.TierReasoning}
	index := -1
	for idx, tier := range tiers {
		if tier == current {
			index = idx
			break
		}
	}
	if index < 0 {
		return "", false
	}
	switch direction {
	case domain.TierSwitchUp:
		if index >= len(tiers)-1 {
			return "", false
		}
		return tiers[index+1], true
	case domain.TierSwitchDown:
		if index <= 0 {
			return "", false
		}
		return tiers[index-1], true
	default:
		return "", false
	}
}

func prioritizeModel(models []string, modelID string) []string {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return models
	}
	index := -1
	for idx, model := range models {
		if model == modelID {
			index = idx
			break
		}
	}
	if index <= 0 {
		return models
	}
	out := make([]string, 0, len(models))
	out = append(out, models[index])
	out = append(out, models[:index]...)
	out = append(out, models[index+1:]...)
	return out
}

func (r *Router) selectTierModel(result domain.ScoringResult, estimatedInputTokens int) string {
	tier := result.Tier
	if result.Ambiguous {
		tier = r.Config.Routing.Overrides.AmbiguousDefaultTier
	}
	ordered, evaluations, skipped := r.rankModelsForTierWithDetails(tier, estimatedInputTokens)
	if len(ordered) == 0 {
		r.debugRouting("tier_selection", map[string]any{
			"tier":                 tier,
			"estimatedInputTokens": estimatedInputTokens,
			"selectedModel":        "",
			"result":               result,
			"skipped":              skipped,
		})
		return ""
	}
	selected := ordered[0]
	r.debugRouting("tier_selection", map[string]any{
		"tier":                 tier,
		"estimatedInputTokens": estimatedInputTokens,
		"selectedModel":        selected,
		"result":               result,
		"orderedCandidates":    ordered,
		"evaluations":          evaluations,
		"skipped":              skipped,
	})
	return selected
}

func promptPreview(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if len(prompt) <= 120 {
		return prompt
	}
	return prompt[:117] + "..."
}

func (r *Router) modelMetadataByID() map[string]Model {
	metadata := make(map[string]Model)
	if r.Catalog != nil {
		for _, model := range r.Catalog.Models() {
			metadata[model.ID] = model
		}
	}
	if r.Providers == nil {
		return metadata
	}
	providerModels, err := r.Providers.Models(context.Background())
	if err != nil {
		return metadata
	}
	for _, model := range providerModels {
		metadata[model.ID] = model
	}
	return metadata
}

func withinContextLimit(model Model, estimatedInputTokens int) bool {
	if estimatedInputTokens <= 0 || model.ContextLimit == nil {
		return true
	}
	return estimatedInputTokens <= *model.ContextLimit
}

func (r *Router) providerThrashLimits() map[string]int {
	limits := make(map[string]int)
	for _, provider := range r.Config.Providers {
		if provider.ThrashLimit == nil || *provider.ThrashLimit <= 0 {
			continue
		}
		providerID := normalizeModelID(provider.Kind, provider.Name, "")
		if providerID == "" {
			continue
		}
		limits[providerID] = *provider.ThrashLimit
	}
	return limits
}

func (r *Router) providerParallelLimits() map[string]int {
	limits := make(map[string]int)
	for _, provider := range r.Config.Providers {
		if provider.ParallelLimit == nil || *provider.ParallelLimit <= 0 {
			continue
		}
		providerID := normalizeModelID(provider.Kind, provider.Name, "")
		if providerID == "" {
			continue
		}
		limits[providerID] = *provider.ParallelLimit
	}
	return limits
}

func (r *Router) recentModelsByProvider(maxLimit int) map[string]map[string]int {
	if maxLimit <= 0 || r.RequestLogs == nil {
		return nil
	}
	recentEntries := r.RequestLogs.Recent(maxLimit * 20)
	byProvider := make(map[string][]string)
	seen := make(map[string]map[string]struct{})
	for _, entry := range recentEntries {
		modelID := strings.TrimSpace(entry.ResolvedModel)
		if modelID == "" {
			continue
		}
		providerID := modelProviderID(modelID)
		if providerID == "" {
			continue
		}
		if _, ok := byProvider[providerID]; !ok {
			byProvider[providerID] = make([]string, 0, maxLimit)
		}
		if len(byProvider[providerID]) >= maxLimit {
			continue
		}
		if _, ok := seen[providerID]; !ok {
			seen[providerID] = make(map[string]struct{})
		}
		if _, exists := seen[providerID][modelID]; exists {
			continue
		}
		byProvider[providerID] = append(byProvider[providerID], modelID)
		seen[providerID][modelID] = struct{}{}
	}
	out := make(map[string]map[string]int)
	for providerID, models := range byProvider {
		out[providerID] = make(map[string]int, len(models))
		for index, modelID := range models {
			out[providerID][modelID] = index
		}
	}
	return out
}

func maxProviderThrashLimit(limits map[string]int) int {
	max := 0
	for _, limit := range limits {
		if limit > max {
			max = limit
		}
	}
	return max
}

func (r *Router) fallbackModel() string {
	modelMetadata := r.modelMetadataByID()
	tiers := []domain.Tier{domain.TierSimple, domain.TierMedium, domain.TierComplex, domain.TierReasoning}
	for _, tier := range tiers {
		tierConfig, ok := r.Config.Routing.Tiers[tier]
		if !ok {
			continue
		}
		for _, candidate := range tierConfig.Models {
			if candidate == "auto" {
				continue
			}
			if _, ok := modelMetadata[candidate]; ok {
				return candidate
			}
		}
	}
	return ""
}

func (r *Router) hasModel(id string) bool {
	_, ok := r.modelMetadataByID()[id]
	return ok
}

func normalizeModelID(kind, name, model string) string {
	parts := []string{strings.TrimSpace(kind), strings.TrimSpace(name), strings.TrimSpace(model)}
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	return strings.Join(filtered, ":")
}

func modelProviderID(modelID string) string {
	index := strings.LastIndex(strings.TrimSpace(modelID), ":")
	if index <= 0 {
		return ""
	}
	return modelID[:index]
}
