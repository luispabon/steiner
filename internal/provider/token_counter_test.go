package provider

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/tiktoken-go/tokenizer"
)

func TestTokenCounterUsesTiktokenStrategyByDefault(t *testing.T) {
	t.Parallel()

	counter := NewTokenCounter()
	request := ChatRequest{
		Model: "gpt-4o",
		Messages: []Message{
			{Role: MessageRoleUser, Content: "Count these tokens."},
		},
	}

	estimate, err := counter.EstimateChatRequestTokens(context.Background(), request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}
	if estimate.Strategy != TokenizerStrategyTiktoken {
		t.Fatalf("Strategy = %q, want %q", estimate.Strategy, TokenizerStrategyTiktoken)
	}
	if estimate.Confidence != "high" {
		t.Fatalf("Confidence = %q, want high", estimate.Confidence)
	}
	if estimate.Tokens <= requestOverheadTokens {
		t.Fatalf("Tokens = %d, want > %d", estimate.Tokens, requestOverheadTokens)
	}
}

func TestTokenCounterFallsBackToHeuristicStrategy(t *testing.T) {
	t.Parallel()

	counter := newTokenCounter(func(string) (tokenizer.Codec, error) {
		return nil, errors.New("boom")
	})
	request := ChatRequest{
		Model: "custom-model",
		Messages: []Message{
			{Role: MessageRoleUser, Content: "abcdefghijklmnopqrstuvwxyz"},
		},
	}

	estimate, err := counter.EstimateChatRequestTokens(context.Background(), request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}
	if estimate.Strategy != TokenizerStrategyHeuristic {
		t.Fatalf("Strategy = %q, want %q", estimate.Strategy, TokenizerStrategyHeuristic)
	}
	if estimate.Confidence != "low" {
		t.Fatalf("Confidence = %q, want low", estimate.Confidence)
	}
	if estimate.Tokens <= requestOverheadTokens {
		t.Fatalf("Tokens = %d, want > %d", estimate.Tokens, requestOverheadTokens)
	}
}

func TestTokenCounterCalibrationUpdatesCorrectionFactor(t *testing.T) {
	t.Parallel()

	counter := newTokenCounter(tokenizerForModel).(*tokenCounter)
	request := ChatRequest{
		Model: "gpt-4o",
		Messages: []Message{
			{Role: MessageRoleUser, Content: "Estimate this request."},
		},
	}

	base, err := counter.EstimateChatRequestTokens(context.Background(), request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}

	counter.ObserveUsage(context.Background(), request, &UsageStats{PromptTokens: base.Tokens * 2})

	calibration, ok := counter.calibrationForModel(request.Model)
	if !ok {
		t.Fatal("expected calibration entry")
	}
	if calibration.samples != 1 {
		t.Fatalf("samples = %d, want 1", calibration.samples)
	}
	if diff := math.Abs(calibration.factor - 1.25); diff > 0.001 {
		t.Fatalf("factor = %.3f, want about 1.25", calibration.factor)
	}
}

func TestTokenCounterCalibrationImprovesEstimate(t *testing.T) {
	t.Parallel()

	counter := newTokenCounter(tokenizerForModel)
	request := ChatRequest{
		Model: "gpt-4o",
		Messages: []Message{
			{Role: MessageRoleSystem, Content: "You are concise."},
			{Role: MessageRoleUser, Content: "Summarize this text carefully and keep structure."},
		},
	}

	base, err := counter.EstimateChatRequestTokens(context.Background(), request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}
	observed := base.Tokens * 2

	counter.ObserveUsage(context.Background(), request, &UsageStats{PromptTokens: observed})

	calibrated, err := counter.EstimateChatRequestTokens(context.Background(), request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() after calibration error = %v", err)
	}
	if calibrated.RawTokens != base.RawTokens {
		t.Fatalf("calibrated raw tokens = %d, want unchanged base raw tokens %d", calibrated.RawTokens, base.RawTokens)
	}
	if calibrated.Tokens == calibrated.RawTokens {
		t.Fatalf("calibrated tokens = %d, want calibration to change the calibrated count", calibrated.Tokens)
	}
	if calibrated.Strategy != TokenizerStrategyProviderUsage {
		t.Fatalf("Strategy = %q, want %q", calibrated.Strategy, TokenizerStrategyProviderUsage)
	}
	if calibrated.Confidence != "medium" {
		t.Fatalf("Confidence = %q, want medium", calibrated.Confidence)
	}

	baseDiff := math.Abs(float64(observed - base.Tokens))
	calibratedDiff := math.Abs(float64(observed - calibrated.Tokens))
	if calibratedDiff >= baseDiff {
		t.Fatalf("calibrated diff = %.0f, want < base diff %.0f", calibratedDiff, baseDiff)
	}
}

