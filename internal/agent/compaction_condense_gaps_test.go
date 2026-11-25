package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// condenseDraftText is the truncated draft the gaps tests quote, so a draft
// adopted unchanged can be compared by content.
const condenseDraftText = "a long handoff summary that was cut off"

// condenseStageBudget fits both the summary call and the condense call.
func condenseStageBudget() prompt.ModelTokenBudget {
	return prompt.ModelTokenBudget{ContextSize: 100000, MaxCompletionTokens: 128, SummaryMaxTokens: 64}
}

// condenseResponder answers the first summary call with draft and every condense
// call, the request that quotes the draft, with reply, or with replyErr.
func condenseResponder(draft, reply provider.ChatResponse, replyErr error) func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return func(_ context.Context, request provider.ChatRequest) (provider.ChatResponse, error) {
		if strings.Contains(retryFinalContent(request), "<"+truncatedSummaryTagName+">") {
			return reply, replyErr
		}
		return draft, nil
	}
}

// tightestCondenseBudget returns the smallest context size that still fits plan's
// first request, so the condense request, which quotes the draft, does not fit it.
func tightestCondenseBudget(t *testing.T, plan compactionExecutionPlan) prompt.ModelTokenBudget {
	t.Helper()
	budgetFor := func(size int) prompt.ModelTokenBudget {
		return prompt.ModelTokenBudget{ContextSize: size, MaxCompletionTokens: 128, SummaryMaxTokens: 64}
	}
	fits := func(size int) bool {
		fit, err := budgetFor(size).FitCompactionRequest(context.Background(), plan.request)
		if err != nil {
			t.Fatalf("FitCompactionRequest() error = %v", err)
		}
		return fit.Fits
	}
	lo, hi := 1, 1<<20
	for lo < hi {
		if mid := (lo + hi) / 2; fits(mid) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	if !fits(lo) {
		t.Fatal("no context size fits the first call")
	}
	return budgetFor(lo)
}

// TestCondenseSummaryCallsAccounting covers every condense branch that adopts
// something: usage and reported cache input sum the condense reply only when the
// condense call succeeded, and each branch keeps its own note.
func TestCondenseSummaryCallsAccounting(t *testing.T) {
	t.Parallel()
	condenseUsage := func() *provider.UsageStats {
		return &provider.UsageStats{PromptTokens: 40, CompletionTokens: 20, CacheReadInputTokens: 10, CacheCreationInputTokens: 5, CacheReported: true}
	}
	summedUsage := func() *provider.UsageStats {
		return &provider.UsageStats{PromptTokens: 140, CompletionTokens: 84, CacheReadInputTokens: 10, CacheCreationInputTokens: 5, CacheReported: true}
	}
	draftUsage := func() *provider.UsageStats {
		return &provider.UsageStats{PromptTokens: 100, CompletionTokens: 64}
	}
	emptyUsage := func() *provider.UsageStats {
		return &provider.UsageStats{PromptTokens: 10, CacheReported: true}
	}
	emptySummed := func() *provider.UsageStats {
		return &provider.UsageStats{PromptTokens: 110, CompletionTokens: 64, CacheReported: true}
	}
	tests := []struct {
		name              string
		replyContent      string
		replyFinish       string
		replyToolCalls    []provider.ToolCall
		replyUsage        *provider.UsageStats
		replyErr          error
		onFallback        bool
		tightBudget       bool
		wantUsage         *provider.UsageStats
		wantCacheReported int
		wantContent       string
		wantTruncated     bool
		wantCondensed     bool
		wantRequests      int
		wantNote          string
	}{
		{
			name: "complete condense reply sums its usage", replyContent: "short handoff summary", replyFinish: "stop", replyUsage: condenseUsage(),
			wantUsage: summedUsage(), wantCacheReported: 40, wantContent: "short handoff summary", wantCondensed: true, wantRequests: 2,
		},
		{
			name: "truncated condense reply sums its usage", replyContent: "a shorter handoff summary still cut off", replyFinish: "length", replyUsage: condenseUsage(),
			wantUsage: summedUsage(), wantCacheReported: 40, wantContent: "a shorter handoff summary still cut off", wantTruncated: true, wantCondensed: true, wantRequests: 2,
		},
		{
			name: "empty condense reply sums its usage", replyFinish: "stop", replyUsage: emptyUsage(),
			wantUsage: emptySummed(), wantCacheReported: 10, wantContent: condenseDraftText, wantTruncated: true, wantRequests: 2,
			wantNote: "condense retry returned no summary (no_text)",
		},
		{
			name: "length-only condense reply reports the length reason", replyFinish: "length", replyUsage: emptyUsage(),
			wantUsage: emptySummed(), wantCacheReported: 10, wantContent: condenseDraftText, wantTruncated: true, wantRequests: 2,
			wantNote: "condense retry returned no summary (length)",
		},
		{
			name: "tool-call-only condense reply reports the tool_calls reason", replyFinish: "tool_calls", replyToolCalls: []provider.ToolCall{retryReadCall("a.go")}, replyUsage: emptyUsage(),
			wantUsage: emptySummed(), wantCacheReported: 10, wantContent: condenseDraftText, wantTruncated: true, wantRequests: 2,
			wantNote: "condense retry returned no summary (tool_calls)",
		},
		{
			name: "failed condense call keeps only the prior usage", replyErr: &provider.HTTPError{StatusCode: 400, Status: "400 Bad Request"},
			wantUsage: draftUsage(), wantContent: condenseDraftText, wantTruncated: true,
			wantNote: "condense retry failed (http_4xx)",
		},
		{
			name: "condense request that does not fit keeps only the prior usage", tightBudget: true,
			wantUsage: draftUsage(), wantContent: condenseDraftText, wantTruncated: true, wantRequests: 1,
			wantNote: "condense retry did not fit",
		},
		{
			name: "fallback truncated condense reply is adopted", onFallback: true, replyContent: "a shorter fallback summary still cut off", replyFinish: "length", replyUsage: condenseUsage(),
			wantUsage: summedUsage(), wantCacheReported: 40, wantContent: "a shorter fallback summary still cut off", wantTruncated: true, wantCondensed: true, wantRequests: 2,
		},
		{
			name: "fallback empty condense reply adopts the draft", onFallback: true, replyFinish: "stop", replyUsage: emptyUsage(),
			wantUsage: emptySummed(), wantCacheReported: 10, wantContent: condenseDraftText, wantTruncated: true, wantRequests: 2,
			wantNote: "condense retry returned no summary (no_text)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			plan := condensePlan()
			budget := condenseStageBudget()
			if tt.tightBudget {
				budget = tightestCondenseBudget(t, plan)
			}
			draft := condenseDraftResponse(condenseDraftText)
			reply := provider.ChatResponse{
				Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: tt.replyContent, ToolCalls: tt.replyToolCalls},
				FinishReason: tt.replyFinish,
				Usage:        tt.replyUsage,
			}
			prov := &fakeProvider{chatFn: condenseResponder(draft, reply, tt.replyErr)}
			rec := &eventRecorder{}
			summary := RunRequest{Provider: prov, ModelBudget: budget, Events: rec.sink()}
			calls, err := summarizeCompactionCalls(context.Background(), summary, 1, plan, tt.onFallback)
			if err != nil {
				t.Fatalf("summarizeCompactionCalls() error = %v, want nil", err)
			}
			if got := calls.response.Message.Content; got != tt.wantContent {
				t.Errorf("response content = %q, want %q", got, tt.wantContent)
			}
			if calls.truncated != tt.wantTruncated || calls.condensed != tt.wantCondensed {
				t.Errorf("condensed, truncated = %t, %t, want %t, %t", calls.condensed, calls.truncated, tt.wantCondensed, tt.wantTruncated)
			}
			if !reflect.DeepEqual(calls.usage, tt.wantUsage) {
				t.Errorf("usage = %+v, want %+v", calls.usage, tt.wantUsage)
			}
			if calls.cacheReported != tt.wantCacheReported {
				t.Errorf("cacheReported = %d, want %d", calls.cacheReported, tt.wantCacheReported)
			}
			if tt.wantRequests > 0 {
				if got := len(prov.requests); got != tt.wantRequests {
					t.Errorf("provider requests = %d, want %d", got, tt.wantRequests)
				}
			}
			if tt.wantNote != "" && !condenseNotePresent(rec, tt.wantNote) {
				t.Errorf("notes = %q, want one containing %q", condenseNotes(rec), tt.wantNote)
			}
		})
	}
}

