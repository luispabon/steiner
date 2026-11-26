package agent

import (
	"context"
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
}
