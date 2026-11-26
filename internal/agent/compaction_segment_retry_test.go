package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
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
	if got, want := providerStub.requests[0].Messages[len(providerStub.requests[0].Messages)-1].Content, prompt.RenderSegmentAppendInstruction("", ""); got != want {
		t.Fatalf("first instruction = %q, want append instruction %q", got, want)
	}
	retry := providerStub.requests[1]
	if got, want := retry.Messages[len(retry.Messages)-1].Content, prompt.RenderConversationCompactionInstruction("", prompt.CompactionModeNormal, "")+"\n\n"+prompt.RenderCompactionRetryInstruction(prompt.CompactionRetry{}); got != want {
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

func TestSegmentedRetryFlagOffKeepsOriginalRequestShape(t *testing.T) {
	state := stateFromMessages(segmentStageConversation())
	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	providerStub := &fakeProvider{responses: []provider.ChatResponse{retryEmptyReply(), summaryResponse("ordinary retry")}}
	req := segmentedCompactionRequest(providerStub, config.SegmentedCompactionPolicy{})
	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 1, candidate, "")
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if len(providerStub.requests) != 2 {
		t.Fatalf("summary calls = %d, want 2", len(providerStub.requests))
	}
	first, retry := providerStub.requests[0], providerStub.requests[1]
	if len(first.Messages) != len(retry.Messages) {
		t.Fatalf("retry messages = %d, first messages = %d", len(retry.Messages), len(first.Messages))
	}
	for i := 0; i < len(first.Messages)-1; i++ {
		if first.Messages[i].Content != retry.Messages[i].Content || first.Messages[i].Role != retry.Messages[i].Role {
			t.Fatalf("retry message %d differs from flag-off first request", i)
		}
	}
	if !strings.HasPrefix(retry.Messages[len(retry.Messages)-1].Content, first.Messages[len(first.Messages)-1].Content+"\n\n") {
		t.Fatal("flag-off retry changed content outside its final steering")
	}
	if outcome.segmentDecision != nil {
		t.Fatalf("flag-off outcome carries segment decision %+v", outcome.segmentDecision)
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
