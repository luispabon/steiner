package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// segmentCompactionMessages builds two immutable segments, a mutable tail with
// note and request block, a two-message append span and one retained turn.
func segmentCompactionMessages() []Message {
	return []Message{
		{Role: MessageRoleSummary, Content: segmentHeader(1) + "first facts", Segment: testSegment(1, 2)},
		{Role: MessageRoleSummary, Content: segmentHeader(2) + "second facts", Segment: testSegment(2, 3)},
		{Role: MessageRoleSummary, Content: withRequestBlock("[steiner:history] archived to sessions/old.jsonl", "old request")},
		{Role: MessageRoleUser, Content: "span request"},
		{Role: MessageRoleAssistant, Content: "span reply"},
		{Role: MessageRoleUser, Content: "current request"},
	}
}

// mustJSON marshals a message slice for a byte-identity comparison.
func mustJSON(t *testing.T, messages []Message) []byte {
	t.Helper()
	encoded, err := json.Marshal(messages)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	return encoded
}

func TestBuildSegmentCompactionStateAppend(t *testing.T) {
	full := segmentCompactionMessages()
	scan := scanSegmentChain(full)
	if scan.Status != chainValid || scan.End != 3 {
		t.Fatalf("scan status = %v end = %d, want a valid chain ending at 3", scan.Status, scan.End)
	}
	next, err := buildSegmentCompactionState(stateFromMessages(full), segmentStateInput{
		Scan:         scan,
		Source:       full[3:5],
		Summary:      withRequestBlock("new facts\n\n[steiner:history] stale note", "summarizer copy of the old request"),
		HistoryNote:  "[steiner:history] archived to sessions/new.jsonl",
		RequestBlock: "current request",
		Retained:     full[5:],
	})
	if err != nil {
		t.Fatalf("buildSegmentCompactionState() error = %v", err)
	}

	if got, want := len(next.Lineage.Generations), 2; got != want {
		t.Fatalf("generations = %d, want %d", got, want)
	}
	prefix := next.Lineage.Generations[1].SummaryPrefix
	if got, want := len(prefix), 4; got != want {
		t.Fatalf("summary prefix len = %d, want %d (two old segments, new segment, tail)", got, want)
	}
	if got, want := mustJSON(t, prefix[:2]), mustJSON(t, full[:2]); !bytes.Equal(got, want) {
		t.Fatalf("old segments changed:\n got %s\nwant %s", got, want)
	}

	segment := prefix[2]
	if segment.Segment == nil || *segment.Segment != (SummarySegment{Sequence: 3, SourceMessageCount: 2}) {
		t.Fatalf("new segment metadata = %+v, want sequence 3 count 2", segment.Segment)
	}
	if got, want := segment.Content, segmentHeader(3)+"new facts\n\n\n[steiner latest-request bytes=34]\nsummarizer copy of the old request"; got != want {
		t.Fatalf("new segment content = %q, want %q", got, want)
	}
	if strings.Contains(segment.Content, HistoryNoteMarker) {
		t.Fatalf("new segment kept a history note: %q", segment.Content)
	}
	if _, _, ok := SplitRequestBlock(segment.Content); ok {
		t.Fatalf("new segment kept a request block: %q", segment.Content)
	}

	tail := prefix[3]
	if tail.Segment != nil {
		t.Fatalf("tail carries segment metadata: %+v", tail.Segment)
	}
	body, request, ok := SplitRequestBlock(tail.Content)
	if !ok || body != "[steiner:history] archived to sessions/new.jsonl" || request != "current request" {
		t.Fatalf("tail body = %q request = %q ok = %t, want the note and current request", body, request, ok)
	}

	retained := next.Lineage.Generations[1].Messages
	if len(retained) != 1 || retained[0].Content != "current request" {
		t.Fatalf("retained = %+v, want the current request only", retained)
	}
	if !sameMessages(next.Conversation, next.Lineage.FullMessages()) {
		t.Fatal("conversation does not match the new lineage")
	}
}

func TestBuildSegmentCompactionStateRebase(t *testing.T) {
	full := segmentCompactionMessages()
	next, err := buildSegmentCompactionState(stateFromMessages(full), segmentStateInput{
		Scan:         scanSegmentChain(full),
		Rebase:       true,
		Source:       full[:5],
		Summary:      withRequestBlock("rebased facts", "summarizer copy"),
		HistoryNote:  "[steiner:history] archived to sessions/rebase.jsonl",
		RequestBlock: "old request",
		Retained:     full[5:],
	})
	if err != nil {
		t.Fatalf("buildSegmentCompactionState() error = %v", err)
	}

	prefix := next.Lineage.Generations[1].SummaryPrefix
	if got, want := len(prefix), 2; got != want {
		t.Fatalf("summary prefix len = %d, want %d (new segment and tail)", got, want)
	}
	segment := prefix[0]
	if segment.Segment == nil || *segment.Segment != (SummarySegment{Sequence: 1, SourceMessageCount: 2}) {
		t.Fatalf("segment metadata = %+v, want sequence 1 count 2", segment.Segment)
	}
	if got, want := segment.Content, segmentHeader(1)+"rebased facts\n\n[steiner latest-request bytes=15]\nsummarizer copy"; got != want {
		t.Fatalf("segment content = %q, want %q", got, want)
	}
	if len(scanSegmentChain(prefix).Segments) != 1 || scanSegmentChain(prefix).Status != chainValid {
		t.Fatalf("rebuilt prefix is not a one-segment chain: %+v", scanSegmentChain(prefix))
	}
	body, request, ok := SplitRequestBlock(prefix[1].Content)
	if !ok || body != "[steiner:history] archived to sessions/rebase.jsonl" || request != "old request" {
		t.Fatalf("tail body = %q request = %q ok = %t, want the note and old request", body, request, ok)
	}
}

func TestBuildSegmentCompactionStateRebaseLegacySummary(t *testing.T) {
	legacy := []Message{
		{Role: MessageRoleSummary, Content: "plain legacy summary"},
		{Role: MessageRoleUser, Content: "newer request"},
	}
	scan := scanSegmentChain(legacy)
	if scan.Status != chainNone {
		t.Fatalf("scan status = %v, want no chain", scan.Status)
	}
	next, err := buildSegmentCompactionState(stateFromMessages(legacy), segmentStateInput{
		Scan:     scan,
		Rebase:   true,
		Source:   legacy,
		Summary:  "folded legacy summary",
		Retained: legacy[1:],
	})
	if err != nil {
		t.Fatalf("buildSegmentCompactionState() error = %v", err)
	}
	prefix := next.Lineage.Generations[1].SummaryPrefix
	if got, want := len(prefix), 1; got != want {
		t.Fatalf("summary prefix len = %d, want %d (empty tail omitted)", got, want)
	}
	if prefix[0].Segment == nil || *prefix[0].Segment != (SummarySegment{Sequence: 1, SourceMessageCount: 1}) {
		t.Fatalf("segment metadata = %+v, want sequence 1 count 1", prefix[0].Segment)
	}
}

func TestBuildSegmentCompactionStateOmitsEmptyTail(t *testing.T) {
	full := segmentCompactionMessages()
	next, err := buildSegmentCompactionState(stateFromMessages(full), segmentStateInput{
		Scan:     scanSegmentChain(full),
		Source:   full[3:5],
		Summary:  "new facts",
		Retained: full[5:],
	})
	if err != nil {
		t.Fatalf("buildSegmentCompactionState() error = %v", err)
	}
	prefix := next.Lineage.Generations[1].SummaryPrefix
	if got, want := len(prefix), 3; got != want {
		t.Fatalf("summary prefix len = %d, want %d (two old segments and the new segment)", got, want)
	}
	for i, message := range prefix {
		if message.Segment == nil {
			t.Fatalf("prefix[%d] is an unexpected tail: %q", i, message.Content)
		}
	}
}

