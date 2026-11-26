package agent

import (
	"encoding/json"
	"reflect"
	"testing"
)

func testSegment(sequence, count int) *SummarySegment {
	return &SummarySegment{Sequence: sequence, SourceMessageCount: count}
}

func summaryMessage(content string, segment *SummarySegment) Message {
	return Message{Role: MessageRoleSummary, Content: content, Segment: segment}
}

func TestScanSegmentChain(t *testing.T) {
	userTurn := Message{Role: MessageRoleUser, Content: "current request"}
	tests := []struct {
		name         string
		messages     []Message
		wantStatus   segmentChainStatus
		wantSegments []int
		wantCounts   []int
		wantTail     string
		wantEnd      int
	}{
		{
			name:       "no messages",
			wantStatus: chainNone,
		},
		{
			name:       "no leading summary",
			messages:   []Message{{Role: MessageRoleAssistant, Content: "hi"}, userTurn},
			wantStatus: chainNone,
		},
		{
			name:       "legacy or tail-only plain summary",
			messages:   []Message{summaryMessage("plain summary", nil), userTurn},
			wantStatus: chainNone,
			wantTail:   "plain summary",
			wantEnd:    1,
		},
		{
			name:         "single segment without tail",
			messages:     []Message{summaryMessage(segmentHeader(1), testSegment(1, 2)), userTurn},
			wantStatus:   chainValid,
			wantSegments: []int{1},
			wantCounts:   []int{2},
			wantEnd:      1,
		},
		{
			name:         "two segments without tail",
			messages:     []Message{summaryMessage(segmentHeader(1), testSegment(1, 2)), summaryMessage(segmentHeader(2), testSegment(2, 3)), userTurn},
			wantStatus:   chainValid,
			wantSegments: []int{1, 2},
			wantCounts:   []int{2, 3},
			wantEnd:      2,
		},
		{
			name:         "three segments without tail",
			messages:     []Message{summaryMessage(segmentHeader(1), testSegment(1, 2)), summaryMessage(segmentHeader(2), testSegment(2, 3)), summaryMessage(segmentHeader(3), testSegment(3, 4)), userTurn},
			wantStatus:   chainValid,
			wantSegments: []int{1, 2, 3},
			wantCounts:   []int{2, 3, 4},
			wantEnd:      3,
		},
		{
			name:         "two segments with tail",
			messages:     []Message{summaryMessage(segmentHeader(1), testSegment(1, 2)), summaryMessage(segmentHeader(2), testSegment(2, 3)), summaryMessage("mutable tail", nil), userTurn},
			wantStatus:   chainValid,
			wantSegments: []int{1, 2},
			wantCounts:   []int{2, 3},
			wantTail:     "mutable tail",
			wantEnd:      3,
		},
		{
			name:       "sequence gap",
			messages:   []Message{summaryMessage(segmentHeader(1), testSegment(1, 2)), summaryMessage(segmentHeader(3), testSegment(3, 2))},
			wantStatus: chainInvalid,
			wantEnd:    2,
		},
		{
			name:       "sequence starts at two",
			messages:   []Message{summaryMessage(segmentHeader(2), testSegment(2, 2))},
			wantStatus: chainInvalid,
			wantEnd:    1,
		},
		{
			name:       "repeated sequence",
			messages:   []Message{summaryMessage(segmentHeader(1), testSegment(1, 2)), summaryMessage(segmentHeader(1), testSegment(1, 3))},
			wantStatus: chainInvalid,
			wantEnd:    2,
		},
		{
			name:       "zero source message count",
			messages:   []Message{summaryMessage(segmentHeader(1), testSegment(1, 0))},
			wantStatus: chainInvalid,
			wantEnd:    1,
		},
		{
			name:       "negative source message count",
			messages:   []Message{summaryMessage(segmentHeader(1), testSegment(1, -1))},
			wantStatus: chainInvalid,
			wantEnd:    1,
		},
		{
			name:       "empty segment content",
			messages:   []Message{summaryMessage(segmentHeader(1), testSegment(1, 2)), summaryMessage("", testSegment(2, 3))},
			wantStatus: chainInvalid,
			wantEnd:    2,
		},
		{
			name:       "whitespace segment content",
			messages:   []Message{summaryMessage(" \n\t ", testSegment(1, 2))},
			wantStatus: chainInvalid,
			wantEnd:    1,
		},
		{
			name:       "segment after a plain summary",
			messages:   []Message{summaryMessage("plain", nil), summaryMessage(segmentHeader(1), testSegment(1, 2))},
			wantStatus: chainInvalid,
			wantEnd:    2,
		},
		{
			name:       "two plain summaries",
			messages:   []Message{summaryMessage("plain", nil), summaryMessage("plain again", nil)},
			wantStatus: chainInvalid,
			wantEnd:    2,
		},
		{
			name:       "two tails",
			messages:   []Message{summaryMessage(segmentHeader(1), testSegment(1, 2)), summaryMessage("tail", nil), summaryMessage("tail again", nil)},
			wantStatus: chainInvalid,
			wantEnd:    3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scan := scanSegmentChain(tt.messages)
			if scan.Status != tt.wantStatus {
				t.Fatalf("status = %d, want %d", scan.Status, tt.wantStatus)
			}
			if scan.End != tt.wantEnd {
				t.Fatalf("End = %d, want %d", scan.End, tt.wantEnd)
			}
			if len(scan.Segments) != len(tt.wantSegments) {
				t.Fatalf("segment count = %d, want %d", len(scan.Segments), len(tt.wantSegments))
			}
			for i, message := range scan.Segments {
				if message.Role != MessageRoleSummary {
					t.Errorf("segment %d role = %q, want %q", i, message.Role, MessageRoleSummary)
				}
				if message.Segment == nil {
					t.Fatalf("segment %d lost its metadata", i)
				}
				if message.Segment.Sequence != tt.wantSegments[i] || message.Segment.SourceMessageCount != tt.wantCounts[i] {
					t.Errorf("segment %d = %+v, want sequence %d count %d", i, *message.Segment, tt.wantSegments[i], tt.wantCounts[i])
				}
			}
			switch {
			case tt.wantTail == "" && scan.Tail != nil:
				t.Fatalf("tail = %q, want none", scan.Tail.Content)
			case tt.wantTail != "" && scan.Tail == nil:
				t.Fatalf("tail = nil, want %q", tt.wantTail)
			case tt.wantTail != "" && scan.Tail.Content != tt.wantTail:
				t.Fatalf("tail = %q, want %q", scan.Tail.Content, tt.wantTail)
			}
		})
	}
}

