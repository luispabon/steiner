package provider

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luispabon/ctok-go"
	"github.com/tiktoken-go/tokenizer"
)

func TestEstimateMessageTokensReturnsPositiveForContent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	msg := Message{
		Role:    MessageRoleUser,
		Content: "Hello, world! This is a test message with enough content to produce tokens.",
	}
	count, err := EstimateMessageTokens(ctx, "gpt-4o", msg)
	if err != nil {
		t.Fatalf("EstimateMessageTokens() error = %v", err)
	}
	if count <= messageOverheadTokens {
		t.Fatalf("count = %d, want > %d", count, messageOverheadTokens)
	}
	// messageOverheadTokens(4) + role "user"(1) + content(16) under o200k_base.
	const wantCount = 21
	if count != wantCount {
		t.Fatalf("count = %d, want %d", count, wantCount)
	}
}

func TestEstimateMessageTokensRespectsCancelledCtx(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	msg := Message{Role: MessageRoleUser, Content: "hello"}
	_, err := EstimateMessageTokens(ctx, "gpt-4o", msg)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestEstimateMessageTokensErrorsOnInvalidModel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	msg := Message{Role: MessageRoleUser, Content: "hello"}
	_, err := EstimateMessageTokens(ctx, "", msg)
	if err != nil {
		t.Fatalf("EstimateMessageTokens() with empty model should fallback, got error = %v", err)
	}
}

func TestEstimateToolSpecTokensReturnsPositive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tool := ToolSpec{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        "get_weather",
			Description: "Get the current weather for a location",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"location": map[string]any{
						"type":        "string",
						"description": "City name",
					},
				},
			},
		},
	}
	count, err := EstimateToolSpecTokens(ctx, "gpt-4o", tool)
	if err != nil {
		t.Fatalf("EstimateToolSpecTokens() error = %v", err)
	}
	if count <= toolSpecOverheadTokens {
		t.Fatalf("count = %d, want > %d", count, toolSpecOverheadTokens)
	}
	// toolSpecOverheadTokens(6) plus type, name, description and the nested
	// parameter map (including per-map-entry overhead) under o200k_base.
	const wantCount = 35
	if count != wantCount {
		t.Fatalf("count = %d, want %d", count, wantCount)
	}
}

