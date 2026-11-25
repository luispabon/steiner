package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// condenseNotePresent reports whether one emitted compaction note contains want.
func condenseNotePresent(rec *eventRecorder, want string) bool {
	for _, note := range condenseNotes(rec) {
		if strings.Contains(note, want) {
			return true
		}
	}
	return false
}

// TestCondenseTruncatedSummaryCancellation proves a condense call canceled while
// it runs fails the stage and adopts nothing: the cancellation wins over the
// draft that triggered the retry.
func TestCondenseTruncatedSummaryCancellation(t *testing.T) {
	t.Parallel()
	draft := condenseDraftResponse("a long handoff summary that was cut off")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prov := &fakeProvider{}
	prov.chatFn = func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
		if len(prov.requests) == 1 {
			return draft, nil
		}
		cancel()
		return provider.ChatResponse{}, context.Canceled
	}
	summary := RunRequest{
		Provider:    prov,
		ModelBudget: prompt.ModelTokenBudget{ContextSize: 100000, MaxCompletionTokens: 128, SummaryMaxTokens: 64},
		Events:      output.NoopSink{},
	}
	calls, err := summarizeCompactionCalls(ctx, summary, 1, condensePlan(), false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("summarizeCompactionCalls() error = %v, want context.Canceled", err)
	}
	if got := calls.response.Message.Content; got != "" {
		t.Errorf("response content = %q, want the draft not adopted on cancellation", got)
	}
	if calls.truncated || calls.condensed {
		t.Errorf("condensed, truncated = %t, %t, want false, false", calls.condensed, calls.truncated)
	}
}

// TestCondenseTruncatedSummaryDoesNotFit proves a condense request the budget
// would not send is skipped: the draft is adopted with the did-not-fit note and
// no second provider call.
func TestCondenseTruncatedSummaryDoesNotFit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	plan := condensePlan()
	budgetFor := func(size int) prompt.ModelTokenBudget {
		return prompt.ModelTokenBudget{ContextSize: size, MaxCompletionTokens: 128, SummaryMaxTokens: 64}
	}
	fits := func(size int) bool {
		fit, err := budgetFor(size).FitCompactionRequest(ctx, plan.request)
		if err != nil {
			t.Fatalf("FitCompactionRequest() error = %v", err)
		}
		return fit.Fits
	}
	// Fit is monotone in the context size, so the smallest size that fits the
	// first call is the tightest one: the condense request quotes the draft and
	// is too large for it.
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
	condense := retryCompactionRequest(plan.request, prompt.CompactionRetry{
		Condense:    true,
		TargetWords: condenseTargetWords(plan.request.MaxTokens),
		Draft:       "a long handoff summary that was cut off",
	}, false)
	if fit, err := budgetFor(lo).FitCompactionRequest(ctx, condense); err != nil || fit.Fits {
		t.Fatalf("condense request fit = %t, err = %v at the tightest first-call size %d, want it not to fit", fit.Fits, err, lo)
	}
	draft := condenseDraftResponse("a long handoff summary that was cut off")
	prov := &fakeProvider{responses: []provider.ChatResponse{draft, summaryResponse("never sent")}}
	rec := &eventRecorder{}
	summary := RunRequest{Provider: prov, ModelBudget: budgetFor(lo), Events: rec.sink()}
	calls, err := summarizeCompactionCalls(ctx, summary, 1, plan, false)
	if err != nil {
		t.Fatalf("summarizeCompactionCalls() error = %v, want nil", err)
	}
	if got := len(prov.requests); got != 1 {
		t.Errorf("provider requests = %d, want 1: the condense request must not be sent", got)
	}
	if got := calls.response.Message.Content; got != draft.Message.Content {
		t.Errorf("response content = %q, want the adopted draft %q", got, draft.Message.Content)
	}
	if !calls.truncated || calls.condensed {
		t.Errorf("condensed, truncated = %t, %t, want false, true", calls.condensed, calls.truncated)
	}
	if len(calls.attempts) != 1 || calls.attempts[0].Reason != summaryTruncated {
		t.Errorf("attempts = %+v, want the single truncated draft", calls.attempts)
	}
	if !condenseNotePresent(rec, "condense retry did not fit") {
		t.Errorf("notes = %q, want one naming the condense retry did not fit", condenseNotes(rec))
	}
}

// TestCondenseTruncatedSummaryOnFallbackTarget proves the condense retry runs on
// the fallback target too, and that adopting its rewrite does not rerun the
// summary on main.
func TestCondenseTruncatedSummaryOnFallbackTarget(t *testing.T) {
	t.Parallel()
	r := newFallbackOperationRun(fallbackOperationNow)
	draft := condenseDraftResponse("normal summary draft cut off at the output limit")
	draft.Usage = &provider.UsageStats{PromptTokens: 50, CompletionTokens: 32}
	condensed := summaryResponse("condensed fallback handoff summary")
	condensed.Usage = &provider.UsageStats{PromptTokens: 50, CompletionTokens: 20}
	r.target.responses = []provider.ChatResponse{draft, condensed}

	outcome, err := r.compact(context.Background())
	if err != nil {
		t.Fatalf("compact() error = %v, want nil", err)
	}
	if got := len(r.main.requests); got != 0 {
		t.Errorf("main requests = %d, want 0: a successful fallback summary must not rerun on main", got)
	}
	if got := len(r.target.requests); got != 2 {
		t.Fatalf("target requests = %d, want 2: the summary call and one condense call", got)
	}
	if !outcome.Applied || !strings.Contains(outcome.SummaryText, "condensed fallback handoff summary") {
		t.Errorf("Applied, SummaryText = %t, %q, want applied with the condensed reply", outcome.Applied, outcome.SummaryText)
	}
	if outcome.FallbackStatus != output.CompactionFallbackUsed {
		t.Errorf("FallbackStatus = %q, want %q", outcome.FallbackStatus, output.CompactionFallbackUsed)
	}
	if final := retryFinalContent(r.target.requests[1]); !strings.Contains(final, "<truncated_summary>") || !strings.Contains(final, "normal summary draft cut off at the output limit") {
		t.Errorf("second target request lacks the condense steering and draft quote: %q", final)
	}
}

