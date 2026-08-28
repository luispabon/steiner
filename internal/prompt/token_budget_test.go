package prompt

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

func TestModelTokenBudgetFitsToolHeavyPayloadWithCompletionAndSafetyReserve(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	toolReq := provider.ChatRequest{
		Model: "gpt-4o",
		Messages: []provider.Message{
			{Role: provider.MessageRoleSystem, Content: "system instructions"},
			{Role: provider.MessageRoleUser, Content: "review the tool-heavy request"},
			{
				Role:    provider.MessageRoleAssistant,
				Content: "calling tools",
				ToolCalls: []provider.ToolCall{
					{
						ID:   "call_1",
						Name: "inspect",
						Arguments: map[string]any{
							"path":   "notes.txt",
							"filter": strings.Repeat("x", 128),
						},
					},
				},
			},
			{Role: provider.MessageRoleTool, ToolCallID: "call_1", Name: "inspect", Content: strings.Repeat("tool output ", 24)},
		},
		Tools: []provider.ToolSpec{
			{
				Type: "function",
				Function: provider.ToolFunctionSpec{
					Name:        "inspect",
					Description: strings.Repeat("describe the inspection tool ", 6),
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path": map[string]any{
								"type":        "string",
								"description": strings.Repeat("filesystem path ", 8),
							},
							"filter": map[string]any{
								"type":        "string",
								"description": strings.Repeat("selector ", 12),
							},
						},
						"required": []any{"path"},
					},
				},
			},
		},
	}

	leanReq := toolReq
	leanReq.Tools = nil

	leanTokens, err := provider.EstimateChatRequestTokens(ctx, leanReq)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens(lean) error = %v", err)
	}
	toolTokens, err := provider.EstimateChatRequestTokens(ctx, toolReq)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens(tool-heavy) error = %v", err)
	}
	if toolTokens <= leanTokens {
		t.Fatalf("tool-heavy tokens = %d, want > lean tokens %d", toolTokens, leanTokens)
	}

	budget := ModelTokenBudget{
		ContextSize:         toolTokens + 64 + 32,
		MaxCompletionTokens: 64,
		SafetyMarginTokens:  32,
		SummaryMaxTokens:    16,
	}

	fit, err := budget.FitRequest(ctx, toolReq)
	if err != nil {
		t.Fatalf("FitRequest() error = %v", err)
	}
	if !fit.Fits {
		t.Fatalf("FitRequest() = %+v, want fit", fit)
	}
	if got, want := fit.EstimatedPromptTokens, toolTokens; got != want {
		t.Fatalf("FitRequest prompt tokens = %d, want %d", got, want)
	}
	if got, want := fit.ReservedCompletionTokens, 64; got != want {
		t.Fatalf("FitRequest completion reserve = %d, want %d", got, want)
	}
	if got, want := fit.SafetyMarginTokens, 32; got != want {
		t.Fatalf("FitRequest safety margin = %d, want %d", got, want)
	}
	if fit.ShouldCompact {
		t.Fatal("FitRequest() should not request compaction for the baseline fit")
	}
	if got, want := fit.HardLimitTokens, budget.ContextSize-budget.SafetyMarginTokens; got != want {
		t.Fatalf("FitRequest hard limit = %d, want %d", got, want)
	}
	if fit.PromptUsage <= 0 {
		t.Fatalf("FitRequest prompt usage = %f, want > 0", fit.PromptUsage)
	}

	tooSmall := budget
	tooSmall.ContextSize = toolTokens + budget.SafetyMarginTokens - 1
	fit, err = tooSmall.FitRequest(ctx, toolReq)
	if err != nil {
		t.Fatalf("FitRequest(smaller) error = %v", err)
	}
	if fit.Fits {
		t.Fatalf("FitRequest(smaller) = %+v, want not fit", fit)
	}
	if !fit.ShouldCompact {
		t.Fatal("FitRequest(smaller) should request compaction when the hard prompt limit is exceeded")
	}
}

