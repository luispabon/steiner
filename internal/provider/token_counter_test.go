package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/tiktoken-go/tokenizer"
)

func TestTokenizerStrategyCtokLiteral(t *testing.T) {
	if TokenizerStrategyCtok != "ctok" {
		t.Fatalf("TokenizerStrategyCtok = %q, want %q", TokenizerStrategyCtok, "ctok")
	}
}

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

	counter := newTokenCounter(counterSelectOptions{codecLoader: func(string) (tokenizer.Codec, error) {
		return nil, errors.New("boom")
	}})
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

	counter := newTokenCounter(counterSelectOptions{codecLoader: tokenizerForModel}).(*tokenCounter)
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

	counter := newTokenCounter(counterSelectOptions{codecLoader: tokenizerForModel})
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

	counter := newTokenCounter(counterSelectOptions{codecLoader: tokenizerForModel}).(*tokenCounter)
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

func TestTokenCounterCtokCalibrationKeepsRawCount(t *testing.T) {
	ctx := context.Background()
	request := ChatRequest{
		Model:    "claude-test-model",
		Messages: []Message{{Role: MessageRoleUser, Content: strings.Repeat("hello ", 32)}},
	}
	candidate := &claudeTextCounter{}
	counter := newTokenCounter(counterSelectOptions{ids: map[string]struct{}{"claude-test-model": {}}, codecLoader: tokenizerForModel, candidate: candidate}).(*tokenCounter)

	raw, err := countRequestWithCounter(ctx, request, candidate)
	if err != nil {
		t.Fatalf("countRequestWithCounter() error = %v", err)
	}
	before, err := counter.EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}
	if before.Strategy != TokenizerStrategyCtok || before.Confidence != "low" || before.Tokens != raw || before.RawTokens != raw {
		t.Fatalf("before calibration = %+v, want ctok/low with Tokens and RawTokens %d", before, raw)
	}

	counter.ObserveUsage(ctx, request, &UsageStats{PromptTokens: raw * 2})
	after, err := counter.EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() after calibration error = %v", err)
	}
	if after.RawTokens != raw {
		t.Fatalf("calibrated RawTokens = %d, want %d", after.RawTokens, raw)
	}
	// One sample moves the factor from 1 to 1 + 0.25 * (2 - 1) = 1.25.
	if want := int(math.Round(float64(raw) * 1.25)); after.Tokens != want {
		t.Fatalf("calibrated Tokens = %d, want %d", after.Tokens, want)
	}
	if after.Strategy != TokenizerStrategyProviderUsage || after.Confidence != "medium" {
		t.Fatalf("calibrated Strategy, Confidence = %q, %q; want provider_usage, medium", after.Strategy, after.Confidence)
	}

	plain := newTokenCounter(counterSelectOptions{codecLoader: tokenizerForModel})
	estimate, err := plain.EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() without injected table error = %v", err)
	}
	if estimate.Strategy != TokenizerStrategyTiktoken {
		t.Fatalf("Strategy without injected table = %q, want %q", estimate.Strategy, TokenizerStrategyTiktoken)
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

// countingCodecLoader counts codec loads and delegates to tokenizerForModel.
type countingCodecLoader struct {
	loads int
}

func (l *countingCodecLoader) load(model string) (tokenizer.Codec, error) {
	l.loads++
	return tokenizerForModel(model)
}

func TestTokenCounterObserveUsageReusesCarriedRawEstimate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	request := ChatRequest{
		Model: "gpt-4o",
		Messages: []Message{
			{Role: MessageRoleUser, Content: strings.Repeat("Carry the raw estimate. ", 16)},
		},
	}

	reference := newTokenCounter(counterSelectOptions{codecLoader: tokenizerForModel}).(*tokenCounter)
	base, err := reference.EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}
	if base.RawTokens <= 0 {
		t.Fatalf("raw estimate = %d, want > 0", base.RawTokens)
	}

	carriedRequest := request
	carriedRequest.RawPromptTokenEstimate = base.RawTokens
	loader := &countingCodecLoader{}
	carried := newTokenCounter(counterSelectOptions{codecLoader: loader.load}).(*tokenCounter)
	usage := &UsageStats{PromptTokens: base.RawTokens * 2}

	carried.ObserveUsage(ctx, carriedRequest, usage)
	reference.ObserveUsage(ctx, request, usage)

	if loader.loads != 0 {
		t.Fatalf("codec loads = %d, want 0: carried raw estimate must not be recomputed", loader.loads)
	}
	carriedCalibration, ok := carried.calibrationForModel(request.Model)
	if !ok {
		t.Fatal("expected calibration entry for carried request")
	}
	referenceCalibration, ok := reference.calibrationForModel(request.Model)
	if !ok {
		t.Fatal("expected calibration entry for reference request")
	}
	if carriedCalibration != referenceCalibration {
		t.Fatalf("carried calibration = %+v, want recomputed calibration %+v", carriedCalibration, referenceCalibration)
	}
	if diff := math.Abs(carriedCalibration.factor - 1.25); diff > 0.001 {
		t.Fatalf("factor = %.3f, want about 1.25", carriedCalibration.factor)
	}
}

