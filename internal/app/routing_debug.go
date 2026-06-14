package app

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/ARMmaster17/minirouter/internal/domain"
)

type tierCandidateSkip struct {
	Model  string `json:"model"`
	Reason string `json:"reason"`
}

func (r *Router) routingDebugEnabled() bool {
	return r.Config.Routing.Debug
}

func (r *Router) payloadDebugEnabled() bool {
	return r.Config.Routing.PayloadDebug
}

func (r *Router) debugRouting(event string, payload any) {
	if !r.routingDebugEnabled() {
		return
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stdout, "[routing-debug] %s\n%+v\n", event, payload)
		return
	}
	fmt.Fprintf(os.Stdout, "[routing-debug] %s\n%s\n", event, encoded)
}

func (r *Router) debugPayload(event string, payload []byte) {
	if !r.payloadDebugEnabled() {
		return
	}
	fmt.Fprintf(os.Stdout, "[payload-debug] %s\n%s\n", event, payload)
}

func (r *Router) tierCandidatesForTier(tier domain.Tier, estimatedInputTokens int) ([]string, map[string]Model, []tierCandidateSkip) {
	tierConfig, ok := r.Config.Routing.Tiers[tier]
	if !ok {
		return nil, nil, nil
	}
	modelMetadata := r.modelMetadataByID()
	candidates := make([]string, 0, len(tierConfig.Models))
	skipped := make([]tierCandidateSkip, 0)
	for _, candidate := range tierConfig.Models {
		if candidate == "auto" {
			skipped = append(skipped, tierCandidateSkip{Model: candidate, Reason: "auto alias is not routable inside a tier"})
			continue
		}
		metadata, ok := modelMetadata[candidate]
		if !ok {
			skipped = append(skipped, tierCandidateSkip{Model: candidate, Reason: "model not found in catalog"})
			continue
		}
		if !withinContextLimit(metadata, estimatedInputTokens) {
			skipped = append(skipped, tierCandidateSkip{Model: candidate, Reason: "estimated input exceeds context limit"})
			continue
		}
		candidates = append(candidates, candidate)
	}
	return candidates, modelMetadata, skipped
}

func (r *Router) rankModelsForTierWithDetails(tier domain.Tier, estimatedInputTokens int) ([]string, []TierCandidateEvaluation, []tierCandidateSkip) {
	candidates, modelMetadata, skipped := r.tierCandidatesForTier(tier, estimatedInputTokens)
	if len(candidates) == 0 {
		return nil, nil, skipped
	}
	ordered, evaluations := r.evaluateTierCandidates(candidates, modelMetadata, estimatedInputTokens)
	return ordered, evaluations, skipped
}