func TestBuildSegmentCompactionStateRefusesEmptySource(t *testing.T) {
	full := segmentCompactionMessages()
	tests := []struct {
		name   string
		source []Message
	}{
		{name: "no source", source: nil},
		{name: "summaries only", source: full[:3]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildSegmentCompactionState(stateFromMessages(full), segmentStateInput{
				Scan:     scanSegmentChain(full),
				Source:   tt.source,
				Summary:  "summary",
				Retained: full[5:],
			})
			if !errors.Is(err, errSegmentSourceEmpty) {
				t.Fatalf("error = %v, want %v", err, errSegmentSourceEmpty)
			}
		})
	}
}

func TestBuildSegmentCompactionStateRefusesInvalidAppendChain(t *testing.T) {
	full := segmentCompactionMessages()
	tests := []struct {
		name string
		scan segmentChainScan
	}{
		{name: "no chain", scan: scanSegmentChain(nil)},
		{name: "invalid chain", scan: scanSegmentChain([]Message{
			{Role: MessageRoleSummary, Content: "one", Segment: testSegment(2, 2)},
		})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildSegmentCompactionState(stateFromMessages(full), segmentStateInput{
				Scan:     tt.scan,
				Source:   full[3:5],
				Summary:  "summary",
				Retained: full[5:],
			})
			if !errors.Is(err, errSegmentAppendChainInvalid) {
				t.Fatalf("error = %v, want %v", err, errSegmentAppendChainInvalid)
			}
		})
	}
}

func TestBuildSegmentCompactionStatePreservesSegments(t *testing.T) {
	full := segmentCompactionMessages()
	scan := scanSegmentChain(full)
	before := mustJSON(t, scan.Segments)
	next, err := buildSegmentCompactionState(stateFromMessages(full), segmentStateInput{
		Scan:         scan,
		Source:       full[3:5],
		Summary:      "new facts",
		HistoryNote:  "[steiner:history] archived",
		RequestBlock: "current request",
		Retained:     full[5:],
	})
	if err != nil {
		t.Fatalf("buildSegmentCompactionState() error = %v", err)
	}
	prefix := next.Lineage.Generations[1].SummaryPrefix
	if after := mustJSON(t, prefix[:2]); !bytes.Equal(before, after) {
		t.Fatalf("segments changed:\n got %s\nwant %s", after, before)
	}
	prefix[0].Segment.Sequence = 9
	if scan.Segments[0].Segment.Sequence != 1 {
		t.Fatalf("new state shares segment metadata with the scan: %+v", scan.Segments[0].Segment)
	}
}

func TestBuildSegmentCompactionStateReplaysSkills(t *testing.T) {
	activation := mustSkillActivation(t, "alpha", "alpha body")
	full := []Message{
		{Role: MessageRoleSummary, Content: segmentHeader(1) + "facts", Segment: testSegment(1, 2)},
		{Role: MessageRoleUser, Content: "span request"},
		{Role: MessageRoleAssistant, Content: "span reply"},
		{Role: MessageRoleUser, Content: prompt.PrependSkillBlocks([]string{activation}, "current request")},
	}
	next, err := buildSegmentCompactionState(stateFromMessages(full), segmentStateInput{
		Scan:     scanSegmentChain(full),
		Source:   full[1:3],
		Summary:  "new facts",
		Retained: full[3:],
	})
	if err != nil {
		t.Fatalf("buildSegmentCompactionState() error = %v", err)
	}
	retained := next.Lineage.Generations[1].Messages
	if len(retained) != 2 {
		t.Fatalf("retained = %+v, want the skill replay and the stripped request", retained)
	}
	if retained[0].Source != MessageSourceSkillReplay || retained[0].Content != activation {
		t.Fatalf("replayed skill = %+v, want %q", retained[0], activation)
	}
	if retained[1].Content != "current request" {
		t.Fatalf("stripped retained content = %q, want %q", retained[1].Content, "current request")
	}
	if got := skillEnvelopeCount(next.Conversation); got != 1 {
		t.Fatalf("skill envelopes = %d, want exactly the replay", got)
	}
}

