package prompt

import (
	"context"
	"fmt"
	"math"

	"github.com/luispabon/steiner/internal/provider"
)

const normalPromptCompactionThreshold = 0.70

// ModelBudgetFromEffectiveLimits derives a ModelTokenBudget from resolved EffectiveLimits.
// Used to adapt provider.ResolvedModel limits to prompt budgeting.
func ModelBudgetFromEffectiveLimits(limits provider.EffectiveLimits) ModelTokenBudget {
	return ModelTokenBudget{
		ContextSize:               limits.ContextWindow,
		MaxCompletionTokens:       limits.MaxOutputTokens,
		SafetyMarginTokens:        limits.EstimatorPadTokens,
		SummaryMaxTokens:          limits.NormalSummaryMaxTokens,
		NormalSummaryMaxTokens:    limits.NormalSummaryMaxTokens,
		EmergencySummaryMaxTokens: limits.EmergencySummaryMaxTokens,
		CompactionThreshold:       limits.CompactionThreshold,
		CompactionThresholdTokens: limits.CompactionThresholdTokens,
	}
}

// Normalized clamps negative budget fields to zero.
func (m ModelTokenBudget) Normalized() ModelTokenBudget {
	if m.ContextSize < 0 {
		m.ContextSize = 0
	}
	if m.MaxCompletionTokens < 0 {
		m.MaxCompletionTokens = 0
	}
	if m.SafetyMarginTokens < 0 {
		m.SafetyMarginTokens = 0
	}
	if m.SummaryMaxTokens < 0 {
		m.SummaryMaxTokens = 0
	}
	if m.NormalSummaryMaxTokens < 0 {
		m.NormalSummaryMaxTokens = 0
	}
	if m.EmergencySummaryMaxTokens < 0 {
		m.EmergencySummaryMaxTokens = 0
	}
	return m
}

// FitRequest estimates whether a normal chat request fits within the model budget.
func (m ModelTokenBudget) FitRequest(ctx context.Context, request provider.ChatRequest) (RequestTokenBudget, error) {
	return m.fit(ctx, request, m.completionReserveForRequest(request))
}

// FitCompactionRequest estimates whether a compaction request fits within the model budget.
func (m ModelTokenBudget) FitCompactionRequest(ctx context.Context, request provider.ChatRequest) (RequestTokenBudget, error) {
	return m.fit(ctx, request, m.completionReserveForCompaction(request))
}

func (m ModelTokenBudget) fit(ctx context.Context, request provider.ChatRequest, completionReserve int) (RequestTokenBudget, error) {
	if err := ctx.Err(); err != nil {
		return RequestTokenBudget{}, err
	}
	m = m.Normalized()
	if completionReserve < 0 {
		completionReserve = 0
	}

	estimate, err := provider.EstimateChatRequestTokenEstimate(ctx, request)
	if err != nil {
		return RequestTokenBudget{}, err
	}

	estimatedPromptTokens := estimate.Tokens
	total := estimatedPromptTokens + completionReserve + m.SafetyMarginTokens
	fit := m.ContextSize <= 0 || total <= m.ContextSize
	return RequestTokenBudget{
		EstimatedPromptTokens:     estimatedPromptTokens,
		RawEstimatedPromptTokens:  estimate.RawTokens,
		PromptUsage:               promptUsage(estimatedPromptTokens, m.ContextSize),
		CompactionThreshold:       m.compactionThresholdPercent(),
		CompactionThresholdTokens: m.compactionThresholdTokens(),
		HardLimitTokens:           hardPromptLimit(m.ContextSize, m.SafetyMarginTokens),
		ShouldCompact:             shouldCompactPrompt(estimatedPromptTokens, m),
		ReservedCompletionTokens:  completionReserve,
		SafetyMarginTokens:        m.SafetyMarginTokens,
		TotalTokens:               total,
		ContextSize:               m.ContextSize,
		Fits:                      fit,
	}, nil
}

func promptUsage(promptTokens, contextTokens int) float64 {
	if promptTokens <= 0 || contextTokens <= 0 {
		return 0
	}
	return float64(promptTokens) / float64(contextTokens)
}

func hardPromptLimit(contextTokens, estimatorPadTokens int) int {
	if contextTokens <= 0 {
		return 0
	}
	limit := contextTokens - estimatorPadTokens
	if limit < 0 {
		return 0
	}
	return limit
}

func shouldCompactPrompt(promptTokens int, budget ModelTokenBudget) bool {
	if budget.ContextSize <= 0 {
		return false
	}
	if budget.CompactionThresholdTokens > 0 {
		if promptTokens >= budget.CompactionThresholdTokens {
			return true
		}
	} else if promptUsage(promptTokens, budget.ContextSize) >= budget.compactionThresholdPercent() {
		return true
	}
	return promptTokens > hardPromptLimit(budget.ContextSize, budget.SafetyMarginTokens)
}

func (m ModelTokenBudget) compactionThresholdPercent() float64 {
	if m.CompactionThreshold > 0 {
		return m.CompactionThreshold
	}
	return normalPromptCompactionThreshold
}

func (m ModelTokenBudget) compactionThresholdTokens() int {
	if m.ContextSize <= 0 {
		return 0
	}
	if m.CompactionThresholdTokens > 0 {
		return m.CompactionThresholdTokens
	}
	return int(math.Ceil(float64(m.ContextSize) * m.compactionThresholdPercent()))
}

func (m ModelTokenBudget) completionReserveForRequest(request provider.ChatRequest) int {
	reserve := m.MaxCompletionTokens
	if request.MaxTokens != nil && *request.MaxTokens > 0 {
		if reserve <= 0 || *request.MaxTokens < reserve {
			reserve = *request.MaxTokens
		}
	}
	return reserve
}

func (m ModelTokenBudget) completionReserveForCompaction(request provider.ChatRequest) int {
	reserve := m.NormalSummaryMaxTokens
	if reserve <= 0 {
		reserve = m.SummaryMaxTokens
	}
	if reserve <= 0 {
		reserve = m.MaxCompletionTokens
	}
	if request.MaxTokens != nil && *request.MaxTokens > 0 {
		if reserve <= 0 || *request.MaxTokens < reserve {
			reserve = *request.MaxTokens
		}
	}
	return reserve
}

// String returns a compact human-readable representation of the fitted budget.
func (r RequestTokenBudget) String() string {
	return fmt.Sprintf("prompt=%d usage=%.0f%% hard_limit=%d compact=%t fits=%t",
		r.EstimatedPromptTokens, r.PromptUsage*100, r.HardLimitTokens, r.ShouldCompact, r.Fits)
}
