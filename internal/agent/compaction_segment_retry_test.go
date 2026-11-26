package agent

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func TestSegmentedAppendRetryRebuildsAndAdoptsRebase(t *testing.T) {
	full := segmentStageConversation()
	providerStub := &fakeProvider{responses: []provider.ChatResponse{
		retryEmptyReply(),
		summaryResponse("rebased facts"),
	}}
	state := stateFromMessages(full)
	req := segmentedCompactionRequest(providerStub, segmentedCompactionPolicy())
	archive := &fakeHistoryArchive{note: "[steiner:history] archived"}
	req.HistoryArchive = archive
	req.CompactionLogPath = filepath.Join(t.TempDir(), "compaction.log")
	runner := &Runner{}
	applied, compacted, _, err := runner.compactConversationForBudgetWithSteering(context.Background(), req, &state, 1, nil, map[string]bool{}, new(int), "")
	if err != nil || !applied || !compacted {
		t.Fatalf("compaction applied=%t compacted=%t error=%v, want adopted retry rebase", applied, compacted, err)
	}
	if len(providerStub.requests) != 2 {
		t.Fatalf("ChatCompletion calls = %d, want 2", len(providerStub.requests))
	}
	if got, want := providerStub.requests[0].Messages[len(providerStub.requests[0].Messages)-1].Content, prompt.RenderSegmentAppendInstruction(""); got != want {
		t.Fatalf("first instruction = %q, want append instruction %q", got, want)
	}
	retry := providerStub.requests[1]
	if got, want := retry.Messages[len(retry.Messages)-1].Content, prompt.RenderConversationCompactionInstruction("", prompt.CompactionModeNormal)+"\n\n"+prompt.RenderCompactionRetryInstruction(prompt.CompactionRetry{}); got != want {
		t.Fatalf("retry instruction = %q, want ordinary rebase instruction with retry steering %q", got, want)
	}
	if !messageContentsContain(retry.Messages, "first facts") || !messageContentsContain(retry.Messages, "second facts") {
		t.Fatal("retry request omitted the existing segment chain")
	}
	for _, retained := range []string{"recent one", "recent two", "recent three"} {
		if messageContentsContain(retry.Messages, retained) {
			t.Fatalf("retry request kept retained content %q", retained)
		}
	}
	latest := state.Lineage.Generations[len(state.Lineage.Generations)-1]
	if len(latest.SummaryPrefix) == 0 || latest.SummaryPrefix[0].Segment == nil || latest.SummaryPrefix[0].Segment.Sequence != 1 {
		t.Fatalf("adopted prefix = %+v, want sequence 1 rebase", latest.SummaryPrefix)
	}
	if got := scanSegmentChain(latest.SummaryPrefix).Segments; len(got) != 1 || got[0].Segment.Sequence != 1 {
		t.Fatalf("adopted segments = %+v, want sequence 1 rebase", got)
	}
	if strings.Contains(latest.SummaryPrefix[0].Content, "span request") {
		t.Fatal("adopted rebase segment contains the append span request")
	}
	if got, want := latest.SummaryPrefix[0].Segment.SourceMessageCount, 2; got != want {
		t.Fatalf("rebased SourceMessageCount = %d, want %d", got, want)
	}
	if len(latest.Messages) != 6 {
		t.Fatalf("retained messages = %d, want 6 from the effective rebase stage", len(latest.Messages))
	}
	if len(archive.notes) != 1 || len(archive.appends) != 1 {
		t.Fatalf("archive Note calls = %d, Append calls = %d, want one Note and one adoption Append", len(archive.notes), len(archive.appends))
	}
	if got, want := len(archive.notes[0]), len(full[:5]); got != want {
		t.Fatalf("archived source count = %d, want actual rebase source count %d", got, want)
	}
	if archive.notes[0][3].Content != "span request" || archive.notes[0][4].Content != "span reply" {
		t.Fatalf("archive source tail = %+v, want the actual rebase span", archive.notes[0][3:])
	}
	if got := len(segmentDecisionLines(t, req.CompactionLogPath)); got != 1 {
		t.Fatalf("logged segment decisions = %d, want one final adopted retry decision", got)
	}
}

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
	req := segmentedCompactionRequest(providerStub, SegmentedCompactionPolicy{})
	req.CompactionLogPath = filepath.Join(t.TempDir(), "compaction.log")
	applied, compacted, _, err := (&Runner{}).compactConversationForBudgetWithSteering(context.Background(), req, &state, 1, nil, map[string]bool{}, new(int), "")
	if err != nil || !applied || !compacted {
		t.Fatalf("compaction applied=%t compacted=%t error=%v, want legacy retry adoption", applied, compacted, err)
	}
	if len(providerStub.requests) != 2 {
		t.Fatalf("summary calls = %d, want 2", len(providerStub.requests))
	}
	first, retry := providerStub.requests[0], providerStub.requests[1]
	wantRetry := retryCompactionRequest(first, prompt.CompactionRetry{}, false)
	wantRetry.RawPromptTokenEstimate = retry.RawPromptTokenEstimate
	if !reflect.DeepEqual(retry, wantRetry) {
		t.Fatalf("retry request = %#v, want full legacy retry %#v", retry, wantRetry)
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

func TestSegmentedEmptySourceRetryMakesOnlyOneRetryCall(t *testing.T) {
	state := plainFallbackState()
	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	providerStub := &fakeProvider{responses: []provider.ChatResponse{retryEmptyReply(), summaryResponse("plain retry summary")}}
	req := segmentedCompactionRequest(providerStub, segmentedCompactionPolicy())
	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 1, candidate, "")
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if len(providerStub.requests) != 2 {
		t.Fatalf("summary calls = %d, want initial call and one retry", len(providerStub.requests))
	}
	if outcome.segmentDecision == nil || outcome.segmentDecision.Reason != segmentReasonEmptySource {
		t.Fatalf("adopted decision = %+v, want plain empty-source", outcome.segmentDecision)
	}
	prefix := outcome.State.Lineage.Generations[len(outcome.State.Lineage.Generations)-1].SummaryPrefix
	if len(prefix) != 1 || prefix[0].Segment != nil {
		t.Fatalf("adopted prefix = %+v, want one plain summary and no segment metadata", prefix)
	}
}