func TestSegmentRetentionStart(t *testing.T) {
	full := []Message{
		{Role: MessageRoleSummary, Content: "one"},
		{Role: MessageRoleSummary, Content: "two"},
		{Role: MessageRoleSummary, Content: "tail"},
		{Role: MessageRoleUser, Content: "a"},
		{Role: MessageRoleAssistant, Content: "b"},
		{Role: MessageRoleUser, Content: "c"},
	}
	tests := []struct {
		name     string
		retained []Message
		want     int
		wantOK   bool
	}{
		{name: "suffix", retained: full[4:], want: 4, wantOK: true},
		{name: "clone of a suffix", retained: cloneMessages(full[3:]), want: 3, wantOK: true},
		{name: "whole conversation", retained: cloneMessages(full), want: 0, wantOK: true},
		{name: "empty retention", retained: nil, want: len(full), wantOK: true},
		{name: "longer than the conversation", retained: append(cloneMessages(full), Message{Role: MessageRoleUser, Content: "d"}), wantOK: false},
		{name: "not a suffix", retained: []Message{{Role: MessageRoleUser, Content: "a"}, {Role: MessageRoleAssistant, Content: "c"}}, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := segmentRetentionStart(full, tt.retained)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("segmentRetentionStart() = %d, %t, want %d, %t", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestSegmentRetentionStartFromStrippedCandidate(t *testing.T) {
	raw := []Message{
		{Role: MessageRoleUser, Content: "one"},
		{Role: MessageRoleAssistant, Content: "one reply"},
		{Role: MessageRoleUser, Content: "two"},
		{Role: MessageRoleAssistant, Content: "two reply"},
		{Role: MessageRoleUser, Content: "three"},
		{Role: MessageRoleAssistant, Content: "three reply"},
		{Role: MessageRoleUser, Content: "four"},
	}
	prefix := segmentCompactionMessages()[:3]

	persisted := ConversationGeneration{ID: 2, SummaryPrefix: cloneMessages(prefix), Messages: cloneMessages(raw)}
	fullMessages := persisted.FullMessages()
	scan := scanSegmentChain(fullMessages)
	retained := retainRecentTurns(persisted.SummaryPrefixStrippedMessages(), 3)
	start, ok := segmentRetentionStart(fullMessages, retained)
	if !ok {
		t.Fatal("segmentRetentionStart() failed on the summary-stripped candidate")
	}
	if want := len(fullMessages) - len(retained); start != want {
		t.Fatalf("retention start = %d, want %d", start, want)
	}
	if !sameMessages(fullMessages[start:], retained) {
		t.Fatal("mapped suffix does not match the retained messages")
	}
	if count, ok := segmentAppendSourceCount(stateFromMessages(fullMessages), fullMessages, scan, start); !ok || count == 0 {
		t.Fatalf("append source count = %d, %t, want a counted non-empty span", count, ok)
	}

	// In-run shape: the chain sits in Messages, so the same absolute index must
	// fall out of the same mapping.
	flattened := newConversationLineage(fullMessages)
	flatFull := flattened.FullMessages()
	flatRetained := retainRecentTurns(flattened.SummaryPrefixStrippedMessages(), 3)
	flatStart, ok := segmentRetentionStart(flatFull, flatRetained)
	if !ok || flatStart != start {
		t.Fatalf("flattened retention start = %d, %t, want %d, true", flatStart, ok, start)
	}
	if !sameMessages(flatFull[flatStart:], retained) {
		t.Fatal("flattened suffix does not match the retained messages")
	}

	transformed := cloneMessages(retained)
	transformed[0].Content += " (elided)"
	if _, ok := segmentRetentionStart(fullMessages, transformed); ok {
		t.Fatal("segmentRetentionStart() accepted a transformed suffix")
	}
}

func TestSegmentAppendSourceCount(t *testing.T) {
	full := []Message{
		{Role: MessageRoleSummary, Content: segmentHeader(1), Segment: testSegment(1, 2)},
		{Role: MessageRoleSummary, Content: "tail"},
		{Role: MessageRoleUser, Content: "pre request"},
		{Role: MessageRoleSummary, Content: "mid summary"},
		{Role: MessageRoleUser, Content: "span request"},
		{Role: MessageRoleUser, Content: "current request"},
	}
	scan := scanSegmentChain(full)
	if scan.End != 2 {
		t.Fatalf("scan end = %d, want 2", scan.End)
	}
	state := stateFromMessages(full)
	tests := []struct {
		name           string
		retentionStart int
		want           int
		wantOK         bool
	}{
		{name: "mid summary excluded", retentionStart: 4, want: 1, wantOK: true},
		{name: "span with summaries excluded", retentionStart: 5, want: 2, wantOK: true},
		{name: "span ending at the chain", retentionStart: 2, want: 0, wantOK: true},
		{name: "retention inside the chain", retentionStart: 1, wantOK: false},
		{name: "retention past the slice", retentionStart: len(full) + 1, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := segmentAppendSourceCount(state, full, scan, tt.retentionStart)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("segmentAppendSourceCount() = %d, %t, want %d, %t", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestSegmentAppendSourceCountSkipsReplayOnlySpan(t *testing.T) {
	activation := mustSkillActivation(t, "alpha", "alpha body")
	full := []Message{
		{Role: MessageRoleSummary, Content: segmentHeader(1) + "facts", Segment: testSegment(1, 2)},
		{Role: MessageRoleUser, Source: MessageSourceSkillReplay, Content: activation},
		{Role: MessageRoleUser, Content: "current request"},
	}
	state := stateFromMessages(full)
	scan := scanSegmentChain(full)
	if scan.End != 1 {
		t.Fatalf("scan end = %d, want 1", scan.End)
	}

	count, ok := segmentAppendSourceCount(state, full, scan, 2)
	if !ok || count != 0 {
		t.Fatalf("replay-only span count = %d, %t, want 0, true", count, ok)
	}
	decision := decideSegmentAction(segmentDecisionInput{
		Policy:               SegmentedCompactionPolicy{MaxSegments: 4, MaxChainPercent: 12.5, MaxPostCompactionPercent: 50},
		Chain:                scan,
		Mode:                 prompt.CompactionModeNormal,
		ContextSize:          20000,
		CompactionThreshold:  8000,
		RetentionStart:       2,
		AppendSourceMessages: count,
	})
	if !decision.Rebase || decision.Reason != "empty-span" {
		t.Fatalf("decision = %+v, want a rebase with reason empty-span", decision)
	}

	count, ok = segmentAppendSourceCount(state, full, scan, len(full))
	if !ok || count != 1 {
		t.Fatalf("span with the request count = %d, %t, want 1, true", count, ok)
	}
}

func TestDropTailRequestBlockState(t *testing.T) {
	t.Parallel()

	segment := Message{Role: MessageRoleSummary, Content: withRequestBlock("Segment 1. facts", "segment request"), Segment: testSegment(1, 2)}
	tests := []struct {
		name        string
		tail        Message
		wantSummary string
		wantPrefix  []Message
	}{
		{
			name:        "history note tail keeps its body",
			tail:        Message{Role: MessageRoleSummary, Content: withRequestBlock("[steiner:history] archived", "tail request")},
			wantSummary: "[steiner:history] archived",
			wantPrefix: []Message{
				segment,
				{Role: MessageRoleSummary, Content: "[steiner:history] archived"},
			},
		},
		{
			name:        "block-only tail is removed",
			tail:        Message{Role: MessageRoleSummary, Content: withRequestBlock("", "tail request")},
			wantSummary: "",
			wantPrefix:  []Message{segment},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state := RunState{Lineage: ConversationLineage{
				Generations:      []ConversationGeneration{{ID: 2, SummaryPrefix: []Message{segment, tt.tail}, Messages: []Message{{Role: MessageRoleUser, Content: "current request"}}}},
				NextGenerationID: 3,
			}}
			state.Conversation = state.Lineage.FullMessages()
			segmentJSON := mustJSON(t, []Message{segment})
			sourceJSON := mustJSON(t, state.Lineage.Generations[0].SummaryPrefix)

			next, summaryText, dropped := dropTailRequestBlockState(state)
			if !dropped {
				t.Fatal("dropTailRequestBlockState() reported no drop")
			}
			if summaryText != tt.wantSummary {
				t.Errorf("summary text = %q, want %q", summaryText, tt.wantSummary)
			}
			prefix := next.Lineage.Generations[0].SummaryPrefix
			if got := mustJSON(t, prefix[:1]); !bytes.Equal(got, segmentJSON) {
				t.Fatalf("segment changed:\n got %s\nwant %s", got, segmentJSON)
			}
			if got, want := mustJSON(t, prefix), mustJSON(t, tt.wantPrefix); !bytes.Equal(got, want) {
				t.Fatalf("summary prefix = %s, want %s", got, want)
			}
			if !sameMessages(next.Conversation, next.Lineage.FullMessages()) {
				t.Fatal("conversation does not match the new lineage")
			}
			if got := mustJSON(t, state.Lineage.Generations[0].SummaryPrefix); !bytes.Equal(got, sourceJSON) {
				t.Fatalf("source state summary prefix mutated:\n got %s\nwant %s", got, sourceJSON)
			}
		})
	}
}

// segmentStageConversation builds the in-run shape of a segmented conversation:
// two immutable segments, a mutable tail, a two-message append span and three
// retained turns. Retention keeps the last three turns, so the append span is
// FullMessages()[3:5].
func segmentStageConversation() []Message {
	return []Message{
		{Role: MessageRoleSummary, Content: segmentHeader(1) + "first facts", Segment: testSegment(1, 2)},
		{Role: MessageRoleSummary, Content: segmentHeader(2) + "second facts", Segment: testSegment(2, 3)},
		{Role: MessageRoleSummary, Content: withRequestBlock("[steiner:history] archived to sessions/old.jsonl", "old request")},
		{Role: MessageRoleUser, Content: "span request"},
		{Role: MessageRoleAssistant, Content: "span reply"},
		{Role: MessageRoleUser, Content: "recent one"},
		{Role: MessageRoleAssistant, Content: "recent one reply"},
		{Role: MessageRoleUser, Content: "recent two"},
		{Role: MessageRoleAssistant, Content: "recent two reply"},
		{Role: MessageRoleUser, Content: "recent three"},
		{Role: MessageRoleAssistant, Content: "recent three reply"},
	}
}

// segmentedCompactionPolicy is the default experiment policy, enabled.
func segmentedCompactionPolicy() SegmentedCompactionPolicy {
	policy := DefaultSegmentedCompactionPolicy()
	policy.Enabled = true
	return policy
}

// segmentedCompactionRequest builds a compaction request for the fixture
// conversations: a large window so only the segment thresholds can rebase.
func segmentedCompactionRequest(p provider.Provider, policy SegmentedCompactionPolicy) RunRequest {
	return RunRequest{
		Provider:            p,
		ResolvedModel:       provider.ResolvedModel{BackendModelID: "test-model"},
		SegmentedCompaction: policy,
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:               100000,
			MaxCompletionTokens:       256,
			SafetyMarginTokens:        0,
			SummaryMaxTokens:          128,
			NormalSummaryMaxTokens:    128,
			EmergencySummaryMaxTokens: 64,
		},
	}
}

// oneSummaryProvider returns a fake provider that answers each summary call
// with the next scripted text.
func oneSummaryProvider(texts ...string) *fakeProvider {
	responses := make([]provider.ChatResponse, 0, len(texts))
	for _, text := range texts {
		responses = append(responses, provider.ChatResponse{
			Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: text},
			FinishReason: "stop",
		})
	}
	return &fakeProvider{responses: responses}
}

// providerCacheBoundaries returns the indices of the request messages that
// carry the segment cache boundary.
func providerCacheBoundaries(messages []provider.Message) []int {
	var indices []int
	for i, message := range messages {
		if message.CacheBoundary {
			indices = append(indices, i)
		}
	}
	return indices
}

func segmentDecisionLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read compaction log: %v", err)
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "segment_decision ") {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestSegmentedCompactionAppendsSegment(t *testing.T) {
	full := segmentStageConversation()
	providerStub := oneSummaryProvider("fresh append facts")
	state := stateFromMessages(full)
	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}

	outcome, err := summarizeCompactor{}.Compact(context.Background(), segmentedCompactionRequest(providerStub, segmentedCompactionPolicy()), state, 4, candidate, "")
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if !outcome.Applied {
		t.Fatal("Applied = false, want true")
	}
	if got, want := len(providerStub.requests), 1; got != want {
		t.Fatalf("ChatCompletion calls = %d, want %d", got, want)
	}
	request := providerStub.requests[0]
	instruction := request.Messages[len(request.Messages)-1]
	if want := prompt.RenderSegmentAppendInstruction(""); instruction.Content != want {
		t.Fatalf("instruction = %q, want the append instruction %q", instruction.Content, want)
	}
	if !messageContentsContain(request.Messages, "span request") {
		t.Fatal("summarizer request is missing the append span")
	}
	for _, retained := range []string{"recent one", "recent two", "recent three"} {
		if messageContentsContain(request.Messages, retained) {
			t.Fatalf("summarizer request kept retained content %q", retained)
		}
	}
	boundaries := providerCacheBoundaries(request.Messages)
	if len(boundaries) != 1 {
		t.Fatalf("cache boundaries = %v, want exactly one on the last old segment", boundaries)
	}
	if got := request.Messages[boundaries[0]].Content; !strings.Contains(got, "second facts") {
		t.Fatalf("cache boundary message = %q, want the second segment", got)
	}

	latest := outcome.State.Lineage.Generations[len(outcome.State.Lineage.Generations)-1]
	prefix := latest.SummaryPrefix
	if got, want := len(prefix), 4; got != want {
		t.Fatalf("summary prefix len = %d, want %d (two old segments, new segment, tail)", got, want)
	}
	if got, want := mustJSON(t, prefix[:2]), mustJSON(t, full[:2]); !bytes.Equal(got, want) {
		t.Fatalf("old segments changed:\n got %s\nwant %s", got, want)
	}
	segment := prefix[2]
	if segment.Segment == nil || *segment.Segment != (SummarySegment{Sequence: 3, SourceMessageCount: 2}) {
		t.Fatalf("new segment metadata = %+v, want sequence 3 count 2", segment.Segment)
	}
	if got, want := segment.Content, segmentHeader(3)+"fresh append facts"; got != want {
		t.Fatalf("new segment content = %q, want %q", got, want)
	}
	if _, _, ok := SplitRequestBlock(segment.Content); ok {
		t.Fatalf("new segment kept a request block: %q", segment.Content)
	}
	tail := prefix[3]
	if tail.Segment != nil {
		t.Fatalf("tail carries segment metadata: %+v", tail.Segment)
	}
	if body, block, ok := SplitRequestBlock(tail.Content); !ok || body != "" || block != "span request" {
		t.Fatalf("tail = %q (body %q block %q ok %t), want the regenerated span request", tail.Content, body, block, ok)
	}
	if !sameMessages(latest.Messages, full[5:]) {
		t.Fatalf("retained = %#v, want the last three turns", latest.Messages)
	}

	if outcome.RequestBlock != output.RequestBlockNew {
		t.Fatalf("RequestBlock = %q, want %q", outcome.RequestBlock, output.RequestBlockNew)
	}
	if got, want := outcome.RequestBlockBytes, len("span request"); got != want {
		t.Fatalf("RequestBlockBytes = %d, want %d", got, want)
	}
	if outcome.segmentDecision == nil {
		t.Fatal("outcome carries no segment decision")
	}
	if got := *outcome.segmentDecision; got.Rebase || got.Reason != "append" || got.Sequence != 3 {
		t.Fatalf("decision = %+v, want append reason append sequence 3", got)
	}
}

func TestSegmentedCompactionPlainFallbackThenChainStart(t *testing.T) {
	full := []Message{
		{Role: MessageRoleSummary, Content: segmentHeader(1) + "first facts", Segment: testSegment(1, 2)},
		{Role: MessageRoleSummary, Content: segmentHeader(2) + "second facts", Segment: testSegment(2, 3)},
		{Role: MessageRoleSummary, Content: withRequestBlock("[steiner:history] archived to sessions/old.jsonl", "old request")},
		{Role: MessageRoleUser, Content: "recent one"},
		{Role: MessageRoleAssistant, Content: "recent one reply"},
		{Role: MessageRoleUser, Content: "recent two"},
		{Role: MessageRoleAssistant, Content: "recent two reply"},
		{Role: MessageRoleUser, Content: "recent three"},
		{Role: MessageRoleAssistant, Content: "recent three reply"},
	}
	providerStub := oneSummaryProvider("plain fallback facts", "chain start facts")
	req := segmentedCompactionRequest(providerStub, segmentedCompactionPolicy())
	req.CompactionLogPath = filepath.Join(t.TempDir(), "compaction.log")

	state := stateFromMessages(full)
	runner := &Runner{}
	applied, ok, _, err := runner.compactConversationForBudgetWithSteering(context.Background(), req, &state, 1, nil, map[string]bool{}, new(int), "")
	if err != nil || !applied || !ok {
		t.Fatalf("first compaction applied=%t ok=%t err=%v, want an applied compaction", applied, ok, err)
	}
	if got, want := len(providerStub.requests), 1; got != want {
		t.Fatalf("first compaction calls = %d, want %d", got, want)
	}
	instruction := providerStub.requests[0].Messages[len(providerStub.requests[0].Messages)-1]
	if want := prompt.RenderConversationCompactionInstruction("", prompt.CompactionModeNormal); instruction.Content != want {
		t.Fatalf("first instruction = %q, want the ordinary instruction", instruction.Content)
	}
	first := state.Lineage.Generations[len(state.Lineage.Generations)-1]
	if got, want := len(first.SummaryPrefix), 1; got != want {
		t.Fatalf("first summary prefix len = %d, want %d plain summary", got, want)
	}
	if first.SummaryPrefix[0].Segment != nil {
		t.Fatalf("plain fallback wrote segment metadata: %+v", first.SummaryPrefix[0].Segment)
	}
	if body, block, ok := SplitRequestBlock(first.SummaryPrefix[0].Content); !ok || body != "plain fallback facts" || block != "old request" {
		t.Fatalf("plain summary = %q (body %q block %q ok %t), want the carried request block", first.SummaryPrefix[0].Content, body, block, ok)
	}

	state.Lineage = state.Lineage.WithAppendedMessages([]Message{
		{Role: MessageRoleUser, Content: "fourth request"},
		{Role: MessageRoleAssistant, Content: "fourth reply"},
	})
	state.Conversation = state.Lineage.FullMessages()
	applied, ok, _, err = runner.compactConversationForBudgetWithSteering(context.Background(), req, &state, 2, nil, map[string]bool{}, new(int), "")
	if err != nil || !applied || !ok {
		t.Fatalf("second compaction applied=%t ok=%t err=%v, want an applied compaction", applied, ok, err)
	}
	if got, want := len(providerStub.requests), 2; got != want {
		t.Fatalf("compaction calls = %d, want %d", got, want)
	}
	second := state.Lineage.Generations[len(state.Lineage.Generations)-1]
	if second.SummaryPrefix[0].Segment == nil || *second.SummaryPrefix[0].Segment != (SummarySegment{Sequence: 1, SourceMessageCount: 2}) {
		t.Fatalf("chain start segment = %+v, want sequence 1 count 2", second.SummaryPrefix[0].Segment)
	}

	lines := segmentDecisionLines(t, req.CompactionLogPath)
	if got, want := len(lines), 2; got != want {
		t.Fatalf("segment_decision lines = %v, want %d", lines, want)
	}
	if want := "segment_decision action=plain reason=empty-source sequence=0"; !strings.HasPrefix(lines[0], want) {
		t.Fatalf("first decision line = %q, want prefix %q", lines[0], want)
	}
	if want := "segment_decision action=rebase reason=chain-start sequence=1"; !strings.HasPrefix(lines[1], want) {
		t.Fatalf("second decision line = %q, want prefix %q", lines[1], want)
	}
}

func TestSegmentedCompactionFlagOffStoresPlainSummary(t *testing.T) {
	full := segmentStageConversation()
	providerStub := oneSummaryProvider("flag off facts")
	req := segmentedCompactionRequest(providerStub, SegmentedCompactionPolicy{})
	req.CompactionLogPath = filepath.Join(t.TempDir(), "compaction.log")
	state := stateFromMessages(full)

	applied, ok, _, err := (&Runner{}).compactConversationForBudgetWithSteering(context.Background(), req, &state, 1, nil, map[string]bool{}, new(int), "")
	if err != nil || !applied || !ok {
		t.Fatalf("compaction applied=%t ok=%t err=%v, want an applied compaction", applied, ok, err)
	}
	request := providerStub.requests[0]
	instruction := request.Messages[len(request.Messages)-1]
	if want := prompt.RenderConversationCompactionInstruction("", prompt.CompactionModeNormal); instruction.Content != want {
		t.Fatalf("instruction = %q, want the ordinary instruction", instruction.Content)
	}
	if boundaries := providerCacheBoundaries(request.Messages); len(boundaries) != 0 {
		t.Fatalf("flag-off request cache boundaries = %v, want none", boundaries)
	}
	latest := state.Lineage.Generations[len(state.Lineage.Generations)-1]
	if got, want := len(latest.SummaryPrefix), 1; got != want {
		t.Fatalf("summary prefix len = %d, want %d plain summary", got, want)
	}
	if latest.SummaryPrefix[0].Segment != nil {
		t.Fatalf("flag-off compaction wrote segment metadata: %+v", latest.SummaryPrefix[0].Segment)
	}
	if got, want := latest.SummaryPrefix[0].Content, "flag off facts"+withRequestBlock("", "span request"); got != want {
		t.Fatalf("plain summary = %q, want %q", got, want)
	}
	if lines := segmentDecisionLines(t, req.CompactionLogPath); len(lines) != 0 {
		t.Fatalf("segment_decision lines = %v, want none with the flag off", lines)
	}
}

func TestSegmentedCompactionEmergencyRebases(t *testing.T) {
	longTurn := func(text string) string { return strings.Repeat(text, 4) }
	messages := []Message{
		{Role: MessageRoleUser, Content: longTurn("turn 1 user detail ")},
		{Role: MessageRoleAssistant, Content: longTurn("turn 1 assistant detail ")},
		{Role: MessageRoleUser, Content: longTurn("turn 2 user detail ")},
		{Role: MessageRoleAssistant, Content: longTurn("turn 2 assistant detail ")},
		{Role: MessageRoleUser, Content: longTurn("turn 3 user detail ")},
		{Role: MessageRoleAssistant, Content: longTurn("turn 3 assistant detail ")},
		{Role: MessageRoleUser, Content: longTurn("turn 4 user detail ")},
		{Role: MessageRoleAssistant, Content: longTurn("turn 4 assistant detail ")},
	}
	providerStub := oneSummaryProvider(strings.Repeat("normal summary detail ", 1200), "emergency handoff summary")
	state := stateFromMessages(messages)
	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	req := segmentedCompactionRequest(providerStub, segmentedCompactionPolicy())
	req.ModelBudget.ContextSize = 7500
	req.ModelBudget.NormalSummaryMaxTokens = 64
	req.ModelBudget.SummaryMaxTokens = 64
	req.ModelBudget.EmergencySummaryMaxTokens = 16

	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 5, candidate, "")
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if !outcome.Applied {
		t.Fatal("Applied = false, want true")
	}
	if got, want := len(providerStub.requests), 2; got != want {
		t.Fatalf("ChatCompletion calls = %d, want %d", got, want)
	}
	emergency := providerStub.requests[1].Messages
	instruction := emergency[len(emergency)-1].Content
	if want := prompt.RenderConversationCompactionInstruction("", prompt.CompactionModeEmergency); instruction != want {
		t.Fatalf("emergency instruction = %q, want the ordinary emergency instruction", instruction)
	}
	if outcome.segmentDecision == nil {
		t.Fatal("outcome carries no segment decision")
	}
	if got := *outcome.segmentDecision; !got.Rebase || got.Reason != "emergency" || got.Sequence != 1 {
		t.Fatalf("decision = %+v, want a rebase reason emergency sequence 1", got)
	}
	latest := outcome.State.Lineage.Generations[len(outcome.State.Lineage.Generations)-1]
	if got, want := len(latest.SummaryPrefix), 2; got != want {
		t.Fatalf("summary prefix len = %d, want %d (emergency segment and tail)", got, want)
	}
	if latest.SummaryPrefix[0].Segment == nil || latest.SummaryPrefix[0].Segment.Sequence != 1 {
		t.Fatalf("emergency segment = %+v, want sequence 1", latest.SummaryPrefix[0].Segment)
	}
}

func TestNewSegmentStageUnmappedRetentionRebases(t *testing.T) {
	full := segmentStageConversation()
	req := segmentedCompactionRequest(oneSummaryProvider(), segmentedCompactionPolicy())
	retained := []Message{{Role: MessageRoleUser, Content: "not the retained suffix"}}
	tests := []struct {
		name       string
		source     []Message
		wantPlain  bool
		wantReason string
		wantSeq    int
	}{
		{name: "countable source rebases defensively", source: full[:5], wantReason: segmentReasonRetentionUnmapped, wantSeq: 1},
		{name: "summary-only source falls back to plain", source: full[:3], wantPlain: true, wantReason: segmentReasonEmptySource, wantSeq: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stage, err := newSegmentStage(context.Background(), req, stateFromMessages(full), segmentStageInput{
				Policy:    req.SegmentedCompaction,
				Full:      full,
				Source:    tt.source,
				Retained:  retained,
				Mode:      prompt.CompactionModeNormal,
				Model:     "test-model",
				Budget:    prompt.RequestTokenBudget{ContextSize: 100000, CompactionThresholdTokens: 70000},
				MaxTokens: 128,
			})
			if err != nil {
				t.Fatalf("newSegmentStage() error = %v", err)
			}
			if stage.Plain != tt.wantPlain {
				t.Fatalf("plain = %t, want %t", stage.Plain, tt.wantPlain)
			}
			if got := stage.Decision.Reason; got != tt.wantReason {
				t.Fatalf("reason = %q, want %q", got, tt.wantReason)
			}
			if got, want := stage.Decision.Sequence, tt.wantSeq; got != want {
				t.Fatalf("sequence = %d, want %d", got, want)
			}
			if !stage.Decision.Rebase {
				t.Fatalf("decision = %+v, want a rebase", stage.Decision)
			}
			if !sameMessages(stage.Source, tt.source) {
				t.Fatalf("source = %#v, want the selected source", stage.Source)
			}
		})
	}
}

func TestSegmentedCompactionNoteStaysInTail(t *testing.T) {
	full := segmentStageConversation()
	archive := &fakeHistoryArchive{note: "[steiner:history] archived to sessions/new.jsonl"}
	providerStub := oneSummaryProvider("fresh append facts")
	req := segmentedCompactionRequest(providerStub, segmentedCompactionPolicy())
	req.HistoryArchive = archive
	state := stateFromMessages(full)
	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}

	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 4, candidate, "")
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if got, want := len(archive.notes), 1; got != want {
		t.Fatalf("history notes = %d, want %d for one stage", got, want)
	}
	// The note source is the replaced tail, which carries the older pointer,
	// followed by the append span: an append must not lose the pointers the tail
	// it replaces held.
	wantSource := append(stripRequestBlocks(full[2:3]), full[3:5]...)
	if !sameMessages(archive.notes[0], wantSource) {
		t.Fatalf("history note source = %#v, want the previous tail and the append span", archive.notes[0])
	}
	latest := outcome.State.Lineage.Generations[len(outcome.State.Lineage.Generations)-1]
	prefix := latest.SummaryPrefix
	if got, want := len(prefix), 4; got != want {
		t.Fatalf("summary prefix len = %d, want %d", got, want)
	}
	if strings.Contains(prefix[2].Content, HistoryNoteMarker) {
		t.Fatalf("segment kept the history note: %q", prefix[2].Content)
	}
	if body, block, ok := SplitRequestBlock(prefix[3].Content); !ok || body != archive.note || block != "span request" {
		t.Fatalf("tail = %q (body %q block %q ok %t), want the note and span request", prefix[3].Content, body, block, ok)
	}
	if got, want := len(outcome.pendingArchive), 1; got != want {
		t.Fatalf("pending archive = %d, want %d append source", got, want)
	}
	if !sameMessages(outcome.pendingArchive[0], wantSource) {
		t.Fatalf("pending archive source = %#v, want the previous tail and the append span", outcome.pendingArchive[0])
	}
}

