package provider

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/tiktoken-go/tokenizer"
)

const (
	// TokenizerStrategyTiktoken uses a concrete tiktoken encoding for estimation.
	TokenizerStrategyTiktoken = "tiktoken"
	// TokenizerStrategyHeuristic uses fallback estimation when tokenizer loading fails.
	TokenizerStrategyHeuristic = "heuristic"
	// TokenizerStrategyProviderUsage uses provider-reported usage calibration data.
	TokenizerStrategyProviderUsage = "provider_usage"

	tokenCalibrationAlpha = 0.25
)

var defaultTokenCounter TokenCounter = NewTokenCounter()

// TokenEstimate carries an estimated token count and the strategy used.
type TokenEstimate struct {
	Tokens     int
	Strategy   string
	Confidence string
}

// TokenCounter estimates prompt tokens and calibrates estimates from provider
// usage data when available.
type TokenCounter interface {
	EstimateChatRequestTokens(ctx context.Context, request ChatRequest) (TokenEstimate, error)
	ObserveUsage(ctx context.Context, request ChatRequest, usage *UsageStats)
}

type tokenCalibration struct {
	factor  float64
	samples int
}

type tokenCounter struct {
	loadTokenizer func(string) (tokenizer.Codec, error)

	mu           sync.RWMutex
	calibrations map[string]tokenCalibration
}

// NewTokenCounter creates a token counter with in-memory provider-usage
// calibration.
func NewTokenCounter() TokenCounter {
	return newTokenCounter(tokenizerForModel)
}

func newTokenCounter(loadTokenizer func(string) (tokenizer.Codec, error)) TokenCounter {
	return &tokenCounter{
		loadTokenizer: loadTokenizer,
		calibrations:  make(map[string]tokenCalibration),
	}
}

func (c *tokenCounter) EstimateChatRequestTokens(ctx context.Context, request ChatRequest) (TokenEstimate, error) {
	baseTokens, strategy, confidence, err := c.estimateBase(ctx, request)
	if err != nil {
		return TokenEstimate{}, err
	}

	calibration, ok := c.calibrationForModel(request.Model)
	if !ok || calibration.samples == 0 || calibration.factor <= 0 {
		return TokenEstimate{
			Tokens:     baseTokens,
			Strategy:   strategy,
			Confidence: confidence,
		}, nil
	}

	adjusted := int(math.Round(float64(baseTokens) * calibration.factor))
	if baseTokens > 0 && adjusted == 0 {
		adjusted = 1
	}
	return TokenEstimate{
		Tokens:     adjusted,
		Strategy:   TokenizerStrategyProviderUsage,
		Confidence: providerUsageConfidence(calibration.samples),
	}, nil
}

func (c *tokenCounter) ObserveUsage(ctx context.Context, request ChatRequest, usage *UsageStats) {
	if usage == nil || usage.PromptTokens <= 0 {
		return
	}

	estimated, _, _, err := c.estimateBase(ctx, request)
	if err != nil || estimated <= 0 {
		return
	}

	model := calibrationKey(request.Model)
	if model == "" {
		return
	}

	ratio := float64(usage.PromptTokens) / float64(estimated)
	if ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	current := c.calibrations[model]
	if current.factor <= 0 {
		current.factor = 1
	}
	current.factor += tokenCalibrationAlpha * (ratio - current.factor)
	current.samples++
	c.calibrations[model] = current
}

func (c *tokenCounter) estimateBase(ctx context.Context, request ChatRequest) (tokens int, strategy string, confidence string, err error) {
	strategy, confidence = resolveTokenizerMetadataWithLoader(request.Model, c.loadTokenizer)
	switch strategy {
	case TokenizerStrategyHeuristic:
		tokens = estimateHeuristicChatRequestTokens(request)
	default:
		tokens, err = estimateTiktokenChatRequestTokens(ctx, request)
	}
	return tokens, strategy, confidence, err
}

func (c *tokenCounter) calibrationForModel(model string) (tokenCalibration, bool) {
	key := calibrationKey(model)
	if key == "" {
		return tokenCalibration{}, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	calibration, ok := c.calibrations[key]
	return calibration, ok
}

func calibrationKey(model string) string {
	return strings.TrimSpace(model)
}

func providerUsageConfidence(samples int) string {
	if samples >= 3 {
		return "high"
	}
	return "medium"
}

func estimateHeuristicChatRequestTokens(request ChatRequest) int {
	totalChars := 0
	for _, tool := range request.Tools {
		totalChars += len(tool.Type)
		totalChars += len(tool.Function.Name)
		totalChars += len(tool.Function.Description)
		totalChars += approximateValueChars(tool.Function.Parameters)
	}
	for _, message := range request.Messages {
		totalChars += len(string(message.Role))
		totalChars += len(message.Name)
		totalChars += len(message.ToolCallID)
		totalChars += len(message.Content)
		totalChars += len(message.ReasoningContent)
		for _, call := range message.ToolCalls {
			totalChars += len(call.ID)
			totalChars += len(call.Name)
			totalChars += approximateValueChars(call.Arguments)
		}
	}

	tokenEstimate := (totalChars + 3) / 4
	return requestOverheadTokens + tokenEstimate
}

func approximateValueChars(value any) int {
	switch typed := value.(type) {
	case nil:
		return 0
	case string:
		return len(typed)
	case bool:
		if typed {
			return len("true")
		}
		return len("false")
	case map[string]any:
		total := 0
		for key, nested := range typed {
			total += len(key)
			total += approximateValueChars(nested)
		}
		return total
	case []any:
		total := 0
		for _, nested := range typed {
			total += approximateValueChars(nested)
		}
		return total
	default:
		return len(fmt.Sprint(value))
	}
}

func observePromptTokenUsage(ctx context.Context, request ChatRequest, usage *UsageStats) {
	defaultTokenCounter.ObserveUsage(ctx, request, usage)
}