func TestEstimateToolSpecTokensRespectsCancelledCtx(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := EstimateToolSpecTokens(ctx, "gpt-4o", ToolSpec{Type: "function"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// selectorWith returns a textCounterSelector that applies opts through selectTextCounter.
func selectorWith(opts counterSelectOptions) textCounterSelector {
	return func(model string) (textCounter, tokenizerMetadata, error) {
		return selectTextCounter(model, opts)
	}
}

// ctokContentCount returns the reference ctok CountContent for text.
func ctokContentCount(t *testing.T, text string) int {
	t.Helper()
	counter, err := ctok.New(ctok.V48)
	if err != nil {
		t.Fatalf("ctok.New(V48) error = %v", err)
	}
	return counter.CountContent(text)
}

func TestSelectTextCounterMatchesOnlyExactTrimmedIDs(t *testing.T) {
	ids := map[string]struct{}{"claude-test-model": {}}
	tests := []struct {
		name     string
		model    string
		wantCtok bool
	}{
		{name: "exact ID", model: "claude-test-model", wantCtok: true},
		{name: "surrounding whitespace", model: " \tclaude-test-model\n", wantCtok: true},
		{name: "case differs", model: "Claude-Test-Model"},
		{name: "alias", model: "claude-test"},
		{name: "dated ID", model: "claude-test-model-20260101"},
		{name: "latest alias", model: "claude-test-model-latest"},
		{name: "future ID", model: "claude-test-model-2"},
		{name: "empty model", model: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counter, meta, err := selectTextCounter(tt.model, counterSelectOptions{ids: ids, codecLoader: tokenizerForModel, candidate: &claudeTextCounter{}})
			if err != nil {
				t.Fatalf("selectTextCounter(%q) error = %v", tt.model, err)
			}
			_, isCtok := counter.(*claudeTextCounter)
			if isCtok != tt.wantCtok {
				t.Fatalf("selectTextCounter(%q) selected ctok = %v, want %v", tt.model, isCtok, tt.wantCtok)
			}
			wantStrategy := TokenizerStrategyTiktoken
			if tt.wantCtok {
				wantStrategy = TokenizerStrategyCtok
			}
			if meta.strategy != wantStrategy {
				t.Fatalf("strategy = %q, want %q", meta.strategy, wantStrategy)
			}
		})
	}
}

func TestSelectTextCounterLoadsCtokOnlyOnFirstCountForSelectedModels(t *testing.T) {
	loads := 0
	opts := counterSelectOptions{
		ids:         map[string]struct{}{"claude-test-model": {}},
		codecLoader: tokenizerForModel,
		candidate: &claudeTextCounter{loader: func() (*ctok.Counter, error) {
			loads++
			return ctok.New(ctok.V48)
		}},
	}

	for _, model := range []string{"gpt-4o", "claude-test"} {
		if _, _, err := selectTextCounter(model, opts); err != nil {
			t.Fatalf("selectTextCounter(%q) error = %v", model, err)
		}
	}
	if loads != 0 {
		t.Fatalf("unselected models loaded ctok %d times, want 0", loads)
	}

	counter, _, err := selectTextCounter("claude-test-model", opts)
	if err != nil {
		t.Fatalf("selectTextCounter() error = %v", err)
	}
	if loads != 0 {
		t.Fatalf("selection loaded ctok %d times, want 0 before Count", loads)
	}
	for range 2 {
		if _, err := counter.Count("hello"); err != nil {
			t.Fatalf("Count() error = %v", err)
		}
	}
	if loads != 1 {
		t.Fatalf("ctok loads = %d after two Count calls, want 1", loads)
	}
}

func TestClaudeTextCounterCountsContentWithoutMessageFraming(t *testing.T) {
	tests := []string{"", "hello", "Read a file"}
	counter := &claudeTextCounter{}
	for _, text := range tests {
		got, err := counter.Count(text)
		if err != nil {
			t.Fatalf("Count(%q) error = %v", text, err)
		}
		if want := ctokContentCount(t, text); got != want {
			t.Fatalf("Count(%q) = %d, want CountContent %d", text, got, want)
		}
	}
}

// TestClaudeTextCounterConcurrentInitialLoad checks that concurrent first Count
// calls share one ctok load and all receive the same content count.
func TestClaudeTextCounterConcurrentInitialLoad(t *testing.T) {
	const (
		callers = 8
		text    = "The quick brown fox jumps over the lazy dog."
		timeout = 10 * time.Second
	)
	want := ctokContentCount(t, text)

	var loads atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseLoad := func() { releaseOnce.Do(func() { close(release) }) }
	// Unblock the loader on every exit path so no goroutine leaks on failure.
	t.Cleanup(releaseLoad)

	counter := &claudeTextCounter{loader: func() (*ctok.Counter, error) {
		if loads.Add(1) == 1 {
			close(entered)
		}
		<-release
		return ctok.New(ctok.V48)
	}}

	type result struct {
		count int
		err   error
	}
	results := make(chan result, callers)
	for range callers {
		go func() {
			count, err := counter.Count(text)
			results <- result{count: count, err: err}
		}()
	}

	select {
	case <-entered:
	case <-time.After(timeout):
		t.Fatal("ctok load did not start")
	}
	releaseLoad()

	for i := range callers {
		select {
		case got := <-results:
			if got.err != nil {
				t.Errorf("Count() call %d error = %v", i, got.err)
			}
			if got.count != want {
				t.Errorf("Count() call %d = %d, want CountContent %d", i, got.count, want)
			}
		case <-time.After(timeout):
			t.Fatalf("Count() call %d did not return", i)
		}
	}
	if n := loads.Load(); n != 1 {
		t.Fatalf("ctok loads = %d, want 1", n)
	}
}

func TestClaudeTextCounterReturnsLoadErrorOnEveryCall(t *testing.T) {
	boom := errors.New("boom")
	loads := 0
	counter := &claudeTextCounter{loader: func() (*ctok.Counter, error) {
		loads++
		return nil, boom
	}}

	for call := range 2 {
		if _, err := counter.Count("hello"); !errors.Is(err, boom) {
			t.Fatalf("Count() call %d error = %v, want %v", call+1, err, boom)
		}
	}
	if loads != 1 {
		t.Fatalf("loader calls = %d, want 1 (a failed load is not retried)", loads)
	}
}

func TestCtokSelectedEstimatesUseContentCounts(t *testing.T) {
	opts := counterSelectOptions{
		ids:         map[string]struct{}{"claude-test-model": {}},
		codecLoader: tokenizerForModel,
		candidate:   &claudeTextCounter{},
	}
	ctx := context.Background()
	msg := Message{Role: MessageRoleUser, Content: "hello"}
	tool := ToolSpec{
		Type: "function",
		Function: ToolFunctionSpec{
			Name:        "read",
			Description: "Read a file",
		},
	}
	wantMessage := messageOverheadTokens + ctokContentCount(t, "user") + ctokContentCount(t, "hello")
	wantTool := toolSpecOverheadTokens + ctokContentCount(t, "function") + ctokContentCount(t, "read") + ctokContentCount(t, "Read a file")
	request := ChatRequest{Model: "claude-test-model", Tools: []ToolSpec{tool}, Messages: []Message{msg}}
	wantRequest := requestOverheadTokens + wantTool + wantMessage

	gotMessage, err := estimateMessageWithSelector(ctx, " claude-test-model ", msg, selectorWith(opts))
	if err != nil {
		t.Fatalf("EstimateMessageTokens() error = %v", err)
	}
	if gotMessage != wantMessage {
		t.Fatalf("EstimateMessageTokens() = %d, want %d", gotMessage, wantMessage)
	}

	gotTool, err := estimateToolSpecWithSelector(ctx, "claude-test-model", tool, selectorWith(opts))
	if err != nil {
		t.Fatalf("EstimateToolSpecTokens() error = %v", err)
	}
	if gotTool != wantTool {
		t.Fatalf("EstimateToolSpecTokens() = %d, want %d", gotTool, wantTool)
	}

	estimate, err := newTokenCounter(opts).EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}
	if estimate.Strategy != TokenizerStrategyCtok || estimate.Confidence != "low" {
		t.Fatalf("Strategy, Confidence = %q, %q; want ctok, low", estimate.Strategy, estimate.Confidence)
	}
	if estimate.RawTokens != wantRequest || estimate.Tokens != wantRequest {
		t.Fatalf("RawTokens, Tokens = %d, %d; want %d", estimate.RawTokens, estimate.Tokens, wantRequest)
	}
}

func TestCtokSelectedEstimatesReturnErrorsWithoutFallback(t *testing.T) {
	boom := errors.New("boom")
	opts := counterSelectOptions{
		ids:         map[string]struct{}{"claude-test-model": {}},
		codecLoader: tokenizerForModel,
		candidate: &claudeTextCounter{loader: func() (*ctok.Counter, error) {
			return nil, boom
		}},
	}
	ctx := context.Background()
	msg := Message{Role: MessageRoleUser, Content: "hello"}

	if _, err := estimateMessageWithSelector(ctx, "claude-test-model", msg, selectorWith(opts)); !errors.Is(err, boom) {
		t.Fatalf("estimateMessageWithSelector() error = %v, want %v", err, boom)
	}
	if _, err := estimateToolSpecWithSelector(ctx, "claude-test-model", ToolSpec{Type: "function"}, selectorWith(opts)); !errors.Is(err, boom) {
		t.Fatalf("estimateToolSpecWithSelector() error = %v, want %v", err, boom)
	}
	request := ChatRequest{Model: "claude-test-model", Messages: []Message{msg}}
	if _, err := newTokenCounter(opts).EstimateChatRequestTokens(ctx, request); !errors.Is(err, boom) {
		t.Fatalf("EstimateChatRequestTokens() error = %v, want %v", err, boom)
	}
}

func TestCtokSelectedEstimatesHonourCancelledContext(t *testing.T) {
	opts := counterSelectOptions{
		ids:         map[string]struct{}{"claude-test-model": {}},
		codecLoader: tokenizerForModel,
		candidate:   &claudeTextCounter{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := ChatRequest{Model: "claude-test-model", Messages: []Message{{Role: MessageRoleUser, Content: "hello"}}}

	if _, err := newTokenCounter(opts).EstimateChatRequestTokens(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("EstimateChatRequestTokens() error = %v, want context.Canceled", err)
	}
	if _, err := countRequestWithCounter(ctx, request, opts.candidate); !errors.Is(err, context.Canceled) {
		t.Fatalf("countRequestWithCounter() error = %v, want context.Canceled", err)
	}
}

// sortedCtokTableIDs returns the production ctok table keys in sorted order.
func sortedCtokTableIDs() []string {
	return slices.Sorted(maps.Keys(ctokActiveModelIDs))
}

func TestProductionCtokTableIsExactActiveSet(t *testing.T) {
	want := []string{"claude-haiku-5-5", "claude-opus-5-5", "claude-sonnet-5-5"}
	if got := sortedCtokTableIDs(); !slices.Equal(got, want) {
		t.Fatalf("production ctok table = %q, want %q", got, want)
	}
}

func TestProductionCtokTableKeepsTiktokenForOtherModels(t *testing.T) {
	ctx := context.Background()
	request := ChatRequest{Model: "claude-test-model", Messages: []Message{{Role: MessageRoleUser, Content: "hello"}}}
	enc, err := tokenizerForModel(request.Model)
	if err != nil {
		t.Fatalf("tokenizerForModel() error = %v", err)
	}
	want, err := countRequestWithCounter(ctx, request, enc)
	if err != nil {
		t.Fatalf("countRequestWithCounter() error = %v", err)
	}
	estimate, err := NewTokenCounter().EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}
	if estimate.Strategy != TokenizerStrategyTiktoken {
		t.Fatalf("Strategy = %q, want %q", estimate.Strategy, TokenizerStrategyTiktoken)
	}
	if estimate.RawTokens != want {
		t.Fatalf("RawTokens = %d, want tiktoken estimate %d", estimate.RawTokens, want)
	}

	// The public message, tool and metadata wrappers use the same empty table.
	msg := request.Messages[0]
	wantMessage, err := countMessageWithCounter(ctx, msg, enc)
	if err != nil {
		t.Fatalf("countMessageWithCounter() error = %v", err)
	}
	gotMessage, err := EstimateMessageTokens(ctx, request.Model, msg)
	if err != nil {
		t.Fatalf("EstimateMessageTokens() error = %v", err)
	}
	if gotMessage != wantMessage {
		t.Fatalf("EstimateMessageTokens() = %d, want tiktoken estimate %d", gotMessage, wantMessage)
	}
	tool := ToolSpec{Type: "function", Function: ToolFunctionSpec{Name: "read"}}
	wantTool, err := countToolSpecWithCounter(ctx, tool, enc)
	if err != nil {
		t.Fatalf("countToolSpecWithCounter() error = %v", err)
	}
	gotTool, err := EstimateToolSpecTokens(ctx, request.Model, tool)
	if err != nil {
		t.Fatalf("EstimateToolSpecTokens() error = %v", err)
	}
	if gotTool != wantTool {
		t.Fatalf("EstimateToolSpecTokens() = %d, want tiktoken estimate %d", gotTool, wantTool)
	}
	if strategy, _ := resolveTokenizerMetadata(request.Model); strategy != TokenizerStrategyTiktoken {
		t.Fatalf("resolveTokenizerMetadata() strategy = %q, want %q", strategy, TokenizerStrategyTiktoken)
	}
}

func TestProductionCtokMetadataIsCtokLowForActiveIDs(t *testing.T) {
	for _, id := range []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", " claude-opus-5-5 ", "\tclaude-haiku-5-5\n"} {
		strategy, confidence := resolveTokenizerMetadata(id)
		if strategy != TokenizerStrategyCtok || confidence != "low" {
			t.Errorf("resolveTokenizerMetadata(%q) = %q/%q, want ctok/low", id, strategy, confidence)
		}
	}
}

// TestProductionCtokNearMissIDsKeepEmptyTableSelection checks that IDs which are
// not exact active IDs select and count exactly as an empty ctok table would.
func TestProductionCtokNearMissIDsKeepEmptyTableSelection(t *testing.T) {
	ctx := context.Background()
	empty := counterSelectOptions{ids: map[string]struct{}{}, codecLoader: tokenizerForModel}
	for _, id := range []string{"claude-opus-5-5-x", "claude-opus-5", "Claude-Opus-5-5", "claude-opus-5_5", "claude-opus-5-5.1"} {
		_, want, err := selectTextCounter(id, empty)
		if err != nil {
			t.Fatalf("selectTextCounter(%q) error = %v", id, err)
		}
		if strategy, confidence := resolveTokenizerMetadata(id); strategy != want.strategy || confidence != want.confidence {
			t.Errorf("resolveTokenizerMetadata(%q) = %q/%q, want %q/%q", id, strategy, confidence, want.strategy, want.confidence)
		}
		request := ChatRequest{Model: id, Messages: []Message{{Role: MessageRoleUser, Content: "hello"}}}
		got, err := NewTokenCounter().EstimateChatRequestTokens(ctx, request)
		if err != nil {
			t.Fatalf("EstimateChatRequestTokens(%q) error = %v", id, err)
		}
		wantEstimate, err := newTokenCounter(empty).EstimateChatRequestTokens(ctx, request)
		if err != nil {
			t.Fatalf("empty-table EstimateChatRequestTokens(%q) error = %v", id, err)
		}
		if got.RawTokens != wantEstimate.RawTokens || got.Strategy != wantEstimate.Strategy {
			t.Errorf("%q estimate = raw %d strategy %q, want raw %d strategy %q", id, got.RawTokens, got.Strategy, wantEstimate.RawTokens, wantEstimate.Strategy)
		}
	}
}

// TestTokenCounterCtokDefaultMatchesCandidate checks that the package default
// counter and the public message and tool helpers count active IDs with the ctok
// candidate, for exact and whitespace-padded IDs.
func TestTokenCounterCtokDefaultMatchesCandidate(t *testing.T) {
	ctx := context.Background()
	request := ChatRequest{
		Messages: []Message{
			{Role: MessageRoleSystem, Content: "Reply only OK."},
			{Role: MessageRoleUser, Content: "Read notes.txt and summarize it."},
			{
				Role:      MessageRoleAssistant,
				Content:   "Calling read.",
				ToolCalls: []ToolCall{{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "notes.txt"}}},
			},
			{Role: MessageRoleTool, ToolCallID: "call_1", Name: "read", Content: "line one\nline two\n"},
		},
		Tools: []ToolSpec{{
			Type: "function",
			Function: ToolFunctionSpec{
				Name:        "read",
				Description: "Read a file from the workspace.",
				Parameters:  map[string]any{"type": "object"},
			},
		}},
	}
	candidate := &claudeTextCounter{}
	for _, model := range []string{"claude-opus-5-5", " claude-sonnet-5-5 ", "\tclaude-haiku-5-5\n"} {
		request.Model = model
		estimate, err := EstimateChatRequestTokenEstimate(ctx, request)
		if err != nil {
			t.Fatalf("EstimateChatRequestTokenEstimate(%q) error = %v", model, err)
		}
		if estimate.Strategy != TokenizerStrategyCtok || estimate.Confidence != "low" {
			t.Errorf("%q strategy/confidence = %q/%q, want ctok/low", model, estimate.Strategy, estimate.Confidence)
		}
		wantRequest, err := countRequestWithCounter(ctx, request, candidate)
		if err != nil {
			t.Fatalf("countRequestWithCounter(%q) error = %v", model, err)
		}
		if estimate.RawTokens != wantRequest {
			t.Errorf("%q RawTokens = %d, want candidate count %d", model, estimate.RawTokens, wantRequest)
		}
		for _, msg := range request.Messages {
			wantMessage, err := countMessageWithCounter(ctx, msg, candidate)
			if err != nil {
				t.Fatalf("countMessageWithCounter(%q) error = %v", model, err)
			}
			gotMessage, err := EstimateMessageTokens(ctx, model, msg)
			if err != nil {
				t.Fatalf("EstimateMessageTokens(%q) error = %v", model, err)
			}
			if gotMessage != wantMessage {
				t.Errorf("EstimateMessageTokens(%q) = %d, want candidate count %d", model, gotMessage, wantMessage)
			}
		}
		for _, tool := range request.Tools {
			wantTool, err := countToolSpecWithCounter(ctx, tool, candidate)
			if err != nil {
				t.Fatalf("countToolSpecWithCounter(%q) error = %v", model, err)
			}
			gotTool, err := EstimateToolSpecTokens(ctx, model, tool)
			if err != nil {
				t.Fatalf("EstimateToolSpecTokens(%q) error = %v", model, err)
			}
			if gotTool != wantTool {
				t.Errorf("EstimateToolSpecTokens(%q) = %d, want candidate count %d", model, gotTool, wantTool)
			}
		}
	}
}

