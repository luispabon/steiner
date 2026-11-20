package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

// compactRequestBlockOnce runs one compaction and requires it to apply.
func compactRequestBlockOnce(t *testing.T, req RunRequest, state RunState, turn int) CompactionOutcome {
	t.Helper()
	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, turn, candidate)
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if !outcome.Applied {
		t.Fatal("Compact() Applied = false, want true")
	}
	return outcome
}

// firstConversationMessage returns the request's first non-system message.
func firstConversationMessage(t *testing.T, messages []provider.Message) provider.Message {
	t.Helper()
	for _, message := range messages {
		if message.Role != provider.MessageRoleSystem {
			return message
		}
	}
	t.Fatal("request has no conversation message")
	return provider.Message{}
}

// TestCompactionSummarizerReplaysMainProjection proves that after a first
// compaction the summarizer request's first conversation message is byte
// identical to the projection a main request carries, request block included, so
// a later compaction shares the prompt cache prefix instead of missing after the
// summary message.
func TestCompactionSummarizerReplaysMainProjection(t *testing.T) {
	t.Parallel()

	prov := &fakeProvider{responses: []provider.ChatResponse{summaryResponse("first summary"), summaryResponse("second summary")}}
	req := compactionTestBudget()
	req.Provider = prov
	conv := requestBlockTurns()
	state := RunState{Conversation: conv, Lineage: newConversationLineage(conv)}

	first := compactRequestBlockOnce(t, req, state, 1)
	stored := first.State.Conversation[0]
	if body, request, ok := SplitRequestBlock(stored.Content); !ok || request != "request 3" {
		t.Fatalf("first stored summary = (%q, %q, %t), want request 3", body, request, ok)
	}

	second := compactRequestBlockOnce(t, req, first.State, 2)
	if len(prov.requests) != 2 {
		t.Fatalf("provider requests = %d, want one per compaction", len(prov.requests))
	}
	want := toProviderMessage(stored).Content
	if !strings.Contains(want, requestContextPrefix) {
		t.Fatalf("main projection = %q, want the request block", want)
	}
	got := firstConversationMessage(t, prov.requests[1].Messages).Content
	if got != want {
		t.Fatalf("summarizer first message = %q, want main's projection %q", got, want)
	}

	body, request, ok := SplitRequestBlock(second.State.Conversation[0].Content)
	if !ok || request != "request 3" {
		t.Fatalf("second stored summary = (%q, %q, %t), want the carried request", body, request, ok)
	}
	if strings.Contains(body, requestBlockMarker) {
		t.Fatalf("stored summary body = %q, want no raw request block header", body)
	}
}

// TestCompactionSummarizerEchoedInputIsNotARawHeader proves a summarizer that
// quotes its whole first conversation message keeps the replayed request block
// inside the summary projection, so the newly stored summary body carries no raw
// block header that could parse as a trailer.
func TestCompactionSummarizerEchoedInputIsNotARawHeader(t *testing.T) {
	t.Parallel()

	prov := &fakeProvider{}
	prov.chatFn = func(_ context.Context, request provider.ChatRequest) (provider.ChatResponse, error) {
		return summaryResponse(firstConversationMessage(t, request.Messages).Content), nil
	}
	req := compactionTestBudget()
	req.Provider = prov
	conv := requestBlockTurns()
	state := RunState{Conversation: conv, Lineage: newConversationLineage(conv)}

	first := compactRequestBlockOnce(t, req, state, 1)
	second := compactRequestBlockOnce(t, req, first.State, 2)
	body, request, ok := SplitRequestBlock(second.State.Conversation[0].Content)
	if !ok || request != "request 3" {
		t.Fatalf("stored summary = (%q, %q, %t), want the carried request", body, request, ok)
	}
	if strings.Contains(body, requestBlockMarker) {
		t.Fatalf("stored summary body = %q, want no raw block header", body)
	}
}
