package agent

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// legacyRetrySteering is the flag-off retry instruction for a reply with no
// text, pinned literally so a dropped or changed steering fails the comparison.
const legacyRetrySteering = `This is a retry of the summary request above. The previous reply did not contain a summary: it contained no text.

Do not call tools. Any tool call will be discarded and not executed. Reply with the plain-text handoff summary in the format requested above.`

// TestSegmentedRetryRebasedPlanNoFitReportsFailure pins the retry ladder's
// mapping of a rebuilt-plan fit failure to retry_does_not_fit. This branch is
// defensive in the segmentStageConversation fixture: the accepted append request
// first fits a 2734-token window, the unsteered rebuilt rebase plan a 2718-token
// window, and the steered retry 2771, so a real retry fails later on the steered
// fit (covered by TestSegmentedRetryRebaseNoFitKeepsStateAndReportsFailure).
// Injecting the builder error proves the mapping itself.
func TestSegmentedRetryRebasedPlanNoFitReportsFailure(t *testing.T) {
	providerStub := &fakeProvider{responses: []provider.ChatResponse{retryEmptyReply()}}
	req := segmentedCompactionRequest(providerStub, segmentedCompactionPolicy())
	plan := compactionExecutionPlan{request: provider.ChatRequest{Messages: []provider.Message{{Role: provider.MessageRoleUser, Content: "append request"}}}}
	summary := RunRequest{Provider: providerStub, ModelBudget: req.ModelBudget}
	calls, err := summarizeCompactionCallsWithRetryPlan(context.Background(), summary, 1, plan, false, func() (compactionExecutionPlan, *segmentStage, summarizeCompactionStageParams, error) {
		return compactionExecutionPlan{}, nil, summarizeCompactionStageParams{}, errCompactionFallbackNotFit
	})
	var summaryErr *compactionSummaryError
	if !errors.As(err, &summaryErr) || summaryErr.Reason != summaryFailRetryNoFit || summaryErr.Attempts != 1 || len(summaryErr.Details) != 1 {
		t.Fatalf("retry calls error = %v, want retry_does_not_fit with one attempt detail", err)
	}
	if errors.Is(err, errCompactionFallbackNotFit) {
		t.Fatal("retry calls error still reports the raw rebuilt-plan fit error")
	}
	detail := summaryErr.Details[0]
	if detail.Attempt != 1 || detail.Reason != summaryFailNoText || detail.FinishReason != "stop" || detail.ToolCalls != 0 {
		t.Fatalf("attempt detail = %+v, want the first empty reply recorded", detail)
	}
	if calls.response.Message.Content != "" || len(providerStub.requests) != 1 {
		t.Fatalf("response = %q, provider calls = %d, want no adoption and one request", calls.response.Message.Content, len(providerStub.requests))
	}
}