func TestScanSegmentChainReturnsClones(t *testing.T) {
	messages := []Message{
		summaryMessage(segmentHeader(1), testSegment(1, 2)),
		summaryMessage(segmentHeader(2), testSegment(2, 3)),
		summaryMessage("tail", nil),
		{Role: MessageRoleUser, Content: "current request"},
	}
	scan := scanSegmentChain(messages)
	if scan.Status != chainValid || len(scan.Segments) != 2 || scan.Tail == nil {
		t.Fatalf("scan = %+v, want a valid two-segment chain with a tail", scan)
	}
	if scan.Segments[0].Segment == messages[0].Segment {
		t.Fatal("segment metadata is shared with the source messages")
	}
	if scan.Tail == &messages[2] {
		t.Fatal("tail aliases the source summary message")
	}
	scan.Segments[0].Content = "mutated"
	scan.Segments[0].Segment.Sequence = 9
	scan.Tail.Content = "mutated tail"
	if messages[0].Content != segmentHeader(1) || messages[0].Segment.Sequence != 1 {
		t.Fatalf("source segment mutated: %+v", messages[0])
	}
	if messages[2].Content != "tail" {
		t.Fatalf("source tail mutated: %q", messages[2].Content)
	}
}

func TestScanSegmentChainIgnoresGenerationLayout(t *testing.T) {
	chain := []Message{
		summaryMessage(segmentHeader(1), testSegment(1, 2)),
		summaryMessage(segmentHeader(2), testSegment(2, 3)),
		summaryMessage("mutable tail", nil),
	}
	// Persisted shape: the chain lives in SummaryPrefix.
	persisted := ConversationGeneration{
		ID:            7,
		SummaryPrefix: chain,
		Messages:      []Message{{Role: MessageRoleUser, Content: "current request"}},
	}
	// In-run shape: seeding and manual compaction flatten the chain into Messages.
	flattened := ConversationGeneration{
		ID: 1,
		Messages: []Message{
			chain[0], chain[1], chain[2],
			{Role: MessageRoleUser, Content: "current request"},
		},
	}

	gotPersisted := scanSegmentChain(persisted.FullMessages())
	gotFlattened := scanSegmentChain(flattened.FullMessages())
	if !reflect.DeepEqual(gotPersisted, gotFlattened) {
		t.Fatalf("scan of persisted chain = %+v, want the flattened scan %+v", gotPersisted, gotFlattened)
	}
	if gotPersisted.Status != chainValid || len(gotPersisted.Segments) != 2 || gotPersisted.Tail == nil {
		t.Fatalf("scan = %+v, want a valid two-segment chain with a tail", gotPersisted)
	}
	if gotPersisted.End != 3 {
		t.Fatalf("End = %d, want 3", gotPersisted.End)
	}
	tailOnly := ConversationGeneration{ID: 1, Messages: []Message{summaryMessage("legacy summary", nil)}}
	if scan := scanSegmentChain(tailOnly.FullMessages()); scan.Status != chainNone || scan.Tail == nil {
		t.Fatalf("scan of flattened legacy summary = %+v, want chainNone with a tail", scan)
	}
}

