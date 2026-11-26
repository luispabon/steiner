package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

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
	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	req := segmentedCompactionRequest(providerStub, segmentedCompactionPolicy())
	archive := &fakeHistoryArchive{note: "[steiner:history] archived"}
	req.HistoryArchive = archive
	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 1, candidate, "")
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
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
	latest := outcome.State.Lineage.Generations[len(outcome.State.Lineage.Generations)-1]
	if len(latest.SummaryPrefix) == 0 || latest.SummaryPrefix[0].Segment == nil || latest.SummaryPrefix[0].Segment.Sequence != 1 {
		t.Fatalf("adopted prefix = %+v, want sequence 1 rebase", latest.SummaryPrefix)
	}
	if got := *outcome.segmentDecision; !got.Rebase || got.Sequence != 1 || got.Reason != "retry" {
		t.Fatalf("adopted decision = %+v, want retry rebase sequence 1", got)
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
	if len(archive.notes) != 1 || len(archive.appends) != 0 {
		t.Fatalf("archive Note calls = %d, Append calls = %d, want one Note after stage success and no Append before adoption", len(archive.notes), len(archive.appends))
	}
	if got, want := len(archive.notes[0]), len(full[:5]); got != want {
		t.Fatalf("archived source count = %d, want actual rebase source count %d", got, want)
	}
	if archive.notes[0][3].Content != "span request" || archive.notes[0][4].Content != "span reply" {
		t.Fatalf("archive source tail = %+v, want the actual rebase span", archive.notes[0][3:])
	}
}

func TestSegmentedRetryRebaseNoFitKeepsRetryError(t *testing.T) {
	providerStub := &fakeProvider{responses: []provider.ChatResponse{retryEmptyReply()}}
	req := segmentedCompactionRequest(providerStub, segmentedCompactionPolicy())
	plan := compactionExecutionPlan{request: provider.ChatRequest{Messages: []provider.Message{{Role: provider.MessageRoleUser, Content: "append request"}}}}
	summary := RunRequest{Provider: providerStub, ModelBudget: req.ModelBudget}
	calls, err := summarizeCompactionCallsWithRetryPlan(context.Background(), summary, 1, plan, false, func() (compactionExecutionPlan, *segmentStage, summarizeCompactionStageParams, error) {
		return compactionExecutionPlan{}, nil, summarizeCompactionStageParams{}, errCompactionFallbackNotFit
	})
	var summaryErr *compactionSummaryError
	if !errors.As(err, &summaryErr) || summaryErr.Reason != summaryFailRetryNoFit || summaryErr.Attempts != 1 || len(summaryErr.Details) != 1 {
		t.Fatalf("retry calls error = %v, want retry_does_not_fit with first attempt details", err)
	}
	if calls.response.Message.Content != "" || len(providerStub.requests) != 1 {
		t.Fatalf("response = %q, provider calls = %d, want no adoption and one request", calls.response.Message.Content, len(providerStub.requests))
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

func TestSegmentedPlainEmptySourceDropsRequestBlockWithoutDeletingSummary(t *testing.T) {
	state := plainFallbackState()
	candidate, _ := selectCompactionCandidate(state.Lineage, nil)
	providerStub := oneSummaryProvider("plain body")
	req := segmentedCompactionRequest(providerStub, segmentedCompactionPolicy())
	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 1, candidate, "")
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	blockState := outcome.State.Clone()
	latest := &blockState.Lineage.Generations[len(blockState.Lineage.Generations)-1]
	latest.SummaryPrefix[0].Content = withRequestBlock("plain body", "old request")
	blockState.Conversation = blockState.Lineage.FullMessages()
	got, _, ok := dropPlainSegmentRequestBlock(blockState, withRequestBlock("plain body", "old request"))
	if !ok {
		t.Fatal("dropPlainSegmentRequestBlock() = false, want block removed")
	}
	prefix := got.Lineage.Generations[len(got.Lineage.Generations)-1].SummaryPrefix
	if len(prefix) != 1 || prefix[0].Content != "plain body" {
		t.Fatalf("plain summary prefix = %+v, want preserved summary without request block", prefix)
	}
}