func TestRequestOverheadTokensReturnsConstant(t *testing.T) {
	got := RequestOverheadTokens()
	if got != requestOverheadTokens {
		t.Fatalf("RequestOverheadTokens() = %d, want %d", got, requestOverheadTokens)
	}
	// Pin the magnitude too: comparing against the wrapped constant alone
	// cannot detect the constant itself drifting.
	if got != 8 {
		t.Fatalf("RequestOverheadTokens() = %d, want 8", got)
	}
}

func TestUsageTokenCountExtractsTokens(t *testing.T) {
	tests := []struct {
		name  string
		usage *UsageStats
		want  int
	}{
		{name: "nil", usage: nil, want: 0},
		{name: "total tokens", usage: &UsageStats{TotalTokens: 100}, want: 100},
		{name: "sum prompt+completion", usage: &UsageStats{PromptTokens: 30, CompletionTokens: 20}, want: 50},
		{name: "total takes precedence", usage: &UsageStats{TotalTokens: 200, PromptTokens: 30, CompletionTokens: 20}, want: 200},
		{name: "zero valued", usage: &UsageStats{}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UsageTokenCount(tt.usage); got != tt.want {
				t.Fatalf("UsageTokenCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestEncodingNameForModelMapsCorrectly(t *testing.T) {
	tests := []struct {
		model string
		want  tokenizer.Encoding
	}{
		{model: "gpt-5", want: tokenizer.O200kBase},
		{model: "gpt-5.6-luna", want: tokenizer.O200kBase},
		{model: "gpt-5-2025-08-07", want: tokenizer.O200kBase},
		{model: "gpt-4.5-preview", want: tokenizer.O200kBase},
		{model: "gpt-4.1-nano", want: tokenizer.O200kBase},
		{model: "gpt-4o", want: tokenizer.O200kBase},
		{model: "o1-mini", want: tokenizer.O200kBase},
		{model: "o3-mini", want: tokenizer.O200kBase},
		{model: "gpt-4", want: tokenizer.Cl100kBase},
		{model: "gpt-4-turbo", want: tokenizer.Cl100kBase},
		{model: "gpt-3.5-turbo", want: tokenizer.Cl100kBase},
		{model: "text-embedding-ada-002", want: tokenizer.Cl100kBase},
		{model: "text-embedding-3-small", want: tokenizer.Cl100kBase},
		{model: "text-davinci-003", want: tokenizer.P50kBase},
		{model: "code-davinci-002", want: tokenizer.P50kBase},
		{model: "code-cushman-001", want: tokenizer.P50kBase},
		{model: "davinci", want: tokenizer.R50kBase},
		{model: "curie", want: tokenizer.R50kBase},
		{model: "babbage", want: tokenizer.R50kBase},
		{model: "ada", want: tokenizer.R50kBase},
		{model: "gpt2", want: tokenizer.R50kBase},
		{model: "unknown-model", want: tokenizer.Cl100kBase},
		{model: "", want: tokenizer.Cl100kBase},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := encodingNameForModel(tt.model); got != tt.want {
				t.Fatalf("encodingNameForModel(%q) = %v, want %v", tt.model, got, tt.want)
			}

		})
	}
}

func TestEstimateMessageTokensGPT5AliasUsesO200kBase(t *testing.T) {
	ctx := context.Background()
	message := Message{
		Role:    MessageRoleUser,
		Content: "お誕生日おめでとう",
	}

	o200k, err := tokenizer.Get(tokenizer.O200kBase)
	if err != nil {
		t.Fatalf("tokenizer.Get(o200k_base) error = %v", err)
	}
	cl100k, err := tokenizer.Get(tokenizer.Cl100kBase)
	if err != nil {
		t.Fatalf("tokenizer.Get(cl100k_base) error = %v", err)
	}
	o200kRole, err := o200k.Count(string(message.Role))
	if err != nil {
		t.Fatalf("o200k.Count(role) error = %v", err)
	}
	o200kContent, err := o200k.Count(message.Content)
	if err != nil {
		t.Fatalf("o200k.Count(content) error = %v", err)
	}
	cl100kContent, err := cl100k.Count(message.Content)
	if err != nil {
		t.Fatalf("cl100k.Count(content) error = %v", err)
	}
	if o200kContent == cl100kContent {
		t.Fatalf("test content has equal token counts: o200k=%d, cl100k=%d", o200kContent, cl100kContent)
	}

	got, err := EstimateMessageTokens(ctx, "gpt-5.6-luna", message)
	if err != nil {
		t.Fatalf("EstimateMessageTokens() error = %v", err)
	}
	want := messageOverheadTokens + o200kRole + o200kContent
	if got != want {
		t.Fatalf("EstimateMessageTokens() = %d, want O200k estimate %d (Cl100k content estimate is %d)", got, want, messageOverheadTokens+o200kRole+cl100kContent)
	}
}

func TestEstimateChatRequestTokensCountsToolHeavySemanticPayload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	toolHeavy := ChatRequest{
		Model: "gpt-4o",
		Messages: []Message{
			{Role: MessageRoleSystem, Content: "system"},
			{Role: MessageRoleUser, Content: "review"},
			{
				Role:    MessageRoleAssistant,
				Content: "call tools",
				ToolCalls: []ToolCall{
					{
						ID:   "call_1",
						Name: "inspect",
						Arguments: map[string]any{
							"path":   "notes.txt",
							"filter": strings.Repeat("x", 64),
						},
					},
				},
			},
			{Role: MessageRoleTool, ToolCallID: "call_1", Name: "inspect", Content: strings.Repeat("tool result ", 12)},
		},
		Tools: []ToolSpec{
			{
				Type: "function",
				Function: ToolFunctionSpec{
					Name:        "inspect",
					Description: strings.Repeat("describe the inspection tool ", 4),
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path": map[string]any{
								"type":        "string",
								"description": strings.Repeat("filesystem path ", 4),
							},
							"filter": map[string]any{
								"type":        "string",
								"description": strings.Repeat("selector ", 6),
							},
						},
						"required": []any{"path"},
					},
				},
			},
		},
	}

	lean := toolHeavy
	lean.Tools = nil

	leanTokens, err := EstimateChatRequestTokens(ctx, lean)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens(lean) error = %v", err)
	}
	heavyTokens, err := EstimateChatRequestTokens(ctx, toolHeavy)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens(tool-heavy) error = %v", err)
	}
	if heavyTokens <= leanTokens {
		t.Fatalf("tool-heavy tokens = %d, want > lean tokens %d", heavyTokens, leanTokens)
	}
}

