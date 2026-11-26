package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func retryNonSystemMessages(messages []provider.Message) []provider.Message {
	var filtered []provider.Message
	for _, message := range messages {
		if message.Role != provider.MessageRoleSystem {
			filtered = append(filtered, message)
		}
	}
	return filtered
}

func TestSegmentRetryAdoptsExactRebuiltSource(t *testing.T) {
	full := segmentStageConversation()
	archive := &fakeHistoryArchive{note: "[steiner:history] archived"}
	providerStub := &fakeProvider{}
	providerStub.chatFn = func(_ context.Context, request provider.ChatRequest) (provider.ChatResponse, error) {
		if len(providerStub.requests) == 2 && len(archive.notes) != 0 {
			t.Fatal("archive Note ran before the successful retry reply")
		}
		if len(providerStub.requests) == 1 {
			return retryEmptyReply(), nil
		}
		return summaryResponse("rebuilt facts"), nil
	}
	state := stateFromMessages(full)
	req := segmentedCompactionRequest(providerStub, segmentedCompactionPolicy())
	req.HistoryArchive = archive
	req.CompactionLogPath = filepath.Join(t.TempDir(), "compaction.log")
	applied, compacted, _, err := (&Runner{}).compactConversationForBudgetWithSteering(context.Background(), req, &state, 1, nil, map[string]bool{}, new(int), "")
	if err != nil || !applied || !compacted {
		t.Fatalf("compaction applied=%t compacted=%t error=%v, want adopted retry rebase", applied, compacted, err)
	}
	if len(providerStub.requests) != 2 {
		t.Fatalf("ChatCompletion calls = %d, want exactly two", len(providerStub.requests))
	}
	retry := providerStub.requests[1]
	wantInstruction := prompt.RenderConversationCompactionInstruction("", prompt.CompactionModeNormal) + "\n\n" + prompt.RenderCompactionRetryInstruction(prompt.CompactionRetry{})
	if got := retry.Messages[len(retry.Messages)-1].Content; got != wantInstruction {
		t.Fatalf("retry instruction = %q, want ordinary rebase instruction and retry steering %q", got, wantInstruction)
	}
	wantSource := []string{"first facts", "second facts", "archived to sessions/old.jsonl", "span request", "span reply"}
	var gotSource []string
	for _, message := range retry.Messages[:len(retry.Messages)-1] {
		if message.Role != provider.MessageRoleSystem {
			gotSource = append(gotSource, message.Content)
		}
	}
	if len(gotSource) != len(wantSource) {
		t.Fatalf("retry source = %q, want rebuilt source %q", gotSource, wantSource)
	}
	for i := range wantSource {
		if !strings.Contains(gotSource[i], wantSource[i]) {
			t.Fatalf("retry source[%d] = %q, want content for %q", i, gotSource[i], wantSource[i])
		}
	}
	for _, retained := range []string{"recent one", "recent two", "recent three"} {
		if messageContentsContain(retry.Messages, retained) {
			t.Fatalf("retry request included retained suffix %q", retained)
		}
	}
	latest := state.Lineage.Generations[len(state.Lineage.Generations)-1]
	if len(latest.SummaryPrefix) != 2 || latest.SummaryPrefix[0].Segment == nil {
		t.Fatalf("rebuilt prefix = %+v, want a segment and regenerated tail", latest.SummaryPrefix)
	}
	segment := latest.SummaryPrefix[0]
	if segment.Segment.Sequence != 1 || segment.Segment.SourceMessageCount != 2 || !strings.Contains(segment.Content, "rebuilt facts") {
		t.Fatalf("rebuilt segment = %+v, want sequence 1, count 2, and retry summary", segment)
	}
	body, request, ok := SplitRequestBlock(latest.SummaryPrefix[1].Content)
	if !ok || body != archive.note || request != "span request" {
		t.Fatalf("regenerated tail = %q (body=%q request=%q ok=%t), want note and exact request block", latest.SummaryPrefix[1].Content, body, request, ok)
	}
	if len(archive.notes) != 1 || len(archive.appends) != 1 {
		t.Fatalf("archive Note calls=%d Append calls=%d, want one each", len(archive.notes), len(archive.appends))
	}
	wantArchive := stripRequestBlocks(full[:5])
	if !sameMessages(archive.notes[0], wantArchive) || !sameMessages(archive.appends[0], wantArchive) {
		t.Fatalf("archive Note input=%#v Append input=%#v, want exact rebuilt source %#v", archive.notes[0], archive.appends[0], wantArchive)
	}
	lines := segmentDecisionLines(t, req.CompactionLogPath)
	wantLine := "segment_decision action=rebase reason=retry sequence=1"
	if len(lines) != 1 || !strings.HasPrefix(lines[0], wantLine) {
		t.Fatalf("segment decisions = %q, want exactly one retry rebase decision", lines)
	}
}

