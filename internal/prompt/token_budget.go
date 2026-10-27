package prompt

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/luispabon/steiner/internal/provider"
)

const normalPromptCompactionThreshold = 0.70

// minCompletionReserveTokens is the floor for a clamped completion reserve. It
// matches the Anthropic wire default and provider defaultMaxOutputTokens.
const minCompletionReserveTokens = 4096

// explicitMaxTokenParamKeys are the params and extra-params keys that set an
// explicit output token limit on the wire.
var explicitMaxTokenParamKeys = []string{"max_tokens", "max_completion_tokens", "max_output_tokens"}

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
	completionReserve = m.clampUnsetCompletionReserve(request, estimatedPromptTokens, completionReserve)
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
	if explicit, ok := explicitMaxTokensValue(request); ok {
		if reserve <= 0 || explicit < reserve {
			reserve = explicit
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

// clampUnsetCompletionReserve limits the reserve to the space left after the
// prompt, keeping at least minCompletionReserveTokens. It applies only when the
// request has no explicit max-tokens value, because the advertised max output
// is an upper bound and is not sent on the wire. An explicit value is already
// the reserve, so clamping it would under-reserve.
func (m ModelTokenBudget) clampUnsetCompletionReserve(request provider.ChatRequest, promptTokens, reserve int) int {
	if m.ContextSize <= 0 || reserve <= 0 {
		return reserve
	}
	if _, ok := explicitMaxTokensValue(request); ok {
		return reserve
	}
	floor := min(reserve, minCompletionReserveTokens)
	available := max(m.ContextSize-promptTokens-m.SafetyMarginTokens, floor)
	return min(reserve, available)
}

// explicitMaxTokensValue returns the first positive output-token limit carried
// by the request: MaxTokens, then a max_tokens, max_completion_tokens, or
// max_output_tokens entry in Params or ExtraParams.
func explicitMaxTokensValue(request provider.ChatRequest) (int, bool) {
	if request.MaxTokens != nil && *request.MaxTokens > 0 {
		return *request.MaxTokens, true
	}
	for _, params := range []map[string]any{request.Params, request.ExtraParams} {
		for _, key := range explicitMaxTokenParamKeys {
			if value, ok := positiveTokenValue(params[key]); ok {
				return value, true
			}
		}
	}
	return 0, false
}

func positiveTokenValue(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, v > 0
	case int64:
		return int(v), v > 0
	case float64:
		return int(v), v > 0
	case json.Number:
		n, err := v.Float64()
		if err != nil {
			return 0, false
		}
		return int(n), n > 0
	}
	return 0, false
}

// String returns a compact human-readable representation of the fitted budget.
func (r RequestTokenBudget) String() string {
	return fmt.Sprintf("prompt=%d usage=%.0f%% hard_limit=%d compact=%t fits=%t",
		r.EstimatedPromptTokens, r.PromptUsage*100, r.HardLimitTokens, r.ShouldCompact, r.Fits)
}