// sequencedHistoryArchive returns one note per Note call, so a test can tell the
// pointers each compaction wrote and the order it wrote them in.
type sequencedHistoryArchive struct {
	notes   []string
	sources [][]Message
}

func (a *sequencedHistoryArchive) Note(source []Message) (string, error) {
	a.sources = append(a.sources, source)
	if len(a.notes) == 0 {
		return "", nil
	}
	note := a.notes[0]
	a.notes = a.notes[1:]
	return note, nil
}

func (a *sequencedHistoryArchive) Append(context.Context, []Message) error { return nil }

// TestSegmentedCompactionAppendArchivesPreviousTailNote covers two appends in a
// row: the tail the second append replaces carries the first append's history
// pointer, so the second archive entry must carry that pointer instead of
// dropping it with the replaced tail.
func TestSegmentedCompactionAppendArchivesPreviousTailNote(t *testing.T) {
	const firstNote = "[steiner:history] archived to sessions/one.jsonl"
	const secondNote = "[steiner:history] archived to sessions/two.jsonl"
	full := segmentStageConversation()
	archive := &sequencedHistoryArchive{notes: []string{firstNote, secondNote}}
	req := segmentedCompactionRequest(oneSummaryProvider("first append facts", "second append facts"), segmentedCompactionPolicy())
	req.HistoryArchive = archive
	ctx := context.Background()
	state := stateFromMessages(full)

	var pending [][]Message
	compact := func(turn int, wantSequence int) {
		t.Helper()
		candidate, ok := selectCompactionCandidate(state.Lineage, nil)
		if !ok {
			t.Fatalf("selectCompactionCandidate() ok = false, want true")
		}
		outcome, err := summarizeCompactor{}.Compact(ctx, req, state, turn, candidate, "")
		if err != nil {
			t.Fatalf("Compact() error = %v", err)
		}
		if !outcome.Applied {
			t.Fatal("Applied = false, want true")
		}
		decision := outcome.segmentDecision
		if decision == nil || decision.Rebase || decision.Reason != "append" || decision.Sequence != wantSequence {
			t.Fatalf("decision = %+v, want append sequence %d", decision, wantSequence)
		}
		if got, want := len(outcome.pendingArchive), 1; got != want {
			t.Fatalf("pending archive = %d, want %d append source", got, want)
		}
		pending = outcome.pendingArchive
		state = outcome.State
	}
	// appendTurns adds a user and assistant message the way a run does.
	appendTurns := func(turns ...string) {
		t.Helper()
		var messages []Message
		for i, content := range turns {
			role := MessageRoleUser
			if i%2 == 1 {
				role = MessageRoleAssistant
			}
			messages = append(messages, Message{Role: role, Content: content})
		}
		state.Lineage = state.Lineage.WithAppendedMessages(messages)
		state.Conversation = state.Lineage.FullMessages()
	}

	compact(4, 3)
	firstWant := append(stripRequestBlocks(full[2:3]), full[3:5]...)
	if !sameMessages(pending[0], firstWant) {
		t.Fatalf("first archive source = %#v, want the previous tail and the first span", pending[0])
	}

	// New turns arrive, so the next compaction has a span to append.
	appendTurns("newer request", "newer reply", "newest request", "newest reply")
	compact(5, 4)

	second := pending[0]
	if len(second) == 0 {
		t.Fatal("second archive source is empty")
	}
	if !strings.Contains(second[0].Content, firstNote) {
		t.Fatalf("second archive source starts with %q, want the first tail's history pointer %q", second[0].Content, firstNote)
	}
	if strings.Contains(second[0].Content, requestBlockMarker) {
		t.Fatalf("second archive source kept a request block: %q", second[0].Content)
	}
	latest := state.Lineage.Generations[len(state.Lineage.Generations)-1]
	tail := latest.SummaryPrefix[len(latest.SummaryPrefix)-1]
	if tail.Segment != nil {
		t.Fatalf("newest summary message is a segment: %+v", tail.Segment)
	}
	if !strings.Contains(tail.Content, secondNote) {
		t.Fatalf("new tail = %q, want the second history note", tail.Content)
	}
}