func TestSegmentRetryZeroCountPlainSourceDoesNotRepeat(t *testing.T) {
	state := plainFallbackState()
	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	archive := &fakeHistoryArchive{note: "[steiner:history] archived"}
	providerStub := &fakeProvider{responses: []provider.ChatResponse{retryEmptyReply(), summaryResponse("plain retry")}}
	req := segmentedCompactionRequest(providerStub, segmentedCompactionPolicy())
	req.HistoryArchive = archive
	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 1, candidate, "")
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if len(providerStub.requests) != 2 {
		t.Fatalf("ChatCompletion calls = %d, want one retry only", len(providerStub.requests))
	}
	retry := providerStub.requests[1]
	wantInstruction := prompt.RenderConversationCompactionInstruction("", prompt.CompactionModeNormal) + "\n\n" + prompt.RenderCompactionRetryInstruction(prompt.CompactionRetry{})
	if got := retry.Messages[len(retry.Messages)-1].Content; got != wantInstruction {
		t.Fatalf("retry instruction = %q, want ordinary instruction with retry steering", got)
	}
	if !messageContentsContain(retry.Messages, "first facts") || !messageContentsContain(retry.Messages, "second facts") {
		t.Fatal("plain retry omitted its summary source")
	}
	if len(archive.notes) != 1 || len(archive.appends) != 0 || len(outcome.pendingArchive) != 1 {
		t.Fatalf("plain retry archive Note=%d Append=%d pending=%d, want one Note, deferred Append, and one pending source", len(archive.notes), len(archive.appends), len(outcome.pendingArchive))
	}
	if outcome.segmentDecision == nil || outcome.segmentDecision.Reason != segmentReasonEmptySource || len(outcome.State.Lineage.Generations[len(outcome.State.Lineage.Generations)-1].SummaryPrefix) != 1 {
		t.Fatalf("plain retry outcome decision=%+v", outcome.segmentDecision)
	}
}

func TestSegmentRetryZeroCountSplitClampAndStrippedView(t *testing.T) {
	t.Run("split clamp", func(t *testing.T) {
		full := []Message{
			{Role: MessageRoleSummary, Content: segmentHeader(1) + "first facts", Segment: testSegment(1, 2)},
			{Role: MessageRoleSummary, Content: segmentHeader(2) + "second facts", Segment: testSegment(2, 3)},
			{Role: MessageRoleSummary, Content: withRequestBlock("old note", "old request")},
			{Role: MessageRoleUser, Content: "leftover request"},
			{Role: MessageRoleAssistant, Content: "leftover reply"},
		}
		providerStub := &fakeProvider{responses: []provider.ChatResponse{retryEmptyReply(), summaryResponse("clamped retry")}}
		req := segmentedCompactionRequest(providerStub, segmentedCompactionPolicy())
		state := stateFromMessages(full)
		candidate, ok := selectCompactionCandidate(state.Lineage, nil)
		if !ok {
			t.Fatal("selectCompactionCandidate() ok = false, want true")
		}
		outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 2, candidate, "")
		if err != nil {
			t.Fatalf("Compact() error = %v", err)
		}
		if len(providerStub.requests) != 2 || outcome.segmentDecision == nil || outcome.segmentDecision.Reason != segmentReasonEmptySource {
			t.Fatalf("calls=%d decision=%+v, want one retry and plain empty-source", len(providerStub.requests), outcome.segmentDecision)
		}
		prefix := outcome.State.Lineage.Generations[len(outcome.State.Lineage.Generations)-1].SummaryPrefix
		if len(prefix) != 1 || prefix[0].Segment != nil || !strings.Contains(prefix[0].Content, "clamped retry") {
			t.Fatalf("split-clamp prefix = %+v, want plain retry summary", prefix)
		}
		if !sameMessages(outcome.RetainedMessages, full[3:]) {
			t.Fatalf("retained = %#v, want clamped suffix %#v", outcome.RetainedMessages, full[3:])
		}
	})
	t.Run("stripped view", func(t *testing.T) {
		full := segmentStageConversation()
		providerStub := &fakeProvider{responses: []provider.ChatResponse{retryEmptyReply(), summaryResponse("stripped retry")}}
		archive := &fakeHistoryArchive{note: "[steiner:history] stripped archive"}
		req := segmentedCompactionRequest(providerStub, segmentedCompactionPolicy())
		req.HistoryArchive = archive
		state := stateFromMessages(full)
		generation := state.Lineage.Generations[0]
		state.Lineage = ConversationLineage{Generations: []ConversationGeneration{newConversationGeneration(generation.ID, cloneMessages(full[:3]), cloneMessages(full[3:]))}}
		state.Conversation = state.Lineage.FullMessages()
		skipped := map[string]bool{fmt.Sprintf("%d:%s", generation.ID, ConversationViewFull): true}
		applied, compacted, _, err := (&Runner{}).compactConversationForBudgetWithSteering(context.Background(), req, &state, 1, nil, skipped, new(int), "")
		if err != nil || !applied || !compacted {
			t.Fatalf("compaction applied=%t compacted=%t error=%v", applied, compacted, err)
		}
		if len(providerStub.requests) != 2 {
			t.Fatalf("ChatCompletion calls = %d, want exactly one retry", len(providerStub.requests))
		}
		retry := providerStub.requests[1]
		if messageContentsContain(retryNonSystemMessages(retry.Messages), "first facts") || messageContentsContain(retryNonSystemMessages(retry.Messages), "second facts") {
			t.Fatal("stripped-view retry request replayed the omitted old chain or span")
		}
		if len(archive.notes) != 1 || len(archive.appends) != 1 || !sameMessages(archive.notes[0], stripRequestBlocks(full[3:5])) || !sameMessages(archive.appends[0], stripRequestBlocks(full[3:5])) {
			t.Fatalf("stripped-view archive Note=%#v Append=%#v, want exact source without the old chain", archive.notes, archive.appends)
		}
		if len(state.Lineage.Generations) < 2 {
			t.Fatalf("generations=%d, want adopted stripped-view rebase", len(state.Lineage.Generations))
		}
		prefix := state.Lineage.Generations[len(state.Lineage.Generations)-1].SummaryPrefix
		if len(prefix) != 2 || prefix[0].Segment == nil || prefix[0].Segment.Sequence != 1 {
			t.Fatalf("stripped-view retry prefix = %+v, want sequence 1 and regenerated tail", prefix)
		}
		if body, request, ok := SplitRequestBlock(prefix[1].Content); !ok || body != archive.note || request != "span request" {
			t.Fatalf("stripped-view retry tail = %q (body=%q request=%q ok=%t)", prefix[1].Content, body, request, ok)
		}
	})
}