// TestCondenseSummaryCallsCleared proves a condense call that retires the backend
// state is reported through calls.cleared on every non-success branch, and that
// the request-scoped signal is read only for a main condense call.
func TestCondenseSummaryCallsCleared(t *testing.T) {
	// Not parallel: the signal cases stub a process-wide hook.
	tests := []struct {
		name          string
		reply         provider.ChatResponse
		replyErr      error
		onFallback    bool
		signal        bool
		wantCleared   bool
		wantContent   string
		wantCondensed bool
	}{
		{
			name: "truncated condense reply that clears backend state",
			reply: provider.ChatResponse{
				Message:           provider.Message{Role: provider.MessageRoleAssistant, Content: "a shorter handoff summary still cut off"},
				FinishReason:      "length",
				ClearBackendState: true,
			},
			wantCleared: true, wantContent: "a shorter handoff summary still cut off", wantCondensed: true,
		},
		{
			name:        "empty condense reply that clears backend state",
			reply:       provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant}, FinishReason: "stop", ClearBackendState: true},
			wantCleared: true, wantContent: condenseDraftText,
		},
		{
			name:        "condense call error marked retired",
			replyErr:    &providerRetiredSummaryError{err: errors.New("provider retired the transcript")},
			wantCleared: true, wantContent: condenseDraftText,
		},
		{
			name: "signalled retirement on a main condense call", signal: true,
			reply:       summaryResponse("short handoff summary"),
			wantCleared: true, wantContent: "short handoff summary", wantCondensed: true,
		},
		{
			name: "fallback condense call reports no signalled retirement", signal: true, onFallback: true,
			reply:       summaryResponse("short handoff summary"),
			wantCleared: false, wantContent: "short handoff summary", wantCondensed: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			draft := condenseDraftResponse(condenseDraftText)
			condenseSent := false
			if tt.signal {
				stubBackendStateRetirement(t, func(ctx context.Context) bool {
					return condenseSent && provider.HasBackendStateRetirement(ctx)
				})
			}
			prov := &fakeProvider{}
			prov.chatFn = func(_ context.Context, request provider.ChatRequest) (provider.ChatResponse, error) {
				if strings.Contains(retryFinalContent(request), "<"+truncatedSummaryTagName+">") {
					condenseSent = true
					return tt.reply, tt.replyErr
				}
				return draft, nil
			}
			rec := &eventRecorder{}
			summary := RunRequest{Provider: prov, ModelBudget: condenseStageBudget(), Events: rec.sink()}
			calls, err := summarizeCompactionCalls(context.Background(), summary, 1, condensePlan(), tt.onFallback)
			if err != nil {
				t.Fatalf("summarizeCompactionCalls() error = %v, want nil", err)
			}
			if calls.cleared != tt.wantCleared {
				t.Errorf("cleared = %t, want %t", calls.cleared, tt.wantCleared)
			}
			if got := calls.response.Message.Content; got != tt.wantContent {
				t.Errorf("response content = %q, want %q", got, tt.wantContent)
			}
			if calls.condensed != tt.wantCondensed {
				t.Errorf("condensed = %t, want %t", calls.condensed, tt.wantCondensed)
			}
		})
	}
}

