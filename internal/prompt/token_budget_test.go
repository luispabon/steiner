package prompt

import (
	"context"
	"encoding/json"
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

func TestModelTokenBudgetFitRequestUsesAbsoluteCompactionThreshold(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	request := provider.ChatRequest{
		Model:    "gpt-4o",
		Messages: []provider.Message{{Role: provider.MessageRoleUser, Content: "review the request"}},
	}
	promptTokens, err := provider.EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}

	for _, tt := range []struct {
		name        string
		threshold   int
		wantCompact bool
	}{
		{name: "below", threshold: promptTokens + 1, wantCompact: false},
		{name: "at", threshold: promptTokens, wantCompact: true},
		{name: "above", threshold: promptTokens - 1, wantCompact: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			budget := ModelTokenBudget{
				ContextSize:               promptTokens * 2,
				CompactionThresholdTokens: tt.threshold,
			}
			fit, err := budget.FitRequest(ctx, request)
			if err != nil {
				t.Fatalf("FitRequest() error = %v", err)
			}
			if fit.ShouldCompact != tt.wantCompact {
				t.Fatalf("FitRequest().ShouldCompact = %v, want %v (fit=%+v)", fit.ShouldCompact, tt.wantCompact, fit)
			}
			if got, want := fit.CompactionThresholdTokens, tt.threshold; got != want {
				t.Fatalf("CompactionThresholdTokens = %d, want %d", got, want)
			}
		})
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

func TestFitRequestAndFitCompactionRequestUseSameFormula(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	request := provider.ChatRequest{
		Model: "gpt-4o",
		Messages: []provider.Message{
			{Role: provider.MessageRoleSystem, Content: "system instructions"},
			{Role: provider.MessageRoleUser, Content: "user request"},
		},
	}

	promptTokens, err := provider.EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}

	// Case: prompt fits under hard limit, but completion reserve would push total over context size.
	// Old FitRequest formula: prompt <= hardLimit (would be true)
	// Correct formula: total <= contextSize (should be false)
	hardLimit := promptTokens - 1
	completionReserve := 10
	safetyMargin := 5
	contextSize := hardLimit + safetyMargin + completionReserve - 1

	budget := ModelTokenBudget{
		ContextSize:         contextSize,
		MaxCompletionTokens: completionReserve,
		SafetyMarginTokens:  safetyMargin,
		SummaryMaxTokens:    completionReserve,
	}

	normalFit, err := budget.FitRequest(ctx, request)
	if err != nil {
		t.Fatalf("FitRequest() error = %v", err)
	}

	compactionRequest := request
	compactionFit, err := budget.FitCompactionRequest(ctx, compactionRequest)
	if err != nil {
		t.Fatalf("FitCompactionRequest() error = %v", err)
	}

	if normalFit.Fits != compactionFit.Fits {
		t.Fatalf("FitRequest.Fits = %v, FitCompactionRequest.Fits = %v, want same", normalFit.Fits, compactionFit.Fits)
	}
	if normalFit.Fits {
		t.Fatalf("Both FitRequest and FitCompactionRequest should report not fit when completion reserve + safety margin exceed context size")
	}
}

func TestFitRequestAndFitCompactionAgreeOnParamMaxTokens(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const explicit = 2048
	const summaryReserve = 8192
	baseRequest := provider.ChatRequest{
		Model:    "gpt-4o",
		Messages: []provider.Message{{Role: provider.MessageRoleUser, Content: "review the request"}},
	}

	tests := []struct {
		name      string
		configure func(*provider.ChatRequest)
	}{
		{
			name:      "params max_tokens",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_tokens": explicit} },
		},
		{
			name:      "extra params max_tokens",
			configure: func(r *provider.ChatRequest) { r.ExtraParams = map[string]any{"max_tokens": explicit} },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			request := baseRequest
			tt.configure(&request)
			// The context size is far above the explicit value and the summary
			// reserve, so only the explicit value can set the reserve.
			budget := ModelTokenBudget{
				ContextSize:            1 << 20,
				MaxCompletionTokens:    1 << 20,
				NormalSummaryMaxTokens: summaryReserve,
			}
			normal, err := budget.FitRequest(ctx, request)
			if err != nil {
				t.Fatalf("FitRequest() error = %v", err)
			}
			compaction, err := budget.FitCompactionRequest(ctx, request)
			if err != nil {
				t.Fatalf("FitCompactionRequest() error = %v", err)
			}
			if normal.ReservedCompletionTokens != explicit {
				t.Fatalf("FitRequest reserve = %d, want explicit %d", normal.ReservedCompletionTokens, explicit)
			}
			if compaction.ReservedCompletionTokens != normal.ReservedCompletionTokens {
				t.Fatalf("FitCompactionRequest reserve = %d, want same as FitRequest %d", compaction.ReservedCompletionTokens, normal.ReservedCompletionTokens)
			}
		})
	}
}