func TestModelTokenBudgetFitCompactionRequestUsesSummaryReserve(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	request := provider.ChatRequest{
		Model: "gpt-4o",
		Messages: []provider.Message{
			{Role: provider.MessageRoleSystem, Content: "compaction system"},
			{Role: provider.MessageRoleUser, Content: strings.Repeat("request ", 20)},
			{Role: provider.MessageRoleTool, ToolCallID: "call_1", Name: "inspect", Content: strings.Repeat("tool result ", 18)},
		},
		Tools: []provider.ToolSpec{
			{
				Type: "function",
				Function: provider.ToolFunctionSpec{
					Name:        "inspect",
					Description: "summarize the workspace",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path": map[string]any{"type": "string"},
						},
					},
				},
			},
		},
	}

	promptTokens, err := provider.EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}

	budget := ModelTokenBudget{
		ContextSize:         promptTokens + 8 + 24,
		MaxCompletionTokens: 96,
		SafetyMarginTokens:  24,
		SummaryMaxTokens:    8,
	}

	fit, err := budget.FitCompactionRequest(ctx, request)
	if err != nil {
		t.Fatalf("FitCompactionRequest() error = %v", err)
	}
	if !fit.Fits {
		t.Fatalf("FitCompactionRequest() = %+v, want fit", fit)
	}
	if got, want := fit.ReservedCompletionTokens, 8; got != want {
		t.Fatalf("FitCompactionRequest completion reserve = %d, want %d", got, want)
	}
	if got, want := fit.TotalTokens, promptTokens+8+24; got != want {
		t.Fatalf("FitCompactionRequest total tokens = %d, want %d", got, want)
	}
}

func TestModelTokenBudgetFitRequestRequestsCompactionAtSeventyPercentUsage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	request := provider.ChatRequest{
		Model: "gpt-4o",
		Messages: []provider.Message{
			{Role: provider.MessageRoleSystem, Content: "system instructions"},
			{Role: provider.MessageRoleUser, Content: "review the request"},
		},
	}

	promptTokens, err := provider.EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}

	contextSize := int(math.Floor(float64(promptTokens) / normalPromptCompactionThreshold))
	budget := ModelTokenBudget{ContextSize: contextSize}

	fit, err := budget.FitRequest(ctx, request)
	if err != nil {
		t.Fatalf("FitRequest() error = %v", err)
	}
	if !fit.ShouldCompact {
		t.Fatalf("FitRequest() = %+v, want compaction at >=70%% prompt usage", fit)
	}
	if !fit.Fits {
		t.Fatalf("FitRequest() = %+v, want hard fit when the prompt still fits", fit)
	}
}

func TestModelTokenBudgetFitRequestLeavesBelowThresholdPromptAlone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	request := provider.ChatRequest{
		Model: "gpt-4o",
		Messages: []provider.Message{
			{Role: provider.MessageRoleSystem, Content: "system instructions"},
			{Role: provider.MessageRoleUser, Content: "review the request"},
		},
	}

	promptTokens, err := provider.EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}

	contextSize := int(math.Ceil(float64(promptTokens) / normalPromptCompactionThreshold))
	if promptUsage(promptTokens, contextSize) >= normalPromptCompactionThreshold {
		contextSize++
	}

	budget := ModelTokenBudget{ContextSize: contextSize}

	fit, err := budget.FitRequest(ctx, request)
	if err != nil {
		t.Fatalf("FitRequest() error = %v", err)
	}
	if fit.ShouldCompact {
		t.Fatalf("FitRequest() = %+v, want no compaction below the threshold", fit)
	}
	if !fit.Fits {
		t.Fatalf("FitRequest() = %+v, want hard fit below the threshold", fit)
	}
	if fit.PromptUsage >= normalPromptCompactionThreshold {
		t.Fatalf("FitRequest() prompt usage = %f, want below %f", fit.PromptUsage, normalPromptCompactionThreshold)
	}
}