func TestEstimateChatRequestTokensDoesNotCountWireWrapperSyntax(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	request := ChatRequest{
		Model: "gpt-4o",
		Messages: []Message{
			{Role: MessageRoleSystem, Content: "s"},
			{Role: MessageRoleUser, Content: "u"},
			{
				Role:    MessageRoleAssistant,
				Content: "a",
				ToolCalls: []ToolCall{
					{
						ID:   "call_1",
						Name: "lookup",
						Arguments: map[string]any{
							"query": "x",
						},
					},
				},
			},
			{Role: MessageRoleTool, ToolCallID: "call_1", Name: "lookup", Content: "result"},
		},
		Tools: []ToolSpec{
			{
				Type: "function",
				Function: ToolFunctionSpec{
					Name:        "lookup",
					Description: "lookup docs",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"query": map[string]any{
								"type":        "string",
								"description": "query text",
							},
						},
					},
				},
			},
		},
	}

	semanticTokens, err := EstimateChatRequestTokens(ctx, request)
	if err != nil {
		t.Fatalf("EstimateChatRequestTokens() error = %v", err)
	}

	wire, err := chatRequestWire(request, request.Model, request.Stream)
	if err != nil {
		t.Fatalf("chatRequestWire() error = %v", err)
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("json.Marshal(wire) error = %v", err)
	}
	enc, err := tokenizerForModel(wire.Model)
	if err != nil {
		t.Fatalf("tokenizerForModel() error = %v", err)
	}
	wireTokens, err := enc.Count(string(payload))
	if err != nil {
		t.Fatalf("Count(wire) error = %v", err)
	}

	if semanticTokens >= wireTokens {
		t.Fatalf("semantic tokens = %d, want less than wire JSON tokens %d", semanticTokens, wireTokens)
	}
	if diff := wireTokens - semanticTokens; diff < 10 {
		t.Fatalf("wire JSON tokens = %d, semantic tokens = %d, want wrapper overhead gap >= 10", wireTokens, semanticTokens)
	}
}