func TestFitRequestClampsUnsetCompletionReserve(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	request := provider.ChatRequest{
		Model:    "gpt-4o",
		Messages: []provider.Message{{Role: provider.MessageRoleUser, Content: "review the request"}},
	}
	promptTokens, err := provider.EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}

	tests := []struct {
		name     string
		pad      int
		wantFits bool
	}{
		{name: "minimum reserve fits", pad: minCompletionReserveTokens, wantFits: true},
		{name: "one token short does not fit", pad: minCompletionReserveTokens - 1, wantFits: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			contextSize := promptTokens + tt.pad
			budget := ModelTokenBudget{ContextSize: contextSize, MaxCompletionTokens: contextSize}
			fit, err := budget.FitRequest(ctx, request)
			if err != nil {
				t.Fatalf("FitRequest() error = %v", err)
			}
			if fit.Fits != tt.wantFits {
				t.Fatalf("Fits = %v, want %v: %+v", fit.Fits, tt.wantFits, fit)
			}
			if fit.ReservedCompletionTokens != minCompletionReserveTokens {
				t.Fatalf("ReservedCompletionTokens = %d, want %d", fit.ReservedCompletionTokens, minCompletionReserveTokens)
			}
			if tt.wantFits && fit.ShouldCompact {
				t.Fatalf("ShouldCompact = true, want false: %+v", fit)
			}
		})
	}
}

func TestFitRequestDoesNotClampExplicitMaxTokens(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseRequest := provider.ChatRequest{
		Model:    "gpt-4o",
		Messages: []provider.Message{{Role: provider.MessageRoleUser, Content: "review the request"}},
	}
	promptTokens, err := provider.EstimateChatRequestTokens(ctx, baseRequest)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}

	// explicit is above the floor, so an unwanted clamp would report minCompletionReserveTokens.
	const explicit = minCompletionReserveTokens + 1
	contextSize := promptTokens + minCompletionReserveTokens
	tests := []struct {
		name          string
		compaction    bool
		configure     func(*provider.ChatRequest)
		maxCompletion int
	}{
		{
			name: "request max tokens",
			configure: func(r *provider.ChatRequest) {
				value := explicit
				r.MaxTokens = &value
			},
			maxCompletion: contextSize,
		},
		{
			name: "extra params max_tokens",
			configure: func(r *provider.ChatRequest) {
				r.ExtraParams = map[string]any{"max_tokens": explicit}
			},
			maxCompletion: explicit,
		},
		{
			name:       "compaction request max tokens",
			compaction: true,
			configure: func(r *provider.ChatRequest) {
				value := explicit
				r.MaxTokens = &value
			},
			maxCompletion: contextSize,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			request := baseRequest
			tt.configure(&request)
			budget := ModelTokenBudget{
				ContextSize:            contextSize,
				MaxCompletionTokens:    tt.maxCompletion,
				NormalSummaryMaxTokens: contextSize,
			}
			fitFn := budget.FitRequest
			if tt.compaction {
				fitFn = budget.FitCompactionRequest
			}
			fit, err := fitFn(ctx, request)
			if err != nil {
				t.Fatalf("fit error = %v", err)
			}
			if fit.Fits {
				t.Fatalf("Fits = true, want false: %+v", fit)
			}
			if fit.ReservedCompletionTokens != explicit {
				t.Fatalf("ReservedCompletionTokens = %d, want explicit %d", fit.ReservedCompletionTokens, explicit)
			}
		})
	}
}

