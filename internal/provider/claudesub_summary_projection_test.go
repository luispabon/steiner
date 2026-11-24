package provider

import (
	"errors"
	"strings"
	"testing"
)

// TestClaudeSubPlanSummaryRefusalMismatch proves planSummary names why the live
// transcript cannot take a summary request: a record-state refusal reports its
// label at no request position with no role, a count problem reports length at
// no position, and a request comparison reports its position, role and one of
// the existing check labels. Every refusal keeps errClaudeSubSummaryNoPlace. An
// interrupted record is accepted only when its interrupted assistant lies in the
// retained tail after the source.
func TestClaudeSubPlanSummaryRefusalMismatch(t *testing.T) {
	u1 := claudeSubUserText("first")
	a1 := Message{Role: MessageRoleAssistant, Content: "answer"}
	u2 := claudeSubUserText("second")
	instruction := claudeSubUserText("summarize")
	base := func() *claudeSubSync {
		return &claudeSubSync{started: true, interruptible: -1, entries: []claudeSubEntry{
			claudeSubEntryFromMessage(u1),
			claudeSubEntryFromMessage(a1),
			claudeSubEntryFromMessage(u2),
		}}
	}
	tests := []struct {
		name      string
		sync      func() *claudeSubSync
		msgs      []Message
		source    int
		wantOK    bool
		wantIndex int
		wantRole  MessageRole
		wantCheck string
	}{
		{
			name:   "accepted source prefix",
			sync:   base,
			msgs:   []Message{u1, instruction},
			source: 1,
			wantOK: true,
		},
		{
			name: "tainted record",
			sync: func() *claudeSubSync {
				s := base()
				s.tainted = true
				return s
			},
			msgs:      []Message{u1, instruction},
			source:    1,
			wantIndex: claudeSubSyncMismatchNoIndex,
			wantCheck: claudeSubCheckTainted,
		},
		{
			name:      "record never started",
			sync:      func() *claudeSubSync { return &claudeSubSync{interruptible: -1} },
			msgs:      []Message{u1, instruction},
			source:    1,
			wantIndex: claudeSubSyncMismatchNoIndex,
			wantCheck: claudeSubCheckNotStarted,
		},
		{
			name: "interrupted assistant inside the source",
			sync: func() *claudeSubSync {
				s := base()
				s.interrupted = true
				s.interruptible = 1
				return s
			},
			msgs:      []Message{u1, a1, instruction},
			source:    2,
			wantIndex: claudeSubSyncMismatchNoIndex,
			wantCheck: claudeSubCheckInterrupted,
		},
		{
			name: "interrupted assistant in the retained tail",
			sync: func() *claudeSubSync {
				s := base()
				s.interrupted = true
				s.interruptible = 2
				s.entries[2] = claudeSubEntryFromMessage(Message{Role: MessageRoleAssistant, ToolCalls: []ToolCall{{ID: "toolu_1", Name: "read"}}})
				return s
			},
			msgs:   []Message{u1, a1, instruction},
			source: 2,
			wantOK: true,
		},
		{
			name: "interrupted record without an interruptible assistant",
			sync: func() *claudeSubSync {
				s := base()
				s.interrupted = true
				return s
			},
			msgs:      []Message{u1, instruction},
			source:    1,
			wantIndex: claudeSubSyncMismatchNoIndex,
			wantCheck: claudeSubCheckInterrupted,
		},
		{
			name: "drained record",
			sync: func() *claudeSubSync {
				s := base()
				s.drained = true
				return s
			},
			msgs:      []Message{u1, instruction},
			source:    1,
			wantIndex: claudeSubSyncMismatchNoIndex,
			wantCheck: claudeSubCheckDrained,
		},
		{
			name:      "source without exactly one instruction after it",
			sync:      base,
			msgs:      []Message{u1, a1, instruction},
			source:    1,
			wantIndex: claudeSubSyncMismatchNoIndex,
			wantCheck: claudeSubCheckLength,
		},
		{
			name:      "source zero",
			sync:      base,
			msgs:      []Message{instruction},
			source:    0,
			wantIndex: claudeSubSyncMismatchNoIndex,
			wantCheck: claudeSubCheckLength,
		},
		{
			name:      "source beyond the record",
			sync:      base,
			msgs:      []Message{u1, a1, u2, instruction, instruction},
			source:    4,
			wantIndex: claudeSubSyncMismatchNoIndex,
			wantCheck: claudeSubCheckLength,
		},
		{
			name:      "instruction is not a user message",
			sync:      base,
			msgs:      []Message{u1, a1},
			source:    1,
			wantIndex: 1,
			wantRole:  MessageRoleAssistant,
			wantCheck: claudeSubCheckShape,
		},
		{
			name:      "source prefix mismatch",
			sync:      base,
			msgs:      []Message{claudeSubUserText("other"), instruction},
			source:    1,
			wantIndex: 0,
			wantRole:  MessageRoleUser,
			wantCheck: claudeSubCheckDigest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mismatch claudeSubSyncMismatch
			_, err := tt.sync().planSummary(tt.msgs, tt.source, &mismatch)
			if tt.wantOK {
				if err != nil {
					t.Fatalf("planSummary() error = %v, want the summary placed in place", err)
				}
				if mismatch != (claudeSubSyncMismatch{}) {
					t.Fatalf("mismatch = %+v, want none for an accepted plan", mismatch)
				}
				return
			}
			if !errors.Is(err, errClaudeSubSummaryNoPlace) {
				t.Fatalf("planSummary() error = %v, want errClaudeSubSummaryNoPlace", err)
			}
			want := claudeSubSyncMismatch{Index: tt.wantIndex, Role: tt.wantRole, Check: tt.wantCheck}
			if mismatch != want {
				t.Fatalf("mismatch = %+v, want %+v", mismatch, want)
			}
		})
	}
}