func TestEstimateMessageTokensWithImages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		msg       Message
		minTokens int
		maxTokens int
	}{
		{
			name: "message with high-dimension image",
			msg: Message{
				Role:    MessageRoleUser,
				Content: "Describe this image",
				Images: []ImageBlock{
					{
						MediaType: "image/jpeg",
						Width:     2560,
						Height:    1545,
					},
				},
			},
			minTokens: 5000, // Expected ~5275 + 85 overhead = 5360
			maxTokens: 6000,
		},
		{
			name: "message with zero-dimension image falls back to data size",
			msg: Message{
				Role:    MessageRoleUser,
				Content: "Check this",
				Images: []ImageBlock{
					{
						MediaType: "image/png",
						Data:      strings.Repeat("ABCD", 500), // 2000 base64 chars
						Width:     0,
						Height:    0,
					},
				},
			},
			minTokens: 100, // (2000 * 3 / 4) / 4 = 375 + 85 overhead = 460
			maxTokens: 600,
		},
		{
			name: "message with no images has baseline tokens",
			msg: Message{
				Role:    MessageRoleUser,
				Content: "Just text",
			},
			minTokens: messageOverheadTokens,
			maxTokens: 50, // Text-only, minimal overhead
		},
		{
			name: "message with multiple images accumulates tokens",
			msg: Message{
				Role:    MessageRoleUser,
				Content: "Two images",
				Images: []ImageBlock{
					{
						MediaType: "image/jpeg",
						Width:     1000,
						Height:    1000,
					},
					{
						MediaType: "image/png",
						Width:     800,
						Height:    600,
					},
				},
			},
			minTokens: 2000, // (1000*1000/750) + (800*600/750) + 2*85 = 1334 + 640 + 170 = 2144
			maxTokens: 2500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			count, err := EstimateMessageTokens(ctx, "gpt-4o", tt.msg)
			if err != nil {
				t.Fatalf("EstimateMessageTokens() error = %v", err)
			}
			if count < tt.minTokens || count > tt.maxTokens {
				t.Fatalf("count = %d, want between %d and %d", count, tt.minTokens, tt.maxTokens)
			}
		})
	}
}

