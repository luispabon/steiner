package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// TestCompactionTruncatedCondenseKeepsBothFlags proves a condense rewrite that
// hits the output limit again is reported as both truncated and condensed, and
// that its marker ends the stored summary body.
func TestCompactionTruncatedCondenseKeepsBothFlags(t *testing.T) {
	t.Parallel()
	outcome, event, _ := compactionMarkerRun(t,
		condenseDraftResponse("a long handoff summary that was cut off"),
		condenseDraftResponse("a shorter handoff summary that was cut off"),
	)
	if !outcome.Applied {
		t.Fatal("Applied = false, want the truncated rewrite to apply")
	}
	if !outcome.SummaryTruncated || !outcome.SummaryCondensed {
		t.Errorf("outcome truncated, condensed = %t, %t, want true, true", outcome.SummaryTruncated, outcome.SummaryCondensed)
	}
	if !event.SummaryTruncated || !event.SummaryCondensed {
		t.Errorf("event truncated, condensed = %t, %t, want true, true", event.SummaryTruncated, event.SummaryCondensed)
	}
	body, _, ok := SplitRequestBlock(outcome.State.Conversation[0].Content)
	if !ok {
		t.Fatalf("stored summary = %q, want a request block", outcome.State.Conversation[0].Content)
	}
	if !strings.Contains(body, "a shorter handoff summary") {
		t.Errorf("summary body = %q, want the condensed rewrite", body)
	}
	if !strings.HasSuffix(body, summaryTruncatedMarker) {
		t.Errorf("summary body = %q, want it to end with the truncation marker", body)
	}
}

// TestCompactionTruncatedMarkerAfterHistoryNote proves the marker follows the
// history note and precedes the request block, so the note stays with the body.
func TestCompactionTruncatedMarkerAfterHistoryNote(t *testing.T) {
	t.Parallel()
	r := retryRun(condenseDraftResponse("a handoff summary that was cut off"), retryEmptyReply())
	r.req.HistoryArchive = &fakeHistoryArchive{note: testHistoryNote}
	outcome, err := r.compact(context.Background())
	if err != nil {
		t.Fatalf("compact() error = %v, want nil", err)
	}
	if !outcome.SummaryTruncated {
		t.Error("SummaryTruncated = false, want true for the adopted draft")
	}
	stored := outcome.State.Conversation[0].Content
	body, request, ok := SplitRequestBlock(stored)
	if !ok {
		t.Fatalf("stored summary = %q, want a request block", stored)
	}
	wantBody := "a handoff summary that was cut off\n\n" + testHistoryNote + "\n\n" + summaryTruncatedMarker
	if body != wantBody {
		t.Errorf("summary body = %q, want %q", body, wantBody)
	}
	if request == "" {
		t.Error("request block is empty, want the verbatim request kept")
	}
	if !strings.Contains(stored, "\n\n"+requestBlockHeaderPrefix) {
		t.Errorf("stored summary = %q, want the marker before an intact request block", stored)
	}
}