// TestSegmentEmptySpanImpliesEmptySource records why an empty span always
// downgrades to the plain fallback: the scan ends after the leading summary
// messages, so the stage source is that summary run plus the span and the two
// counts of non-summary messages are equal.
func TestSegmentEmptySpanImpliesEmptySource(t *testing.T) {
	activation := mustSkillActivation(t, "alpha", "alpha body")
	fixtures := map[string][]Message{
		"no span": {
			{Role: MessageRoleSummary, Content: segmentHeader(1), Segment: testSegment(1, 2)},
			{Role: MessageRoleSummary, Content: "tail"},
			{Role: MessageRoleUser, Content: "retained request"},
		},
		"skill replay span": {
			{Role: MessageRoleSummary, Content: segmentHeader(1), Segment: testSegment(1, 2)},
			{Role: MessageRoleUser, Source: MessageSourceSkillReplay, Content: activation},
			{Role: MessageRoleUser, Content: "current request"},
		},
		"mid summary span": {
			{Role: MessageRoleSummary, Content: segmentHeader(1), Segment: testSegment(1, 2)},
			{Role: MessageRoleUser, Content: "span request"},
			{Role: MessageRoleSummary, Content: "mid summary"},
			{Role: MessageRoleUser, Content: "current request"},
		},
	}
	for name, full := range fixtures {
		t.Run(name, func(t *testing.T) {
			state := stateFromMessages(full)
			scan := scanSegmentChain(full)
			for start := scan.End; start <= len(full); start++ {
				span, ok := segmentAppendSourceCount(state, full, scan, start)
				if !ok {
					t.Fatalf("segmentAppendSourceCount(%d) not ok", start)
				}
				source := countSummarizedMessages(full[:start], activeSkillBlocks(state))
				if span != source {
					t.Fatalf("span count %d at %d, source count %d: an empty span must imply an empty source", span, start, source)
				}
			}
		})
	}
}