func TestEstimateMessageTokensImageTokensAreAdditive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	textOnlyMsg := Message{
		Role:    MessageRoleUser,
		Content: "Please analyze this for me",
	}

	textWithImageMsg := Message{
		Role:    MessageRoleUser,
		Content: "Please analyze this for me",
		Images: []ImageBlock{
			{
				MediaType: "image/jpeg",
				Width:     2560,
				Height:    1545,
			},
		},
	}

	textOnlyTokens, err := EstimateMessageTokens(ctx, "gpt-4o", textOnlyMsg)
	if err != nil {
		t.Fatalf("EstimateMessageTokens(text-only) error = %v", err)
	}

	withImageTokens, err := EstimateMessageTokens(ctx, "gpt-4o", textWithImageMsg)
	if err != nil {
		t.Fatalf("EstimateMessageTokens(with-image) error = %v", err)
	}

	// Image tokens should be clearly additive (at least 1000 tokens difference for a 2560x1545 image)
	imageDiff := withImageTokens - textOnlyTokens
	if imageDiff < 1000 {
		t.Fatalf("image tokens = %d, want >= 1000; text-only = %d, with-image = %d",
			imageDiff, textOnlyTokens, withImageTokens)
	}
}

func TestEstimateMessageTokensCountsReasoningContent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	reasoning := "Let me think through this step by step. First I need to understand the problem."
	msg := Message{
		Role:             MessageRoleAssistant,
		Content:          "The answer is 42",
		ReasoningContent: reasoning,
	}
	count, err := EstimateMessageTokens(ctx, "gpt-4o", msg)
	if err != nil {
		t.Fatalf("EstimateMessageTokens() error = %v", err)
	}

	// Get count without reasoning to compare
	msgWithoutReasoning := Message{
		Role:    MessageRoleAssistant,
		Content: "The answer is 42",
	}
	countWithoutReasoning, err := EstimateMessageTokens(ctx, "gpt-4o", msgWithoutReasoning)
	if err != nil {
		t.Fatalf("EstimateMessageTokens() error = %v", err)
	}

	// F412: ReasoningContent should add tokens
	diff := count - countWithoutReasoning
	if diff <= 0 {
		t.Fatalf("reasoning content token diff = %d, want > 0 (reasoning should add tokens)", diff)
	}
	// reasoning text is about 16 tokens, should have some positive count
	if diff < 5 {
		t.Fatalf("reasoning content token diff = %d, want >= 5", diff)
	}
}