// TestCondenseSummaryCallsCancelledBeforeCall proves a cancellation after the
// truncated draft is received but before the condense fit check fails the stage:
// no condense call is sent and the draft is not adopted.
func TestCondenseSummaryCallsCancelledBeforeCall(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prov := &fakeProvider{}
	prov.chatFn = func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
		if len(prov.requests) == 1 {
			cancel()
			return condenseDraftResponse(condenseDraftText), nil
		}
		return summaryResponse("never sent"), nil
	}
	summary := RunRequest{Provider: prov, ModelBudget: condenseStageBudget(), Events: (&eventRecorder{}).sink()}
	calls, err := summarizeCompactionCalls(ctx, summary, 1, condensePlan(), false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("summarizeCompactionCalls() error = %v, want context.Canceled", err)
	}
	if got := len(prov.requests); got != 1 {
		t.Errorf("provider requests = %d, want 1: the condense call must not be sent", got)
	}
	if got := calls.response.Message.Content; got != "" {
		t.Errorf("response content = %q, want nothing adopted after cancellation", got)
	}
	if calls.truncated || calls.condensed {
		t.Errorf("condensed, truncated = %t, %t, want false, false", calls.condensed, calls.truncated)
	}
}

// TestCondenseSummaryCallsCondenseDeadlineExceeded pins what a condense call that
// returns context.DeadlineExceeded on a live context does: it is not a
// cancellation, so it is classified as a timeout and the draft is adopted.
func TestCondenseSummaryCallsCondenseDeadlineExceeded(t *testing.T) {
	t.Parallel()
	draft := condenseDraftResponse(condenseDraftText)
	prov := &fakeProvider{chatFn: condenseResponder(draft, provider.ChatResponse{}, context.DeadlineExceeded)}
	rec := &eventRecorder{}
	summary := RunRequest{Provider: prov, ModelBudget: condenseStageBudget(), Events: rec.sink()}
	calls, err := summarizeCompactionCalls(context.Background(), summary, 1, condensePlan(), false)
	if err != nil {
		t.Fatalf("summarizeCompactionCalls() error = %v, want nil", err)
	}
	if got := calls.response.Message.Content; got != condenseDraftText {
		t.Errorf("response content = %q, want the adopted draft", got)
	}
	if !calls.truncated || calls.condensed {
		t.Errorf("condensed, truncated = %t, %t, want false, true", calls.condensed, calls.truncated)
	}
	if !condenseNotePresent(rec, "condense retry failed (timeout)") {
		t.Errorf("notes = %q, want one naming the timeout class", condenseNotes(rec))
	}
}