// TestSegmentedCompactionSplitInChainClampsPlain covers retention starting at
// the chain: the rebase source must be the whole leading chain and the retained
// tail must start after it, so no chain message follows the plain summary.
func TestSegmentedCompactionSplitInChainClampsPlain(t *testing.T) {
	full := []Message{
		{Role: MessageRoleSummary, Content: segmentHeader(1) + "first facts", Segment: testSegment(1, 2)},
		{Role: MessageRoleSummary, Content: segmentHeader(2) + "second facts", Segment: testSegment(2, 3)},
		{Role: MessageRoleSummary, Content: withRequestBlock("[steiner:history] archived to sessions/old.jsonl", "old request")},
		{Role: MessageRoleUser, Content: "leftover request"},
		{Role: MessageRoleAssistant, Content: "leftover reply"},
	}
	scan := scanSegmentChain(full)
	if scan.End != 3 {
		t.Fatalf("scan end = %d, want 3", scan.End)
	}
	providerStub := oneSummaryProvider("clamped plain facts")
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
	if !outcome.Applied {
		t.Fatal("Applied = false, want true")
	}
	if outcome.segmentDecision == nil || outcome.segmentDecision.Reason != segmentReasonEmptySource {
		t.Fatalf("decision = %+v, want the plain empty-source fallback", outcome.segmentDecision)
	}
	if outcome.RequestBlock != output.RequestBlockCarried {
		t.Fatalf("RequestBlock = %q, want the carried block", outcome.RequestBlock)
	}
	if got, want := outcome.RequestBlockBytes, len("old request"); got != want {
		t.Fatalf("RequestBlockBytes = %d, want %d", got, want)
	}
	wantFit, err := fitConversationState(context.Background(), req, outcome.State)
	if err != nil {
		t.Fatalf("fitConversationState() error = %v", err)
	}
	if got, want := outcome.Fit.EstimatedPromptTokens, wantFit.EstimatedPromptTokens; got != want {
		t.Fatalf("fit tokens = %d, want the refit %d", got, want)
	}
	if !sameMessages(outcome.RetainedMessages, full[scan.End:]) {
		t.Fatalf("retained = %#v, want the clamped retained tail", outcome.RetainedMessages)
	}
	latest := outcome.State.Lineage.Generations[len(outcome.State.Lineage.Generations)-1]
	if got, want := len(latest.SummaryPrefix), 1; got != want {
		t.Fatalf("summary prefix len = %d, want %d plain summary", got, want)
	}
	if latest.SummaryPrefix[0].Segment != nil {
		t.Fatalf("plain summary carries segment metadata: %+v", latest.SummaryPrefix[0].Segment)
	}
	for i, message := range latest.Messages {
		if message.Role == MessageRoleSummary || message.Segment != nil {
			t.Fatalf("retained message %d is a chain message: %#v", i, message)
		}
	}
	wantRetained := full[scan.End:]
	if !sameMessages(latest.Messages, wantRetained) {
		t.Fatalf("retained = %#v, want everything after the chain %#v", latest.Messages, wantRetained)
	}
	request := providerStub.requests[0]
	for _, needle := range []string{"first facts", "second facts"} {
		if !messageContentsContain(request.Messages, needle) {
			t.Fatalf("summarizer request missing the whole chain, want %q", needle)
		}
	}
	if messageContentsContain(request.Messages, "leftover request") {
		t.Fatal("clamped source summarized the retained tail")
	}
}

