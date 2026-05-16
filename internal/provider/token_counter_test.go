package provider

import (
	"context"
	"errors"
	"math"
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