// condenseFitErrorCounter estimates requests normally except the condense request,
// whose estimate fails, so the condense fit check returns a non-cancellation error.
type condenseFitErrorCounter struct{ err error }

func (c condenseFitErrorCounter) EstimateChatRequestTokens(_ context.Context, request provider.ChatRequest) (provider.TokenEstimate, error) {
	for _, message := range request.Messages {
		if strings.Contains(message.Content, "<"+truncatedSummaryTagName+">") {
			return provider.TokenEstimate{}, c.err
		}
	}
	return provider.TokenEstimate{Tokens: 10, RawTokens: 10}, nil
}

func (condenseFitErrorCounter) ObserveUsage(context.Context, provider.ChatRequest, *provider.UsageStats) {
}

// TestCondenseSummaryCallsFitError proves a non-cancellation fit error adopts the
// draft with its own size-check note, distinct from the does-not-fit note, and
// sends no condense call.
func TestCondenseSummaryCallsFitError(t *testing.T) {
	// Not parallel: swaps the process-wide token counter.
	const wantNote = "compaction summary hit the output limit; condense retry size check failed, adopted the truncated summary"
	restore := provider.SwapDefaultTokenCounter(condenseFitErrorCounter{err: errors.New("token counter unavailable")})
	defer restore()

	draft := condenseDraftResponse(condenseDraftText)
	prov := &fakeProvider{responses: []provider.ChatResponse{draft, summaryResponse("never sent")}}
	rec := &eventRecorder{}
	summary := RunRequest{Provider: prov, ModelBudget: condenseStageBudget(), Events: rec.sink()}
	calls, err := summarizeCompactionCalls(context.Background(), summary, 1, condensePlan(), false)
	if err != nil {
		t.Fatalf("summarizeCompactionCalls() error = %v, want nil", err)
	}
	if got := len(prov.requests); got != 1 {
		t.Errorf("provider requests = %d, want 1: the condense call must not be sent", got)
	}
	if got := calls.response.Message.Content; got != condenseDraftText {
		t.Errorf("response content = %q, want the adopted draft", got)
	}
	if !calls.truncated || calls.condensed {
		t.Errorf("condensed, truncated = %t, %t, want false, true", calls.condensed, calls.truncated)
	}
	found := false
	for _, note := range condenseNotes(rec) {
		found = found || note == wantNote
		if strings.Contains(note, "condense retry did not fit") {
			t.Errorf("notes = %q, want the size-check note, not the does-not-fit note", condenseNotes(rec))
		}
	}
	if !found {
		t.Errorf("notes = %q, want %q", condenseNotes(rec), wantNote)
	}
}