// TestScanSegmentChainShape pins the clone-free validator that
// scanSegmentChain and request projection share: its segment count, run end and
// status must agree with the scanning form for every chain shape.
func TestScanSegmentChainShape(t *testing.T) {
	userTurn := Message{Role: MessageRoleUser, Content: "current request"}
	tests := []struct {
		name     string
		messages []Message
		segments int
		end      int
		status   segmentChainStatus
	}{
		{name: "no leading summary", messages: []Message{userTurn}, status: chainNone},
		{name: "tail only", messages: []Message{summaryMessage("plain summary", nil), userTurn}, end: 1, status: chainNone},
		{name: "valid chain with tail", messages: []Message{summaryMessage(segmentHeader(1), testSegment(1, 2)), summaryMessage("mutable tail", nil), userTurn}, segments: 1, end: 2, status: chainValid},
		{name: "invalid source count", messages: []Message{summaryMessage(segmentHeader(1), testSegment(1, 0)), userTurn}, end: 1, status: chainInvalid},
		{name: "empty segment text", messages: []Message{summaryMessage("  ", testSegment(1, 2)), userTurn}, end: 1, status: chainInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			segments, end, status := scanSegmentChainShape(tt.messages)
			if segments != tt.segments || end != tt.end || status != tt.status {
				t.Fatalf("scanSegmentChainShape() = (%d, %d, %v), want (%d, %d, %v)", segments, end, status, tt.segments, tt.end, tt.status)
			}
			scan := scanSegmentChain(tt.messages)
			if scan.Status != status || scan.End != end || len(scan.Segments) != segments {
				t.Fatalf("scanSegmentChain() = status %v end %d segments %d, want status %v end %d segments %d", scan.Status, scan.End, len(scan.Segments), status, end, segments)
			}
		})
	}
}

func TestSegmentHeader(t *testing.T) {
	if got, want := segmentHeader(3), "Segment 3. Later segments override earlier conflicting state.\n\n"; got != want {
		t.Fatalf("segmentHeader(3) = %q, want %q", got, want)
	}
}

func TestMessageSegmentJSON(t *testing.T) {
	legacy := Message{Role: MessageRoleSummary, Content: "plain summary"}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy message: %v", err)
	}
	if got, want := string(encoded), `{"role":"summary","content":"plain summary"}`; got != want {
		t.Fatalf("legacy message JSON = %s, want %s", got, want)
	}

	segmented := Message{
		Role:    MessageRoleSummary,
		Content: segmentHeader(1) + "summary",
		Segment: testSegment(1, 7),
	}
	segmentJSON, err := json.Marshal(segmented.Segment)
	if err != nil {
		t.Fatalf("marshal segment: %v", err)
	}
	if got, want := string(segmentJSON), `{"sequence":1,"source_message_count":7}`; got != want {
		t.Fatalf("segment JSON = %s, want %s", got, want)
	}
	encoded, err = json.Marshal(segmented)
	if err != nil {
		t.Fatalf("marshal segmented message: %v", err)
	}
	var decoded Message
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal segmented message: %v", err)
	}
	if !reflect.DeepEqual(decoded.Segment, segmented.Segment) {
		t.Fatalf("segment round trip = %+v, want %+v", decoded.Segment, segmented.Segment)
	}
}