func TestModelTokenBudgetFitRequestTriggersHardLimitPadBeforeThreshold(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	request := provider.ChatRequest{
		Model: "gpt-4o",
		Messages: []provider.Message{
			{Role: provider.MessageRoleSystem, Content: "system instructions"},
			{Role: provider.MessageRoleUser, Content: "review the request"},
		},
	}

	promptTokens, err := provider.EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}
	contextSize := promptTokens * 2
	safetyMargin := promptTokens + 1
	budget := ModelTokenBudget{
		ContextSize:        contextSize,
		SafetyMarginTokens: safetyMargin,
	}

	fit, err := budget.FitRequest(ctx, request)
	if err != nil {
		t.Fatalf("FitRequest() error = %v", err)
	}
	if !fit.ShouldCompact {
		t.Fatalf("FitRequest() = %+v, want compaction from the hard-limit pad", fit)
	}
	if fit.PromptUsage >= normalPromptCompactionThreshold {
		t.Fatalf("FitRequest() prompt usage = %f, want below %f", fit.PromptUsage, normalPromptCompactionThreshold)
	}
	if fit.Fits {
		t.Fatalf("FitRequest() = %+v, want hard-limit pad to force non-fit", fit)
	}
	if got, want := fit.HardLimitTokens, promptTokens-1; got != want {
		t.Fatalf("FitRequest hard limit = %d, want %d", got, want)
	}
}

func TestModelTokenBudgetCarriesRawAndCalibratedPromptTokens(t *testing.T) {
	ctx := context.Background()
	request := provider.ChatRequest{
		Model: "gpt-4o",
		Messages: []provider.Message{
			{Role: provider.MessageRoleUser, Content: strings.Repeat("calibrated prompt ", 16)},
		},
	}

	counter := provider.NewTokenCounter()
	base, err := counter.EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() before calibration error = %v", err)
	}
	counter.ObserveUsage(ctx, request, &provider.UsageStats{PromptTokens: base.RawTokens * 2})
	calibrated, err := counter.EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() after calibration error = %v", err)
	}
	if calibrated.RawTokens != base.RawTokens {
		t.Fatalf("calibrated raw tokens = %d, want %d", calibrated.RawTokens, base.RawTokens)
	}
	if calibrated.Tokens == calibrated.RawTokens {
		t.Fatalf("calibrated tokens = %d, want calibration to change raw tokens %d", calibrated.Tokens, calibrated.RawTokens)
	}

	restore := provider.SwapDefaultTokenCounter(counter)
	defer restore()

	normal, err := (ModelTokenBudget{ContextSize: calibrated.Tokens - 1}).FitRequest(ctx, request)
	if err != nil {
		t.Fatalf("FitRequest() error = %v", err)
	}
	if normal.EstimatedPromptTokens != calibrated.Tokens || normal.RawEstimatedPromptTokens != calibrated.RawTokens {
		t.Fatalf("FitRequest() prompt estimates = calibrated %d, raw %d; want calibrated %d, raw %d", normal.EstimatedPromptTokens, normal.RawEstimatedPromptTokens, calibrated.Tokens, calibrated.RawTokens)
	}
	if normal.Fits {
		t.Fatalf("FitRequest() = %+v, want calibrated estimate to prevent fit", normal)
	}

	compactionContext := calibrated.Tokens + 4 - 1
	compaction, err := (ModelTokenBudget{
		ContextSize:      compactionContext,
		SummaryMaxTokens: 4,
	}).FitCompactionRequest(ctx, request)
	if err != nil {
		t.Fatalf("FitCompactionRequest() error = %v", err)
	}
	if compaction.EstimatedPromptTokens != calibrated.Tokens || compaction.RawEstimatedPromptTokens != calibrated.RawTokens {
		t.Fatalf("FitCompactionRequest() prompt estimates = calibrated %d, raw %d; want calibrated %d, raw %d", compaction.EstimatedPromptTokens, compaction.RawEstimatedPromptTokens, calibrated.Tokens, calibrated.RawTokens)
	}
	if compaction.Fits {
		t.Fatalf("FitCompactionRequest() = %+v, want calibrated total to prevent fit", compaction)
	}
	if got, want := compaction.TotalTokens, calibrated.Tokens+4; got != want {
		t.Fatalf("FitCompactionRequest() total tokens = %d, want calibrated total %d", got, want)
	}
}