// TestCondenseSummaryCallsTargetWordsFromMaxTokens proves the condense word
// target derives from the request's MaxTokens, so the emergency stage's larger
// limit asks for a different rewrite size.
func TestCondenseSummaryCallsTargetWordsFromMaxTokens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		mode          prompt.CompactionMode
		wantMaxTokens int
		wantTarget    string
	}{
		{name: "normal stage limit", mode: prompt.CompactionModeNormal, wantMaxTokens: 400, wantTarget: "in about 240 words"},
		{name: "emergency stage limit", mode: prompt.CompactionModeEmergency, wantMaxTokens: 4096, wantTarget: "in about 2457 words"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			budget := condenseStageBudget()
			budget.NormalSummaryMaxTokens = 400
			budget.EmergencySummaryMaxTokens = 4096
			maxTokens := compactionSummaryMaxTokensForMode(budget, tt.mode)
			if maxTokens != tt.wantMaxTokens {
				t.Fatalf("stage MaxTokens = %d, want %d", maxTokens, tt.wantMaxTokens)
			}
			plan := condensePlan()
			plan.request.MaxTokens = intPtr(maxTokens)
			draft := condenseDraftResponse(condenseDraftText)
			prov := &fakeProvider{chatFn: condenseResponder(draft, summaryResponse("short handoff summary"), nil)}
			summary := RunRequest{Provider: prov, ModelBudget: budget, Events: (&eventRecorder{}).sink()}
			calls, err := summarizeCompactionCalls(context.Background(), summary, 1, plan, false)
			if err != nil {
				t.Fatalf("summarizeCompactionCalls() error = %v, want nil", err)
			}
			if !calls.condensed {
				t.Fatal("condensed = false, want the rewrite adopted")
			}
			if got := len(prov.requests); got != 2 {
				t.Fatalf("provider requests = %d, want 2", got)
			}
			condenseRequest := prov.requests[1]
			if condenseRequest.MaxTokens == nil || *condenseRequest.MaxTokens != maxTokens {
				t.Errorf("condense MaxTokens = %v, want the unchanged %d", condenseRequest.MaxTokens, maxTokens)
			}
			if final := retryFinalContent(condenseRequest); !strings.Contains(final, tt.wantTarget) {
				t.Errorf("condense request = %q, want it to ask for %q", final, tt.wantTarget)
			}
		})
	}
}
