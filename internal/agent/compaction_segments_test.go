package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
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
	if got, want := segment.Content, segmentHeader(3)+"new facts"; got != want {
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
	if got, want := segment.Content, segmentHeader(1)+"rebased facts"; got != want {
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
	if count, ok := segmentAppendSourceCount(fullMessages, scan, start); !ok || count == 0 {
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
			got, ok := segmentAppendSourceCount(full, scan, tt.retentionStart)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("segmentAppendSourceCount() = %d, %t, want %d, %t", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestStripTailRequestBlocks(t *testing.T) {
	source := []Message{
		{Role: MessageRoleSummary, Content: withRequestBlock("Segment 1. facts", "segment request"), Segment: testSegment(1, 2)},
		{Role: MessageRoleSummary, Content: withRequestBlock("[steiner:history] archived", "tail request")},
		{Role: MessageRoleUser, Content: "current request"},
	}
	out, stripped := stripTailRequestBlocks(source)
	if !stripped {
		t.Fatal("stripTailRequestBlocks() reported no drop")
	}
	if out[0].Content != source[0].Content {
		t.Fatalf("segment rewritten to %q, want %q", out[0].Content, source[0].Content)
	}
	body, request, ok := SplitRequestBlock(out[1].Content)
	if ok || body != "[steiner:history] archived" || request != "" {
		t.Fatalf("tail = %q, want the note without a block", out[1].Content)
	}
	if out[2].Content != source[2].Content {
		t.Fatal("non-summary message rewritten")
	}
	if source[1].Content == out[1].Content {
		t.Fatal("source slice mutated")
	}
}

func TestDropTailRequestBlockState(t *testing.T) {
	segment := Message{Role: MessageRoleSummary, Content: withRequestBlock("Segment 1. facts", "segment request"), Segment: testSegment(1, 2)}
	tail := Message{Role: MessageRoleSummary, Content: withRequestBlock("[steiner:history] archived", "tail request")}
	state := RunState{Lineage: ConversationLineage{
		Generations:      []ConversationGeneration{{ID: 2, SummaryPrefix: []Message{segment, tail}, Messages: []Message{{Role: MessageRoleUser, Content: "current request"}}}},
		NextGenerationID: 3,
	}}
	state.Conversation = state.Lineage.FullMessages()
	segmentJSON := mustJSON(t, []Message{segment})

	next, summaryText, dropped := dropTailRequestBlockState(state)
	if !dropped {
		t.Fatal("dropTailRequestBlockState() reported no drop")
	}
	if summaryText != "[steiner:history] archived" {
		t.Fatalf("summary text = %q, want the tail body", summaryText)
	}
	prefix := next.Lineage.Generations[0].SummaryPrefix
	if got := mustJSON(t, prefix[:1]); !bytes.Equal(got, segmentJSON) {
		t.Fatalf("segment changed:\n got %s\nwant %s", got, segmentJSON)
	}
	if _, _, ok := SplitRequestBlock(prefix[1].Content); ok || prefix[1].Content != "[steiner:history] archived" {
		t.Fatalf("tail = %q, want the note without a block", prefix[1].Content)
	}
	if !sameMessages(next.Conversation, next.Lineage.FullMessages()) {
		t.Fatal("conversation does not match the new lineage")
	}
	if state.Lineage.Generations[0].SummaryPrefix[0].Content != segment.Content {
		t.Fatal("source state segment mutated")
	}
	if state.Lineage.Generations[0].SummaryPrefix[1].Content != tail.Content {
		t.Fatal("source state tail mutated")
	}
}
