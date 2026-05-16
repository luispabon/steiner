package prompt

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/provider"
)

// ModelBudgetFromEffectiveLimits derives a ModelTokenBudget from resolved EffectiveLimits.
// Used to adapt provider.ResolvedModel limits to prompt budgeting.
func ModelBudgetFromEffectiveLimits(limits provider.EffectiveLimits) ModelTokenBudget {
	return ModelTokenBudget{
		ContextSize:         limits.ContextWindow,
		MaxCompletionTokens: limits.MaxOutputTokens,
		SafetyMarginTokens:  limits.SafetyMarginTokens,
		SummaryMaxTokens:    limits.SummaryMaxTokens,
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

	estimatedPromptTokens, err := provider.EstimateChatRequestTokens(ctx, request)
	if err != nil {
		return RequestTokenBudget{}, err
	}

	total := estimatedPromptTokens + completionReserve + m.SafetyMarginTokens
	fit := m.ContextSize <= 0 || total <= m.ContextSize
	return RequestTokenBudget{
		EstimatedPromptTokens:    estimatedPromptTokens,
		ReservedCompletionTokens: completionReserve,
		SafetyMarginTokens:       m.SafetyMarginTokens,
		TotalTokens:              total,
		ContextSize:              m.ContextSize,
		Fits:                     fit,
	}, nil
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
	reserve := m.SummaryMaxTokens
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
	return fmt.Sprintf("prompt=%d reserve=%d safety=%d total=%d context=%d fits=%t",
		r.EstimatedPromptTokens, r.ReservedCompletionTokens, r.SafetyMarginTokens, r.TotalTokens, r.ContextSize, r.Fits)
}