func TestEncodingNameForModel_TrimWhitespace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		model    string
		wantEnc  tokenizer.Encoding
		wantName string
	}{
		{
			model:    "gpt-4o",
			wantEnc:  tokenizer.O200kBase,
			wantName: "o200k_base",
		},
		{
			// F413: whitespace-padded model should match correctly
			model:    "  gpt-4o  ",
			wantEnc:  tokenizer.O200kBase,
			wantName: "o200k_base",
		},
		{
			model:    "gpt-4",
			wantEnc:  tokenizer.Cl100kBase,
			wantName: "cl100k_base",
		},
		{
			// F413: whitespace-padded gpt-4 should match cl100k_base
			model:    "  gpt-4  ",
			wantEnc:  tokenizer.Cl100kBase,
			wantName: "cl100k_base",
		},
		{
			model:    "o1",
			wantEnc:  tokenizer.O200kBase,
			wantName: "o200k_base",
		},
		{
			// F413: whitespace-padded o1 should match o200k_base
			model:    "\to1\t",
			wantEnc:  tokenizer.O200kBase,
			wantName: "o200k_base",
		},
		{
			model:    "unknown-model",
			wantEnc:  tokenizer.Cl100kBase,
			wantName: "cl100k_base",
		},
		{
			// F413: whitespace-padded unknown should default to cl100k_base
			model:    "  unknown-model  ",
			wantEnc:  tokenizer.Cl100kBase,
			wantName: "cl100k_base",
		},
	}

	for _, tt := range tests {
		t.Run("model_"+strings.TrimSpace(tt.model), func(t *testing.T) {
			enc := encodingNameForModel(tt.model)
			if enc != tt.wantEnc {
				t.Fatalf("encodingNameForModel(%q) = %v, want %v", tt.model, enc, tt.wantEnc)
			}
		})
	}
}