// TestSegmentedCompactionStrippedViewRebases covers the in-run fallback where the
// full view was already skipped and the stripped view is compacted: an existing
// chain must force an ordinary rebase over the stripped source.
func TestSegmentedCompactionStrippedViewRebases(t *testing.T) {
	full := segmentStageConversation()
	providerStub := oneSummaryProvider("stripped view facts")
	req := segmentedCompactionRequest(providerStub, segmentedCompactionPolicy())
	req.CompactionLogPath = filepath.Join(t.TempDir(), "compaction.log")
	state := stateFromMessages(full)
	latest := state.Lineage.Generations[0]
	state.Lineage = ConversationLineage{
		Generations: []ConversationGeneration{newConversationGeneration(latest.ID, cloneMessages(full[:3]), cloneMessages(full[3:]))},
	}
	state.Conversation = state.Lineage.FullMessages()
	skipped := map[string]bool{fmt.Sprintf("%d:%s", latest.ID, ConversationViewFull): true}

	applied, ok, _, err := (&Runner{}).compactConversationForBudgetWithSteering(context.Background(), req, &state, 1, nil, skipped, new(int), "")
	if err != nil || !applied || !ok {
		t.Fatalf("compaction applied=%t ok=%t err=%v, want an applied compaction", applied, ok, err)
	}
	if got, want := len(providerStub.requests), 1; got != want {
		t.Fatalf("compaction calls = %d, want %d", got, want)
	}
	instruction := providerStub.requests[0].Messages[len(providerStub.requests[0].Messages)-1]
	if want := prompt.RenderConversationCompactionInstruction("", prompt.CompactionModeNormal); instruction.Content != want {
		t.Fatalf("instruction = %q, want the ordinary instruction", instruction.Content)
	}
	if messageContentsContain(providerStub.requests[0].Messages, "first facts") {
		t.Fatal("stripped view summarizer request replayed the chain")
	}
	lines := segmentDecisionLines(t, req.CompactionLogPath)
	want := "segment_decision action=rebase reason=" + segmentReasonStrippedView + " sequence=1"
	if len(lines) != 1 || !strings.HasPrefix(lines[0], want) {
		t.Fatalf("decision lines = %v, want one line with prefix %q", lines, want)
	}
	newest := state.Lineage.Generations[len(state.Lineage.Generations)-1]
	if newest.SummaryPrefix[0].Segment == nil || newest.SummaryPrefix[0].Segment.Sequence != 1 {
		t.Fatalf("stripped view segment = %+v, want sequence 1", newest.SummaryPrefix[0].Segment)
	}
}

// plainFallbackState is the empty-span fixture: retention starts at the end of
// the chain and the source holds only summaries.
func plainFallbackState() RunState {
	return stateFromMessages([]Message{
		{Role: MessageRoleSummary, Content: segmentHeader(1) + "first facts", Segment: testSegment(1, 2)},
		{Role: MessageRoleSummary, Content: segmentHeader(2) + "second facts", Segment: testSegment(2, 3)},
		{Role: MessageRoleSummary, Content: withRequestBlock("[steiner:history] archived to sessions/old.jsonl", "old request")},
		{Role: MessageRoleUser, Content: "recent one"},
		{Role: MessageRoleAssistant, Content: "recent one reply"},
		{Role: MessageRoleUser, Content: "recent two"},
		{Role: MessageRoleAssistant, Content: "recent two reply"},
		{Role: MessageRoleUser, Content: "recent three"},
		{Role: MessageRoleAssistant, Content: "recent three reply"},
	})
}

// TestSegmentedCompactionHistoryNoteTiming checks that the archive note is
// rendered once, after the summarizer succeeded, and that the plain fallback
// stores the same bytes as the flag-off path for a present, empty and failed
// note.
func TestSegmentedCompactionHistoryNoteTiming(t *testing.T) {
	tests := []struct {
		name    string
		note    string
		noteErr error
	}{
		{name: "note present", note: "[steiner:history] archived to sessions/new.jsonl"},
		{name: "note empty"},
		{name: "note error", noteErr: errors.New("archive unavailable")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored := map[bool]string{}
			for _, enabled := range []bool{false, true} {
				archive := &fakeHistoryArchive{note: tt.note, noteErr: tt.noteErr}
				policy := SegmentedCompactionPolicy{}
				if enabled {
					policy = segmentedCompactionPolicy()
				}
				req := segmentedCompactionRequest(oneSummaryProvider("plain fallback facts"), policy)
				req.HistoryArchive = archive
				state := plainFallbackState()
				candidate, ok := selectCompactionCandidate(state.Lineage, nil)
				if !ok {
					t.Fatal("selectCompactionCandidate() ok = false, want true")
				}
				outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 1, candidate, "")
				if err != nil {
					t.Fatalf("enabled=%t Compact() error = %v", enabled, err)
				}
				if got, want := len(archive.notes), 1; got != want {
					t.Fatalf("enabled=%t history notes = %d, want %d", enabled, got, want)
				}
				wantPending := 0
				if tt.noteErr == nil && tt.note != "" {
					wantPending = 1
				}
				if got := len(outcome.pendingArchive); got != wantPending {
					t.Fatalf("enabled=%t pending archive = %d, want %d", enabled, got, wantPending)
				}
				if len(outcome.pendingArchive) == 1 && !sameMessages(outcome.pendingArchive[0], stripRequestBlocks(outcome.pendingRecall)) {
					t.Fatalf("enabled=%t archive source = %#v, want the summarized source", enabled, outcome.pendingArchive[0])
				}
				newest := outcome.State.Lineage.Generations[len(outcome.State.Lineage.Generations)-1]
				stored[enabled] = newest.SummaryPrefix[0].Content
			}
			if stored[false] != stored[true] {
				t.Fatalf("flag-off summary = %q, flag-on = %q, want identical bytes", stored[false], stored[true])
			}
		})
	}
}