// TestSegmentedRetryRebaseNoFitKeepsStateAndReportsFailure drives the real
// steered no-fit branch: the append plan is accepted at the chosen window, the
// retry plan fits unsteered, and only the retry steering pushes the request over,
// so the ladder fails after rebuilding the plan rather than in the builder.
func TestSegmentedRetryRebaseNoFitKeepsStateAndReportsFailure(t *testing.T) {
	full := segmentStageConversation()
	probe := &fakeProvider{responses: []provider.ChatResponse{retryEmptyReply(), summaryResponse("rebased facts")}}
	probeReq := segmentedCompactionRequest(probe, segmentedCompactionPolicy())
	probeState := stateFromMessages(full)
	candidate, ok := selectCompactionCandidate(probeState.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	if _, err := (summarizeCompactor{}).Compact(context.Background(), probeReq, probeState, 1, candidate, ""); err != nil {
		t.Fatalf("probe Compact() error = %v", err)
	}
	if len(probe.requests) != 2 {
		t.Fatalf("probe summary calls = %d, want 2", len(probe.requests))
	}
	budget := probeReq.ModelBudget
	budget.ContextSize = 100000
	firstEstimate, err := provider.EstimateChatRequestTokens(context.Background(), probe.requests[0])
	if err != nil {
		t.Fatalf("estimate first request: %v", err)
	}
	retryEstimate, err := provider.EstimateChatRequestTokens(context.Background(), probe.requests[1])
	if err != nil {
		t.Fatalf("estimate retry request: %v", err)
	}
	fits := func(request provider.ChatRequest) bool {
		t.Helper()
		fit, err := budget.FitCompactionRequest(context.Background(), request)
		if err != nil {
			t.Fatalf("FitCompactionRequest() error = %v", err)
		}
		return fit.Fits
	}
	maxContextSize := max(firstEstimate, retryEstimate) + 512
	firstFitsAt, retryFitsAt := 0, 0
	for size := 1; size <= maxContextSize; size++ {
		budget.ContextSize = size
		if firstFitsAt == 0 && fits(probe.requests[0]) {
			firstFitsAt = size
		}
		if retryFitsAt == 0 && fits(probe.requests[1]) {
			retryFitsAt = size
		}
	}
	if firstFitsAt == 0 || retryFitsAt <= firstFitsAt {
		t.Fatalf("fit thresholds first=%d retry=%d, want append to fit before rebuilt rebase", firstFitsAt, retryFitsAt)
	}
	providerStub := &fakeProvider{responses: []provider.ChatResponse{retryEmptyReply()}}
	req := segmentedCompactionRequest(providerStub, segmentedCompactionPolicy())
	req.ModelBudget.ContextSize = retryFitsAt - 1
	state := stateFromMessages(full)
	before := state.Clone()
	archive := &fakeHistoryArchive{note: "[steiner:history] archived"}
	req.HistoryArchive = archive
	req.CompactionLogPath = filepath.Join(t.TempDir(), "compaction.log")
	var rec eventRecorder
	req.Events = rec.sink()
	_, applied, _, err := (&Runner{}).compactConversationForBudgetWithSteering(context.Background(), req, &state, 1, nil, map[string]bool{}, new(int), "")
	var summaryErr *compactionSummaryError
	if !errors.As(err, &summaryErr) || summaryErr.Reason != summaryFailRetryNoFit || summaryErr.Attempts != 1 || len(summaryErr.Details) != 1 {
		t.Fatalf("compaction error = %v, want retry_does_not_fit with first attempt details", err)
	}
	if applied || len(providerStub.requests) != 1 {
		t.Fatalf("applied = %t, provider calls = %d, want no adoption and one request", applied, len(providerStub.requests))
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatal("failed retry changed conversation or lineage")
	}
	if len(archive.notes) != 0 || len(archive.appends) != 0 {
		t.Fatalf("archive Note calls = %d, Append calls = %d, want none", len(archive.notes), len(archive.appends))
	}
	if lines := segmentDecisionLines(t, req.CompactionLogPath); len(lines) != 0 {
		t.Fatalf("segment decision log = %v, want none for failed retry", lines)
	}
	var failed bool
	for _, event := range rec.snapshot() {
		if event.Type != output.EventTypeContextDiagnostics {
			continue
		}
		payload, ok := output.AsContextCompactionEvent(event.Payload)
		if ok && payload.CompactionResult == output.CompactionResultFailed {
			failed = payload.SummaryFailureReason == summaryFailRetryNoFit && payload.SummaryAttempts == 1
		}
	}
	if !failed {
		t.Fatal("failed compaction diagnostics omitted retry_does_not_fit")
	}
}

func TestSegmentedRetryFlagOffKeepsLegacyRequestAndAdoption(t *testing.T) {
	state := stateFromMessages(segmentStageConversation())
	providerStub := &fakeProvider{responses: []provider.ChatResponse{retryEmptyReply(), summaryResponse("ordinary retry")}}
	req := segmentedCompactionRequest(providerStub, config.SegmentedCompactionPolicy{})
	req.CompactionLogPath = filepath.Join(t.TempDir(), "compaction.log")
	applied, compacted, _, err := (&Runner{}).compactConversationForBudgetWithSteering(context.Background(), req, &state, 1, nil, map[string]bool{}, new(int), "")
	if err != nil || !applied || !compacted {
		t.Fatalf("compaction applied=%t compacted=%t error=%v, want legacy retry adoption", applied, compacted, err)
	}
	if len(providerStub.requests) != 2 {
		t.Fatalf("summary calls = %d, want 2", len(providerStub.requests))
	}
	first, retry := providerStub.requests[0], providerStub.requests[1]
	wantRetry := first
	wantRetry.Messages = provider.CloneMessages(first.Messages)
	last := len(wantRetry.Messages) - 1
	wantRetry.Messages[last].Content += "\n\n" + legacyRetrySteering
	estimate, err := provider.EstimateChatRequestTokenEstimate(context.Background(), wantRetry)
	if err != nil {
		t.Fatalf("estimate expected retry: %v", err)
	}
	wantRetry.RawPromptTokenEstimate = estimate.RawTokens
	if !reflect.DeepEqual(retry, wantRetry) {
		t.Fatalf("retry request = %#v, want complete legacy request %#v", retry, wantRetry)
	}
	if len(providerCacheBoundaries(first.Messages)) != 0 || len(providerCacheBoundaries(retry.Messages)) != 0 {
		t.Fatal("flag-off legacy request has a segment cache boundary")
	}
	latest := state.Lineage.Generations[len(state.Lineage.Generations)-1]
	if len(latest.SummaryPrefix) != 1 || latest.SummaryPrefix[0].Segment != nil || !strings.Contains(latest.SummaryPrefix[0].Content, "ordinary retry") {
		t.Fatalf("adopted prefix = %+v, want one plain legacy summary", latest.SummaryPrefix)
	}
	if lines := segmentDecisionLines(t, req.CompactionLogPath); len(lines) != 0 {
		t.Fatalf("segment decision log = %v, want none with flag off", lines)
	}
}

func TestSegmentedPlainEmptySourceDropsBlockOnlyAndRetainsSummary(t *testing.T) {
	state := plainFallbackState()
	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	req := segmentedCompactionRequest(oneSummaryProvider("plain body"), segmentedCompactionPolicy())
	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 1, candidate, "")
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if outcome.segmentDecision == nil || outcome.segmentDecision.Reason != segmentReasonEmptySource {
		t.Fatalf("plain outcome decision = %+v, want empty-source fallback", outcome.segmentDecision)
	}
	if outcome.RequestBlock != output.RequestBlockCarried {
		t.Fatalf("plain outcome request block = %q, want carried", outcome.RequestBlock)
	}
	// The state below is synthetic on purpose. A summarizer must return text, so
	// it cannot produce a block-only reply, but stripping the history note or
	// other summary content can leave only the request block. Construct that
	// stored shape so the block drop is exercised against it.
	blockOnly := withRequestBlock("", "old request")
	blockState := outcome.State.Clone()
	latest := &blockState.Lineage.Generations[len(blockState.Lineage.Generations)-1]
	if len(latest.SummaryPrefix) != 1 || latest.SummaryPrefix[0].Segment != nil {
		t.Fatalf("plain summary prefix = %+v, want one plain summary", latest.SummaryPrefix)
	}
	latest.SummaryPrefix[0].Content = blockOnly
	blockState.Conversation = blockState.Lineage.FullMessages()
	outcome.State = blockState
	outcome.SummaryText = blockOnly
	if body, _, ok := SplitRequestBlock(blockOnly); !ok || body != "" {
		t.Fatalf("pre-drop summary = %q (body=%q ok=%t), want a block-only summary", blockOnly, body, ok)
	}
	stages := summarizeCompactionStages{fitRunner: func(context.Context, RunRequest, RunState) (prompt.RequestTokenBudget, error) {
		return prompt.RequestTokenBudget{Fits: true}, nil
	}}
	adopted, ok := stages.adoptWithoutRequestBlock(context.Background(), req, outcome, &compactionBudgetMissError{err: errors.New("over budget")})
	if !ok {
		t.Fatal("adoptWithoutRequestBlock() ok = false, want the block-only plain summary adoption")
	}
	prefix := adopted.State.Lineage.Generations[len(adopted.State.Lineage.Generations)-1].SummaryPrefix
	if len(prefix) != 1 || prefix[0].Segment != nil || prefix[0].Content != "" {
		t.Fatalf("plain summary prefix = %+v, want one preserved empty summary", prefix)
	}
	if adopted.SummaryText != "" || adopted.RequestBlock != output.RequestBlockDropped {
		t.Fatalf("adopted summary = %q, block status = %q, want retained empty body and dropped block", adopted.SummaryText, adopted.RequestBlock)
	}
}