func TestTokenCounterObserveUsageEstimatesWhenRawEstimateUnknown(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	request := ChatRequest{
		Model: "gpt-4o",
		Messages: []Message{
			{Role: MessageRoleUser, Content: strings.Repeat("Estimate the request. ", 16)},
		},
	}

	reference := newTokenCounter(counterSelectOptions{codecLoader: tokenizerForModel}).(*tokenCounter)
	base, err := reference.EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}
	usage := &UsageStats{PromptTokens: base.RawTokens * 2}
	reference.ObserveUsage(ctx, request, usage)
	referenceCalibration, ok := reference.calibrationForModel(request.Model)
	if !ok {
		t.Fatal("expected calibration entry for reference request")
	}

	tests := []struct {
		name  string
		carry int
	}{
		{name: "zero estimate", carry: 0},
		{name: "negative estimate", carry: -7},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidate := request
			candidate.RawPromptTokenEstimate = tc.carry
			loader := &countingCodecLoader{}
			counter := newTokenCounter(counterSelectOptions{codecLoader: loader.load}).(*tokenCounter)

			counter.ObserveUsage(ctx, candidate, usage)

			if loader.loads == 0 {
				t.Fatal("codec loads = 0, want a fresh estimate when the raw estimate is unknown")
			}
			calibration, ok := counter.calibrationForModel(request.Model)
			if !ok {
				t.Fatal("expected calibration entry")
			}
			if calibration != referenceCalibration {
				t.Fatalf("calibration = %+v, want %+v", calibration, referenceCalibration)
			}
		})
	}
}

func TestTokenCounterObserveUsageSkipsCanceledContextWithCarriedEstimate(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := ChatRequest{
		Model:                  "gpt-4o",
		Messages:               []Message{{Role: MessageRoleUser, Content: "hello"}},
		RawPromptTokenEstimate: 100,
	}
	plain := request
	plain.RawPromptTokenEstimate = 0
	usage := &UsageStats{PromptTokens: 200}

	carried := newTokenCounter(counterSelectOptions{codecLoader: tokenizerForModel}).(*tokenCounter)
	carried.ObserveUsage(ctx, request, usage)
	if _, ok := carried.calibrationForModel(request.Model); ok {
		t.Fatal("expected no calibration entry for a carried estimate after context cancellation")
	}

	fallback := newTokenCounter(counterSelectOptions{codecLoader: tokenizerForModel}).(*tokenCounter)
	fallback.ObserveUsage(ctx, plain, usage)
	if _, ok := fallback.calibrationForModel(request.Model); ok {
		t.Fatal("expected no calibration entry for a fresh estimate after context cancellation")
	}
}