func TestFitRequestLowersAdvertisedOutputForExplicitParamMaxTokens(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const contextWindow = 262144
	const explicit = 8192
	baseRequest := provider.ChatRequest{
		Model:    "kimi-k2.7-code",
		Messages: []provider.Message{{Role: provider.MessageRoleUser, Content: strings.Repeat("kimi prompt ", 4000)}},
	}

	// The advertised output equals the context window, so only lowering the
	// reserve to the explicit wire value keeps the request fitting.
	tests := []struct {
		name      string
		configure func(*provider.ChatRequest)
	}{
		{
			name:      "params max_tokens",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_tokens": explicit} },
		},
		{
			name:      "params max_completion_tokens",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_completion_tokens": explicit} },
		},
		{
			name:      "params max_output_tokens",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_output_tokens": explicit} },
		},
		{
			name:      "extra params max_tokens",
			configure: func(r *provider.ChatRequest) { r.ExtraParams = map[string]any{"max_tokens": explicit} },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			request := baseRequest
			tt.configure(&request)
			budget := ModelTokenBudget{
				ContextSize:         contextWindow,
				MaxCompletionTokens: contextWindow,
				SafetyMarginTokens:  2048,
			}
			fit, err := budget.FitRequest(ctx, request)
			if err != nil {
				t.Fatalf("FitRequest() error = %v", err)
			}
			if !fit.Fits {
				t.Fatalf("Fits = false, want true: %+v", fit)
			}
			if fit.ReservedCompletionTokens != explicit {
				t.Fatalf("ReservedCompletionTokens = %d, want %d", fit.ReservedCompletionTokens, explicit)
			}
		})
	}
}

func TestFitRequestKeepsClampForNonPositiveExplicitMaxTokens(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseRequest := provider.ChatRequest{
		Model:    "gpt-4o",
		Messages: []provider.Message{{Role: provider.MessageRoleUser, Content: "review the request"}},
	}
	promptTokens, err := provider.EstimateChatRequestTokens(ctx, baseRequest)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}
	contextSize := promptTokens + minCompletionReserveTokens

	tests := []struct {
		name      string
		configure func(*provider.ChatRequest)
	}{
		{
			name: "zero request max tokens",
			configure: func(r *provider.ChatRequest) {
				value := 0
				r.MaxTokens = &value
			},
		},
		{
			name:      "zero params max_tokens",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_tokens": 0} },
		},
		{
			name:      "negative params max_tokens",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_tokens": -1} },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			request := baseRequest
			tt.configure(&request)
			budget := ModelTokenBudget{ContextSize: contextSize, MaxCompletionTokens: contextSize}
			fit, err := budget.FitRequest(ctx, request)
			if err != nil {
				t.Fatalf("FitRequest() error = %v", err)
			}
			if fit.ReservedCompletionTokens != minCompletionReserveTokens {
				t.Fatalf("ReservedCompletionTokens = %d, want clamp floor %d", fit.ReservedCompletionTokens, minCompletionReserveTokens)
			}
			if !fit.Fits {
				t.Fatalf("Fits = false, want true: %+v", fit)
			}
		})
	}
}