func TestSegmentedCompactionFailedSummaryWritesNoNote(t *testing.T) {
	archive := &fakeHistoryArchive{note: "[steiner:history] archived to sessions/new.jsonl"}
	providerStub := &fakeProvider{chatFn: func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
		return provider.ChatResponse{}, errors.New("summarizer failed")
	}}
	req := segmentedCompactionRequest(providerStub, segmentedCompactionPolicy())
	req.HistoryArchive = archive
	state := plainFallbackState()
	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	if _, err := (summarizeCompactor{}).Compact(context.Background(), req, state, 1, candidate, ""); err == nil {
		t.Fatal("Compact() error = nil, want the summarizer failure")
	}
	if got := len(archive.notes); got != 0 {
		t.Fatalf("history notes = %d, want none after a failed summary", got)
	}
}

// TestSegmentBodyKeepsTrailerShapedText checks that a summarizer reply quoting a
// request block keeps every byte and stores no detectable block.
func TestSegmentBodyKeepsTrailerShapedText(t *testing.T) {
	body := "kept facts\n\n" + withRequestBlock("inner facts", "echoed request")
	stored := segmentBody(body)
	if strings.Contains(stored, requestBlockMarker) {
		t.Fatalf("segment body kept the request marker: %q", stored)
	}
	if _, _, ok := SplitRequestBlock(stored); ok {
		t.Fatalf("segment body parses as a request block: %q", stored)
	}
	for _, needle := range []string{"kept facts", "inner facts", "echoed request"} {
		if !strings.Contains(stored, needle) {
			t.Fatalf("segment body dropped %q: %q", needle, stored)
		}
	}
}

// TestSegmentedCompactionKeepsTrailerShapedSummary stores a summary that quotes a
// request block, checking the segment keeps the model text and the tail keeps the
// real block.
func TestSegmentedCompactionKeepsTrailerShapedSummary(t *testing.T) {
	full := segmentStageConversation()
	summary := "new facts\n\n" + withRequestBlock("quoted facts", "quoted request")
	providerStub := oneSummaryProvider(summary)
	req := segmentedCompactionRequest(providerStub, segmentedCompactionPolicy())
	state := stateFromMessages(full)
	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 4, candidate, "")
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	prefix := outcome.State.Lineage.Generations[len(outcome.State.Lineage.Generations)-1].SummaryPrefix
	segment := prefix[len(prefix)-2]
	if strings.Contains(segment.Content, requestBlockMarker) {
		t.Fatalf("segment kept the request marker: %q", segment.Content)
	}
	if _, _, ok := SplitRequestBlock(segment.Content); ok {
		t.Fatalf("segment parses as a request block: %q", segment.Content)
	}
	for _, needle := range []string{"new facts", "quoted facts", "quoted request"} {
		if !strings.Contains(segment.Content, needle) {
			t.Fatalf("segment dropped %q: %q", needle, segment.Content)
		}
	}
	tail := prefix[len(prefix)-1]
	if body, block, ok := SplitRequestBlock(tail.Content); !ok || body != "" || block != "span request" {
		t.Fatalf("tail = %q (body %q block %q ok %t), want the real request only", tail.Content, body, block, ok)
	}
}

func TestNewSegmentStageStrippedViewKeepsEmergencyReason(t *testing.T) {
	full := segmentStageConversation()
	req := segmentedCompactionRequest(oneSummaryProvider(), segmentedCompactionPolicy())
	stage, err := newSegmentStage(context.Background(), req, stateFromMessages(full), segmentStageInput{
		Policy:       req.SegmentedCompaction,
		Full:         full,
		Source:       full[:5],
		Retained:     full[5:],
		StrippedView: true,
		Mode:         prompt.CompactionModeEmergency,
		Model:        "test-model",
		Budget:       prompt.RequestTokenBudget{ContextSize: 100000, CompactionThresholdTokens: 70000},
		MaxTokens:    64,
	})
	if err != nil {
		t.Fatalf("newSegmentStage() error = %v", err)
	}
	if !stage.Decision.Rebase || stage.Decision.Reason != "emergency" {
		t.Fatalf("decision = %+v, want the emergency rebase reason", stage.Decision)
	}
}

// TestSegmentedAdoptWithoutRequestBlockRewritesOnlyTail covers the tail-only
// adoption a failed emergency retention reaches: the immutable segments keep
// their bytes, a tail that held only the block is removed, and the displayed
// summary no longer carries one.
func TestSegmentedAdoptWithoutRequestBlockRewritesOnlyTail(t *testing.T) {
	full := segmentStageConversation()
	providerStub := oneSummaryProvider("fresh append facts")
	req := segmentedCompactionRequest(providerStub, segmentedCompactionPolicy())
	state := stateFromMessages(full)
	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 4, candidate, "")
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if outcome.RequestBlock != output.RequestBlockNew {
		t.Fatalf("RequestBlock = %q, want %q", outcome.RequestBlock, output.RequestBlockNew)
	}
	if _, _, ok := SplitRequestBlock(outcome.SummaryText); !ok {
		t.Fatalf("SummaryText = %q, want the stored block", outcome.SummaryText)
	}
	before := outcome.State.Lineage.Generations[len(outcome.State.Lineage.Generations)-1].SummaryPrefix
	stages := summarizeCompactionStages{fitRunner: func(context.Context, RunRequest, RunState) (prompt.RequestTokenBudget, error) {
		return prompt.RequestTokenBudget{Fits: true}, nil
	}}
	adopted, ok := stages.adoptWithoutRequestBlock(context.Background(), req, outcome, &compactionBudgetMissError{err: errors.New("over budget")})
	if !ok {
		t.Fatal("adoptWithoutRequestBlock() ok = false, want the tail-only adoption")
	}
	prefix := adopted.State.Lineage.Generations[len(adopted.State.Lineage.Generations)-1].SummaryPrefix
	if got, want := mustJSON(t, prefix[:3]), mustJSON(t, before[:3]); !bytes.Equal(got, want) {
		t.Fatalf("segments changed:\n got %s\nwant %s", got, want)
	}
	if got, want := mustJSON(t, prefix[:2]), mustJSON(t, full[:2]); !bytes.Equal(got, want) {
		t.Fatalf("old segments changed:\n got %s\nwant %s", got, want)
	}
	if got := len(prefix); got != 3 {
		t.Fatalf("prefix length = %d, want only the 3 immutable segments once the block-only tail is removed", got)
	}
	for i, message := range prefix {
		if message.Segment == nil {
			t.Fatalf("prefix[%d] = %q, want no mutable tail left after its block was dropped", i, message.Content)
		}
	}
	if _, _, ok := SplitRequestBlock(adopted.SummaryText); ok {
		t.Fatalf("SummaryText kept the block: %q", adopted.SummaryText)
	}
	wantSummary, _, _ := SplitRequestBlock(outcome.SummaryText)
	if adopted.SummaryText != wantSummary {
		t.Fatalf("SummaryText = %q, want the display text without the block %q", adopted.SummaryText, wantSummary)
	}
	if adopted.RequestBlock != output.RequestBlockDropped || adopted.RequestBlockBytes != 0 {
		t.Fatalf("block status = %q bytes = %d, want the dropped marker", adopted.RequestBlock, adopted.RequestBlockBytes)
	}
}
