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

// legacyRetrySteering is the flag-off retry instruction for a reply with no
// text, pinned literally so a dropped or changed steering fails the comparison.
const legacyRetrySteering = `This is a retry of the summary request above. The previous reply did not contain a summary: it contained no text.

Do not call tools. Any tool call will be discarded and not executed. Reply with the plain-text handoff summary in the format requested above.`

// TestSegmentedRetryRebasedPlanNoFitReportsFailure pins the retry ladder's
// mapping of a rebuilt-plan fit failure to retry_does_not_fit. A retry from an
// accepted stage reuses that stage's source and only swaps the longer append
// instruction for the ordinary one, so its rebuilt plan is never larger: in the
// segmentStageConversation fixture the accepted append request first fits a
// 2734-token window while the rebuilt rebase plan fits a 2718-token window, so
// this branch is defensive. Injecting the builder error proves the mapping itself.
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