func TestFitRequestFitsAdvertisedOutputEqualToContextWindow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const contextWindow = 262144
	const unit = "kimi prompt "
	sample := provider.ChatRequest{
		Model:    "kimi-k2.7-code",
		Messages: []provider.Message{{Role: provider.MessageRoleUser, Content: strings.Repeat(unit, 1000)}},
	}
	sampleTokens, err := provider.EstimateChatRequestTokens(ctx, sample)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}

	// Scale the content to roughly 12k estimated prompt tokens.
	repeats := 12000 * 1000 / sampleTokens
	request := provider.ChatRequest{
		Model:    "kimi-k2.7-code",
		Messages: []provider.Message{{Role: provider.MessageRoleUser, Content: strings.Repeat(unit, repeats)}},
	}
	promptTokens, err := provider.EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}
	if promptTokens < 11000 || promptTokens > 13000 {
		t.Fatalf("prompt tokens = %d, want roughly 12000", promptTokens)
	}

	budget := ModelTokenBudget{
		ContextSize:         contextWindow,
		MaxCompletionTokens: contextWindow,
		SafetyMarginTokens:  2048,
	}
	fit, err := budget.FitRequest(ctx, request)
	if err != nil {
		t.Fatalf("FitRequest() error = %v", err)
	}
	if !fit.Fits {
		t.Fatalf("Fits = false, want true: %+v", fit)
	}
	if fit.ShouldCompact {
		t.Fatalf("ShouldCompact = true, want false: %+v", fit)
	}
	if fit.TotalTokens > contextWindow {
		t.Fatalf("TotalTokens = %d, want at most %d", fit.TotalTokens, contextWindow)
	}
}

func TestExplicitMaxTokensValue(t *testing.T) {
	t.Parallel()

	maxTokens := func(value int) *int { return &value }
	tests := []struct {
		name      string
		configure func(*provider.ChatRequest)
		want      int
		wantOK    bool
	}{
		{
			name:      "positive request max tokens",
			configure: func(r *provider.ChatRequest) { r.MaxTokens = maxTokens(2048) },
			want:      2048,
			wantOK:    true,
		},
		{
			name:      "zero request max tokens",
			configure: func(r *provider.ChatRequest) { r.MaxTokens = maxTokens(0) },
		},
		{
			name:      "positive int param",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_tokens": 4096} },
			want:      4096,
			wantOK:    true,
		},
		{
			name:      "negative int param",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_tokens": -1} },
		},
		{
			name:      "zero int param",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_tokens": 0} },
		},
		{
			name:      "positive int64 param",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_tokens": int64(4096)} },
			want:      4096,
			wantOK:    true,
		},
		{
			name:      "positive float64 param",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_tokens": 4096.0} },
			want:      4096,
			wantOK:    true,
		},
		{
			name:      "zero float64 param",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_tokens": 0.0} },
		},
		{
			name:      "positive json number param",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_tokens": json.Number("4096")} },
			want:      4096,
			wantOK:    true,
		},
		{
			name:      "invalid json number param",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_tokens": json.Number("abc")} },
		},
		{
			name:      "string param",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_tokens": "4096"} },
		},
		{
			name:      "fractional float64 param below one",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_tokens": 0.5} },
		},
		{
			name:      "fractional json number param below one",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_tokens": json.Number("0.5")} },
		},
		{
			name:      "negative float64 param",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_tokens": -4096.0} },
		},
		{
			name:      "huge float64 param",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_tokens": 1e300} },
		},
		{
			name:      "huge json number param",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_tokens": json.Number("1e300")} },
		},
		{
			name:      "huge uint64 param",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_tokens": uint64(math.MaxUint64)} },
		},
		{
			name:      "positive int32 param",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_tokens": int32(4096)} },
			want:      4096,
			wantOK:    true,
		},
		{
			name:      "positive uint param",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_tokens": uint(4096)} },
			want:      4096,
			wantOK:    true,
		},
		{
			name: "extra params win over params",
			configure: func(r *provider.ChatRequest) {
				r.Params = map[string]any{"max_tokens": 4096}
				r.ExtraParams = map[string]any{"max_tokens": 2048}
			},
			want:   2048,
			wantOK: true,
		},
		{
			name:      "max completion tokens param",
			configure: func(r *provider.ChatRequest) { r.Params = map[string]any{"max_completion_tokens": 8192} },
			want:      8192,
			wantOK:    true,
		},
		{
			name:      "max output tokens extra param",
			configure: func(r *provider.ChatRequest) { r.ExtraParams = map[string]any{"max_output_tokens": 8192} },
			want:      8192,
			wantOK:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			request := provider.ChatRequest{}
			tt.configure(&request)
			got, ok := explicitMaxTokensValue(request)
			if ok != tt.wantOK {
				t.Fatalf("explicitMaxTokensValue() ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Fatalf("explicitMaxTokensValue() = %d, want %d", got, tt.want)
			}
		})
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