// TestCompactionTruncatedFallbackSummaryIsUsed proves a truncated fallback
// summary is adopted as the fallback result, with no main-model summary call.
func TestCompactionTruncatedFallbackSummaryIsUsed(t *testing.T) {
	t.Parallel()
	r := newFallbackOperationRun(fallbackOperationNow)
	r.main.responses = nil
	r.target.responses = []provider.ChatResponse{
		condenseDraftResponse("a fallback handoff summary that was cut off"),
		retryEmptyReply(),
	}
	outcome, err := r.compact(context.Background())
	if err != nil {
		t.Fatalf("compact() error = %v, want nil", err)
	}
	if !outcome.Applied {
		t.Fatal("Applied = false, want the adopted fallback draft to apply")
	}
	if !outcome.SummaryTruncated {
		t.Error("SummaryTruncated = false, want true for the adopted fallback draft")
	}
	if outcome.SummaryCondensed {
		t.Error("SummaryCondensed = true, want false: the draft was adopted, not the condense rewrite")
	}
	if got, want := outcome.FallbackStatus, output.CompactionFallbackUsed; got != want {
		t.Errorf("FallbackStatus = %q, want %q", got, want)
	}
	if got := len(r.main.requests); got != 0 {
		t.Errorf("main summary calls = %d, want 0", got)
	}
	if got, want := len(r.target.requests), 2; got != want {
		t.Errorf("target summary calls = %d, want %d", got, want)
	}
	body, _, ok := SplitRequestBlock(outcome.State.Conversation[0].Content)
	if !ok {
		t.Fatalf("stored summary = %q, want a request block", outcome.State.Conversation[0].Content)
	}
	if !strings.Contains(body, "a fallback handoff summary that was cut off") {
		t.Errorf("summary body = %q, want the adopted fallback draft", body)
	}
	if !strings.HasSuffix(body, summaryTruncatedMarker) {
		t.Errorf("summary body = %q, want it to end with the truncation marker", body)
	}
}

// TestCompactionNormalStageAdoptedWithoutBlockKeepsMarker proves the
// block-dropping rung that adopts the normal stage keeps that stage's truncated
// flag and marker. It is reached only after a budget failure from the emergency
// stage: a non-budget emergency error fails the compaction instead, so the
// normal outcome is never adopted (see
// TestSummarizeCompactionStagesPropagatesNonBudgetErrors).
func TestCompactionNormalStageAdoptedWithoutBlockKeepsMarker(t *testing.T) {
	t.Parallel()
	fits := prompt.RequestTokenBudget{Fits: true, ContextSize: 1000, PromptUsage: 0.1}
	dense := prompt.RequestTokenBudget{Fits: true, ContextSize: 1000, PromptUsage: 0.9}
	// The emergency summary is dense on its own, so dropping its block still
	// leaves it over the threshold and only the normal draft can be adopted.
	r := retryRun(
		condenseDraftResponse("a normal handoff summary that was cut off"),
		retryEmptyReply(),
		summaryResponse("emergency "+strings.Repeat("detail ", 200)),
	)
	candidate, ok := selectCompactionCandidate(r.state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	stages := summarizeCompactionStages{
		stageRunner: summarizeCompactionStage,
		fitRunner: func(_ context.Context, _ RunRequest, state RunState) (prompt.RequestTokenBudget, error) {
			content := state.Conversation[0].Content
			if strings.Contains(content, "emergency") || strings.Contains(content, requestBlockMarker) {
				return dense, nil
			}
			return fits, nil
		},
	}
	outcome, err := stages.run(context.Background(), r.req, r.state, 5, candidate)
	if err != nil {
		t.Fatalf("run() error = %v, want the normal stage adopted without the block", err)
	}
	if got, want := outcome.RequestBlock, output.RequestBlockDropped; got != want {
		t.Errorf("RequestBlock = %q, want %q", got, want)
	}
	if !outcome.SummaryTruncated {
		t.Error("SummaryTruncated = false, want the normal stage's flag carried")
	}
	if outcome.SummaryCondensed {
		t.Error("SummaryCondensed = true, want false: the normal draft was adopted, not the condense rewrite")
	}
	if got, want := outcome.StageCount, 2; got != want {
		t.Errorf("StageCount = %d, want %d", got, want)
	}
	stored := outcome.State.Conversation[0].Content
	if strings.Contains(stored, requestBlockHeaderPrefix) {
		t.Errorf("stored summary = %q, want the request block gone", stored)
	}
	if want := "a normal handoff summary that was cut off\n\n" + summaryTruncatedMarker; stored != want {
		t.Errorf("stored summary = %q, want %q", stored, want)
	}
	if !strings.HasSuffix(outcome.SummaryText, summaryTruncatedMarker) {
		t.Errorf("SummaryText = %q, want it to still end with the truncation marker", outcome.SummaryText)
	}
}