func TestTokenCounterObserveUsageCanceledHeuristicFallbackMatchesEstimate(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	failingLoader := func(string) (tokenizer.Codec, error) {
		return nil, errors.New("tokenizer load failed")
	}
	request := ChatRequest{
		Model:                  "custom-model",
		Messages:               []Message{{Role: MessageRoleUser, Content: "hello"}},
		RawPromptTokenEstimate: 100,
	}
	plain := request
	plain.RawPromptTokenEstimate = 0
	usage := &UsageStats{PromptTokens: 400}

	carried := newTokenCounter(counterSelectOptions{codecLoader: failingLoader}).(*tokenCounter)
	carried.ObserveUsage(ctx, request, usage)
	baseline := newTokenCounter(counterSelectOptions{codecLoader: failingLoader}).(*tokenCounter)
	baseline.ObserveUsage(ctx, plain, usage)

	carriedCalibration, ok := carried.calibrationForModel(request.Model)
	if !ok {
		t.Fatal("expected a heuristic calibration entry for a carried estimate")
	}
	baselineCalibration, ok := baseline.calibrationForModel(request.Model)
	if !ok {
		t.Fatal("expected a heuristic calibration entry for a fresh estimate")
	}
	if carriedCalibration != baselineCalibration {
		t.Fatalf("carried calibration = %+v, want heuristic calibration %+v", carriedCalibration, baselineCalibration)
	}
	heuristic := estimateHeuristicChatRequestTokens(plain)
	wantFactor := 1 + tokenCalibrationAlpha*(float64(usage.PromptTokens)/float64(heuristic)-1)
	if diff := math.Abs(baselineCalibration.factor - wantFactor); diff > 1e-9 {
		t.Fatalf("factor = %f, want heuristic factor %f", baselineCalibration.factor, wantFactor)
	}
}

func TestChatRequestRawPromptTokenEstimateIsNotSerialized(t *testing.T) {
	t.Parallel()

	field, ok := reflect.TypeFor[ChatRequest]().FieldByName("RawPromptTokenEstimate")
	if !ok {
		t.Fatal("ChatRequest.RawPromptTokenEstimate is missing")
	}
	if got := field.Tag.Get("json"); got != "-" {
		t.Fatalf("ChatRequest.RawPromptTokenEstimate json tag = %q, want %q", got, "-")
	}

	base := ChatRequest{
		Model:    "gpt-4o",
		Messages: []Message{{Role: MessageRoleUser, Content: "hello"}},
		Tools:    []ToolSpec{{Type: "function", Function: ToolFunctionSpec{Name: "read"}}},
	}
	withEstimate := base
	withEstimate.RawPromptTokenEstimate = 12345

	baseJSON, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	withJSON, err := json.Marshal(withEstimate)
	if err != nil {
		t.Fatalf("json.Marshal() with estimate error = %v", err)
	}
	if !bytes.Equal(baseJSON, withJSON) {
		t.Fatalf("ChatRequest JSON changed with the estimate: %s vs %s", baseJSON, withJSON)
	}

	wires := []struct {
		name string
		wire interface {
			Payload(ChatRequest, bool) ([]byte, error)
		}
	}{
		{name: "openai", wire: &openaiWire{providerType: "openai"}},
		{name: "anthropic", wire: &anthropicWire{}},
	}
	for _, tc := range wires {
		want, err := tc.wire.Payload(base, false)
		if err != nil {
			t.Fatalf("%s Payload() error = %v", tc.name, err)
		}
		got, err := tc.wire.Payload(withEstimate, false)
		if err != nil {
			t.Fatalf("%s Payload() with estimate error = %v", tc.name, err)
		}
		if !bytes.Equal(want, got) {
			t.Fatalf("%s wire payload changed with the estimate: %s vs %s", tc.name, want, got)
		}
	}
}