// TestCondenseTruncatedSummaryBackendState proves a condense reply that retires
// the backend state is reported, and that the condense call still resends the
// state because the retirement is known only after its reply.
func TestCondenseTruncatedSummaryBackendState(t *testing.T) {
	t.Parallel()
	draft := condenseDraftResponse("a long handoff summary that was cut off")
	condensed := summaryResponse("condensed handoff summary")
	condensed.ClearBackendState = true
	r := retryRun(draft, condensed)
	gens := r.state.Lineage.Generations
	gens[len(gens)-1].BackendState = []byte(`{"transcript":"main"}`)
	outcome, err := r.compact(context.Background())
	if err != nil {
		t.Fatalf("compact() error = %v, want nil", err)
	}
	if !outcome.BackendStateCleared {
		t.Error("BackendStateCleared = false, want true: the condense call retired the state")
	}
	if got := len(r.main.requests); got != 2 {
		t.Fatalf("main requests = %d, want 2", got)
	}
	for i, req := range r.main.requests {
		if req.BackendState == nil {
			t.Errorf("call %d carries no backend state, want it resent until the condense reply retires it", i+1)
		}
	}
	if !strings.Contains(outcome.SummaryText, "condensed handoff summary") {
		t.Errorf("SummaryText = %q, want the condensed reply", outcome.SummaryText)
	}

	direct := &fakeProvider{responses: []provider.ChatResponse{draft, condensed}}
	summary := RunRequest{
		Provider:    direct,
		ModelBudget: prompt.ModelTokenBudget{ContextSize: 100000, MaxCompletionTokens: 128, SummaryMaxTokens: 64},
		Events:      output.NoopSink{},
	}
	calls, err := summarizeCompactionCalls(context.Background(), summary, 1, condensePlan(), false)
	if err != nil {
		t.Fatalf("summarizeCompactionCalls() error = %v, want nil", err)
	}
	if !calls.cleared || !calls.condensed {
		t.Errorf("cleared, condensed = %t, %t, want true, true", calls.cleared, calls.condensed)
	}
}

// TestCondenseTruncatedSummaryNoRecoveredNote proves the condense call is not
// counted as a ladder attempt, so adopting its rewrite emits no recovered note.
func TestCondenseTruncatedSummaryNoRecoveredNote(t *testing.T) {
	t.Parallel()
	prov := &fakeProvider{responses: []provider.ChatResponse{
		condenseDraftResponse("a long handoff summary that was cut off"),
		summaryResponse("short handoff summary"),
	}}
	rec := &eventRecorder{}
	summary := RunRequest{
		Provider:    prov,
		ModelBudget: prompt.ModelTokenBudget{ContextSize: 100000, MaxCompletionTokens: 128, SummaryMaxTokens: 64},
		Events:      rec.sink(),
	}
	calls, err := summarizeCompactionCalls(context.Background(), summary, 1, condensePlan(), false)
	if err != nil {
		t.Fatalf("summarizeCompactionCalls() error = %v, want nil", err)
	}
	if got := len(prov.requests); got != 2 {
		t.Errorf("provider requests = %d, want 2", got)
	}
	if len(calls.attempts) != 2 {
		t.Errorf("attempts = %d, want 2", len(calls.attempts))
	}
	if condenseNotePresent(rec, "recovered") {
		t.Errorf("notes = %q, want no recovered note: the condense call is not a ladder attempt", condenseNotes(rec))
	}
}

// TestCondenseTruncatedSummaryOneCondenseCall proves a condense reply that is
// still truncated is adopted without another condense call: two requests total.
func TestCondenseTruncatedSummaryOneCondenseCall(t *testing.T) {
	t.Parallel()
	draft := condenseDraftResponse("a long handoff summary that was cut off")
	stillTruncated := condenseDraftResponse("a shorter handoff summary still cut off")
	prov := &fakeProvider{responses: []provider.ChatResponse{draft, stillTruncated}}
	summary := RunRequest{
		Provider:    prov,
		ModelBudget: prompt.ModelTokenBudget{ContextSize: 100000, MaxCompletionTokens: 128, SummaryMaxTokens: 64},
		Events:      output.NoopSink{},
	}
	calls, err := summarizeCompactionCalls(context.Background(), summary, 1, condensePlan(), false)
	if err != nil {
		t.Fatalf("summarizeCompactionCalls() error = %v, want nil", err)
	}
	if got := len(prov.requests); got != 2 {
		t.Fatalf("provider requests = %d, want 2: a truncated condense reply must not condense again", got)
	}
	if got := calls.response.Message.Content; got != stillTruncated.Message.Content {
		t.Errorf("response content = %q, want the truncated condense reply", got)
	}
	if !calls.condensed || !calls.truncated {
		t.Errorf("condensed, truncated = %t, %t, want true, true", calls.condensed, calls.truncated)
	}
	if strings.Contains(retryFinalContent(prov.requests[0]), "<truncated_summary>") {
		t.Error("first request carries the condense steering, want it only in the condense call")
	}
	if !strings.Contains(retryFinalContent(prov.requests[1]), "<truncated_summary>") {
		t.Error("second request lacks the condense steering")
	}
}
