package agent

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
)

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
	state := stateFromMessages(full)
	next, err := buildSegmentCompactionState(state, segmentStateInput{
		Scan:     scanSegmentChain(full),
		Source:   full[1:3],
		Skills:   activeSkillBlocks(state),
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

// TestBuildCompactionStageStateNoteOnlySummary pins a summary whose text is only
// a history note: the stored plain summary keeps the blank line before the note,
// while the segmented display text omits it.
func TestBuildCompactionStageStateNoteOnlySummary(t *testing.T) {
	const note = "[steiner:history] archived to sessions/only.jsonl"
	req := RunRequest{HistoryArchive: &fakeHistoryArchive{note: note}, Events: output.NoopSink{}}
	noted, gotNote, _ := stageHistoryNote(req, note, []Message{{Role: MessageRoleUser, Content: "request"}})
	if gotNote != note {
		t.Fatalf("note = %q, want %q", gotNote, note)
	}
	if want := "\n\n" + note; noted != want {
		t.Fatalf("noted = %q, want the plain shape %q", noted, want)
	}

	state := stateFromMessages([]Message{{Role: MessageRoleUser, Content: "live"}})
	segment := &segmentStage{Decision: segmentDecision{Rebase: true}, Source: []Message{{Role: MessageRoleUser, Content: "span"}}}
	_, display, err := buildCompactionStageState(state, segment, nil, noted, gotNote, "", "", nil)
	if err != nil {
		t.Fatalf("buildCompactionStageState() error = %v", err)
	}
	if display != note {
		t.Fatalf("segmented display = %q, want the note without a leading separator %q", display, note)
	}

	_, stored, err := buildCompactionStageState(state, nil, nil, noted, gotNote, "", "", nil)
	if err != nil {
		t.Fatalf("buildCompactionStageState() error = %v", err)
	}
	if want := "\n\n" + note; stored != want {
		t.Fatalf("plain stored summary = %q, want %q", stored, want)
	}
}