func TestSegmentedPlainEmptySourceDropsBlockOnlyAndRetainsSummary(t *testing.T) {
	state := plainFallbackState()
	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	providerStub := oneSummaryProvider("plain body")
	req := segmentedCompactionRequest(providerStub, segmentedCompactionPolicy())
	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 1, candidate, "")
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if outcome.segmentDecision == nil || outcome.segmentDecision.Reason != segmentReasonEmptySource {
		t.Fatalf("plain outcome decision = %+v, want empty-source fallback", outcome.segmentDecision)
	}
	stages := summarizeCompactionStages{fitRunner: func(context.Context, RunRequest, RunState) (prompt.RequestTokenBudget, error) {
		return prompt.RequestTokenBudget{Fits: true}, nil
	}}
	adopted, ok := stages.adoptWithoutRequestBlock(context.Background(), req, outcome, &compactionBudgetMissError{err: errors.New("over budget")})
	if !ok {
		t.Fatal("adoptWithoutRequestBlock() ok = false, want the block-only plain summary adoption")
	}
	prefix := adopted.State.Lineage.Generations[len(adopted.State.Lineage.Generations)-1].SummaryPrefix
	if len(prefix) != 1 || prefix[0].Segment != nil || prefix[0].Content != "plain body" {
		t.Fatalf("plain summary prefix = %+v, want one preserved plain summary without request block", prefix)
	}
	if adopted.SummaryText != "plain body" || adopted.RequestBlock != output.RequestBlockDropped {
		t.Fatalf("adopted summary = %q, block status = %q, want retained body and dropped block", adopted.SummaryText, adopted.RequestBlock)
	}
}
