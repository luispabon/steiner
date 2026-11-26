package agent

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/prompt"
)

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
	if count, ok := segmentAppendSourceCount(activeSkillBlocks(stateFromMessages(fullMessages)), fullMessages, scan, start); !ok || count == 0 {
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
			got, ok := segmentAppendSourceCount(activeSkillBlocks(state), full, scan, tt.retentionStart)
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

	count, ok := segmentAppendSourceCount(activeSkillBlocks(state), full, scan, 2)
	if !ok || count != 0 {
		t.Fatalf("replay-only span count = %d, %t, want 0, true", count, ok)
	}
	decision := decideSegmentAction(segmentDecisionInput{
		Policy:               config.SegmentedCompactionPolicy{MaxSegments: 4, MaxChainPercent: 12.5, MaxPostCompactionPercent: 50},
		Chain:                scan,
		Mode:                 prompt.CompactionModeNormal,
		ContextSize:          20000,
		CompactionThreshold:  8000,
		RetentionStart:       2,
		AppendSourceMessages: count,
	})
	if !decision.Rebase || decision.Reason != segmentReasonEmptySpan {
		t.Fatalf("decision = %+v, want a rebase with reason empty-span", decision)
	}

	count, ok = segmentAppendSourceCount(activeSkillBlocks(state), full, scan, len(full))
	if !ok || count != 1 {
		t.Fatalf("span with the request count = %d, %t, want 1, true", count, ok)
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
			if stage.Decision.Plain != tt.wantPlain {
				t.Fatalf("plain = %t, want %t", stage.Decision.Plain, tt.wantPlain)
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
	if !stage.Decision.Rebase || stage.Decision.Reason != segmentReasonEmergency {
		t.Fatalf("decision = %+v, want the emergency rebase reason", stage.Decision)
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
				span, ok := segmentAppendSourceCount(activeSkillBlocks(state), full, scan, start)
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