// TestClaudeSubMarkSummaryText pins the marked instruction bytes. A completed
// record keeps the historical wording byte for byte, and only a record planned
// as interrupted adds the transport-artifact disclaimer, which planSummary
// passes through from the record state.
func TestClaudeSubMarkSummaryText(t *testing.T) {
	instruction := claudeSubUserText("summarize")
	const completed = "[steiner compaction summary request: summarize the 3 older source messages earlier in this transcript. The 2 retained tail messages after them are already part of this transcript.]\n\nsummarize"
	const interrupted = "[steiner compaction summary request: summarize the 3 older source messages earlier in this transcript. The 2 retained tail messages after them are already part of this transcript. The tool rejection and interrupt lines the CLI wrote after the source are transport artifacts from cancelling a pending tool call: do not summarize them as user intent or as the outcome of those tool calls.]\n\nsummarize"
	tests := []struct {
		name        string
		interrupted bool
		want        string
	}{
		{name: "completed record", want: completed},
		{name: "interrupted record adds the artifact disclaimer", interrupted: true, want: interrupted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := claudeSubMarkSummary(instruction, 3, 2, tt.interrupted).Content; got != tt.want {
				t.Errorf("marked instruction = %q, want %q", got, tt.want)
			}
		})
	}

	u1 := claudeSubUserText("first")
	toolCall := Message{Role: MessageRoleAssistant, ToolCalls: []ToolCall{{ID: "toolu_1", Name: "read"}}}
	base := []claudeSubEntry{claudeSubEntryFromMessage(u1), claudeSubEntryFromMessage(toolCall)}
	for _, tt := range []struct {
		name           string
		record         *claudeSubSync
		wantDisclaimer bool
	}{
		{name: "completed plan keeps the clear text", record: &claudeSubSync{started: true, interruptible: -1, entries: base}},
		{name: "interrupted plan adds the disclaimer", record: &claudeSubSync{started: true, interrupted: true, interruptible: 1, entries: base}, wantDisclaimer: true},
	} {
		t.Run("plan: "+tt.name, func(t *testing.T) {
			delta, err := tt.record.planSummary([]Message{u1, instruction}, 1, nil)
			if err != nil {
				t.Fatalf("planSummary() error = %v, want the summary placed in place", err)
			}
			if len(delta.User) != 1 {
				t.Fatalf("planSummary() user messages = %d, want one marked instruction", len(delta.User))
			}
			if got := strings.Contains(delta.User[0].Content, claudeSubSummaryArtifactDisclaimer); got != tt.wantDisclaimer {
				t.Errorf("marked instruction carries the disclaimer = %v, want %v: %q", got, tt.wantDisclaimer, delta.User[0].Content)
			}
		})
	}
}

// still matches a live transcript that carries the projected latest-request
// block. The agent sends the summary request with the same projection as the
// main request, so the recorded digest of the summary message matches; a source
// without the block does not match and would reseed.
func TestClaudeSubPlanSummaryMatchesBlockProjection(t *testing.T) {
	summary := claudeSubUserText("<summary>\nfirst summary\n</summary>\n\n<latest_user_request>\nfirst request\n</latest_user_request>")
	answer := Message{Role: MessageRoleAssistant, Content: "answer"}
	follow := claudeSubUserText("second request")
	s := &claudeSubSync{started: true, interruptible: -1, entries: []claudeSubEntry{
		claudeSubEntryFromMessage(summary),
		claudeSubEntryFromMessage(answer),
		claudeSubEntryFromMessage(follow),
	}}
	instruction := claudeSubUserText("summarize")

	tests := []struct {
		name   string
		source Message
		wantOK bool
	}{
		{name: "main projection matches", source: summary, wantOK: true},
		{name: "block-stripped source does not match", source: claudeSubUserText("<summary>\nfirst summary\n</summary>")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.planSummary([]Message{tt.source, answer, instruction}, 2, nil)
			if !tt.wantOK {
				if !errors.Is(err, errClaudeSubSummaryNoPlace) {
					t.Fatalf("planSummary() error = %v, want errClaudeSubSummaryNoPlace", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("planSummary() error = %v, want the summary placed in place", err)
			}
		})
	}
}