func TestTokenCounterCalibrationStaysStableWithCacheInclusiveUsage(t *testing.T) {
	t.Parallel()

	counter := newTokenCounter(tokenizerForModel).(*tokenCounter)
	request := ChatRequest{
		Model: "gpt-4o",
		Messages: []Message{
			{Role: MessageRoleUser, Content: strings.Repeat("Track these prompt tokens. ", 12)},
		},
	}

	base, err := counter.EstimateChatRequestTokens(context.Background(), request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}

	cacheCreationTokens := base.Tokens / 5
	if cacheCreationTokens < 1 {
		cacheCreationTokens = 1
	}
	cacheReadTokens := base.Tokens / 10
	if cacheReadTokens < 1 {
		cacheReadTokens = 1
	}

	observations := []UsageStats{
		{
			PromptTokens:             base.Tokens + cacheCreationTokens,
			CompletionTokens:         8,
			CacheCreationInputTokens: cacheCreationTokens,
		},
		{
			PromptTokens:         base.Tokens + cacheReadTokens,
			CompletionTokens:     8,
			CacheReadInputTokens: cacheReadTokens,
		},
		{
			PromptTokens:             base.Tokens + cacheCreationTokens,
			CompletionTokens:         8,
			CacheCreationInputTokens: cacheCreationTokens,
		},
		{
			PromptTokens:         base.Tokens + cacheReadTokens,
			CompletionTokens:     8,
			CacheReadInputTokens: cacheReadTokens,
		},
	}

	var previous float64
	for i, usage := range observations {
		counter.ObserveUsage(context.Background(), request, &usage)

		calibration, ok := counter.calibrationForModel(request.Model)
		if !ok {
			t.Fatal("expected calibration entry")
		}
		if calibration.samples != i+1 {
			t.Fatalf("samples = %d, want %d", calibration.samples, i+1)
		}

		if i == 0 {
			previous = calibration.factor
			continue
		}

		if diff := math.Abs(calibration.factor - previous); diff > 0.05 {
			t.Fatalf("factor swing = %.3f, want <= 0.05", diff)
		}
		previous = calibration.factor
	}
}

func TestTokenCounterCalibrationGPT5O200kEstimateStaysUnchanged(t *testing.T) {
	counter := NewTokenCounter()
	restore := SwapDefaultTokenCounter(counter)
	defer restore()

	ctx := context.Background()
	request := ChatRequest{
		Model: "gpt-5.6-luna",
		Messages: []Message{
			{Role: MessageRoleUser, Content: strings.Repeat("お誕生日おめでとう ", 64)},
		},
	}

	o200k, err := tokenizer.Get(tokenizer.O200kBase)
	if err != nil {
		t.Fatalf("tokenizer.Get(o200k_base) error = %v", err)
	}
	estimator := semanticTokenEstimator{ctx: ctx, enc: o200k}
	expected, err := estimator.countRequest(request)
	if err != nil {
		t.Fatalf("countRequest() error = %v", err)
	}

	base, err := EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}
	if base != expected {
		t.Fatalf("GPT-5 estimate = %d, want O200k estimate %d", base, expected)
	}

	observePromptTokenUsage(ctx, request, &UsageStats{PromptTokens: expected})
	got, err := EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() after calibration error = %v", err)
	}
	if got != expected {
		t.Fatalf("calibrated estimate = %d, want unchanged O200k estimate %d", got, expected)
	}
}
