package agent

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSegmentChain(t *testing.T) {
	segment := func(sequence, source int) *SummarySegment {
		return &SummarySegment{Sequence: sequence, SourceGenerationID: source, SourceMessageCount: 4}
	}
	summary := func(content string, seg *SummarySegment) Message {
		return Message{Role: MessageRoleSummary, Content: content, Segment: seg}
	}
	tests := []struct {
		name           string
		generation     ConversationGeneration
		wantOK         bool
		wantSequences  []int
		wantSourceGens []int
		wantTail       bool
	}{
		{
			name:           "single segment",
			generation:     ConversationGeneration{ID: 2, SummaryPrefix: []Message{summary("one", segment(1, 1))}},
			wantOK:         true,
			wantSequences:  []int{1},
			wantSourceGens: []int{1},
		},
		{
			name: "chain with tail",
			generation: ConversationGeneration{ID: 4, SummaryPrefix: []Message{
				summary("one", segment(1, 1)),
				summary("two", segment(2, 3)),
				summary("tail", nil),
			}},
			wantOK:         true,
			wantSequences:  []int{1, 2},
			wantSourceGens: []int{1, 3},
			wantTail:       true,
		},
		{
			name: "chain without tail",
			generation: ConversationGeneration{ID: 5, SummaryPrefix: []Message{
				summary("one", segment(1, 1)),
				summary("two", segment(2, 4)),
			}},
			wantOK:         true,
			wantSequences:  []int{1, 2},
			wantSourceGens: []int{1, 4},
		},
		{
			name:       "empty prefix",
			generation: ConversationGeneration{ID: 2},
		},
		{
			name:       "legacy plain summary",
			generation: ConversationGeneration{ID: 2, SummaryPrefix: []Message{summary("plain", nil)}},
		},
		{
			name: "sequence gap",
			generation: ConversationGeneration{ID: 4, SummaryPrefix: []Message{
				summary("one", segment(1, 1)),
				summary("three", segment(3, 2)),
			}},
		},
		{
			name:       "sequence starts at two",
			generation: ConversationGeneration{ID: 3, SummaryPrefix: []Message{summary("two", segment(2, 1))}},
		},
		{
			name: "repeated sequence",
			generation: ConversationGeneration{ID: 4, SummaryPrefix: []Message{
				summary("one", segment(1, 1)),
				summary("one again", segment(1, 2)),
			}},
		},
		{
			name: "non-increasing source generation",
			generation: ConversationGeneration{ID: 4, SummaryPrefix: []Message{
				summary("one", segment(1, 2)),
				summary("two", segment(2, 2)),
			}},
		},
		{
			name: "decreasing source generation",
			generation: ConversationGeneration{ID: 5, SummaryPrefix: []Message{
				summary("one", segment(1, 3)),
				summary("two", segment(2, 2)),
			}},
		},
		{
			name:       "source generation equals current generation",
			generation: ConversationGeneration{ID: 2, SummaryPrefix: []Message{summary("one", segment(1, 2))}},
		},
		{
			name: "source generation above current generation",
			generation: ConversationGeneration{ID: 3, SummaryPrefix: []Message{
				summary("one", segment(1, 1)),
				summary("two", segment(2, 5)),
			}},
		},
		{
			name: "tail before a segment",
			generation: ConversationGeneration{ID: 4, SummaryPrefix: []Message{
				summary("one", segment(1, 1)),
				summary("tail", nil),
				summary("two", segment(2, 2)),
			}},
		},
		{
			name: "two tails",
			generation: ConversationGeneration{ID: 3, SummaryPrefix: []Message{
				summary("one", segment(1, 1)),
				summary("tail", nil),
				summary("tail again", nil),
			}},
		},
		{
			name: "non-summary prefix message",
			generation: ConversationGeneration{ID: 3, SummaryPrefix: []Message{
				summary("one", segment(1, 1)),
				{Role: MessageRoleUser, Content: "user"},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			segments, tail, ok := segmentChain(tt.generation)
			if ok != tt.wantOK {
				t.Fatalf("ok = %t, want %t", ok, tt.wantOK)
			}
			if !tt.wantOK {
				if segments != nil || tail != nil {
					t.Fatalf("invalid chain returned segments=%v tail=%v, want nil", segments, tail)
				}
				return
			}
			if len(segments) != len(tt.wantSequences) {
				t.Fatalf("segment count = %d, want %d", len(segments), len(tt.wantSequences))
			}
			for i, message := range segments {
				if message.Role != MessageRoleSummary {
					t.Fatalf("segment %d role = %q, want %q", i, message.Role, MessageRoleSummary)
				}
				if message.Segment == nil {
					t.Fatalf("segment %d lost its metadata", i)
				}
				if message.Segment.Sequence != tt.wantSequences[i] || message.Segment.SourceGenerationID != tt.wantSourceGens[i] {
					t.Fatalf("segment %d = %+v, want sequence %d source %d", i, *message.Segment, tt.wantSequences[i], tt.wantSourceGens[i])
				}
			}
			if tt.wantTail != (tail != nil) {
				t.Fatalf("tail present = %t, want %t", tail != nil, tt.wantTail)
			}
		})
	}
}

func TestSegmentChainReturnsClones(t *testing.T) {
	prefix := []Message{
		{Role: MessageRoleSummary, Content: "one", Segment: &SummarySegment{Sequence: 1, SourceGenerationID: 1, SourceMessageCount: 2}},
		{Role: MessageRoleSummary, Content: "two", Segment: &SummarySegment{Sequence: 2, SourceGenerationID: 3, SourceMessageCount: 4}},
		{Role: MessageRoleSummary, Content: "tail"},
	}
	generation := ConversationGeneration{ID: 4, SummaryPrefix: prefix}
	segments, tail, ok := segmentChain(generation)
	if !ok {
		t.Fatal("chain rejected, want valid")
	}
	if len(segments) != 2 || tail == nil {
		t.Fatalf("got %d segments and tail=%v, want 2 segments and a tail", len(segments), tail)
	}
	if segments[0].Segment == prefix[0].Segment {
		t.Fatal("segment metadata is shared with the source generation")
	}
	if tail == &generation.SummaryPrefix[2] {
		t.Fatal("tail aliases the source summary message")
	}
	segments[0].Content = "mutated"
	segments[0].Segment.Sequence = 9
	tail.Content = "mutated tail"
	if generation.SummaryPrefix[0].Content != "one" || generation.SummaryPrefix[0].Segment.Sequence != 1 {
		t.Fatalf("source segment mutated: %+v", generation.SummaryPrefix[0])
	}
	if generation.SummaryPrefix[2].Content != "tail" {
		t.Fatalf("source tail mutated: %q", generation.SummaryPrefix[2].Content)
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
		Segment: &SummarySegment{Sequence: 1, SourceGenerationID: 2, SourceMessageCount: 7},
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
