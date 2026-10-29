package output

import "testing"

func TestFormatCompactionFallbackSummary(t *testing.T) {
	applied := ContextCompactionEvent{
		Scope:             "conversation",
		Turn:              3,
		Severity:          "ok",
		CompactedTurns:    2,
		CompactedMessages: 4,
		RetainedTurns:     1,
		RetainedMessages:  2,
	}
	blank := applied
	blank.CompactionModel = " "
	blank.FallbackStatus = " "
	blank.FallbackReason = " "
	skipped := ContextCompactionEvent{
		Scope:    "conversation",
		Turn:     3,
		Severity: "skipped",
	}

	tests := []struct {
		name    string
		payload ContextCompactionEvent
		want    string
	}{
		{
			name:    "unset fallback fields leave summary unchanged",
			payload: applied,
			want:    "ok: compaction turn 3 compacted 2 turns/4 messages; retained 1 turn/2 messages",
		},
		{
			name:    "whitespace-only fallback fields are omitted",
			payload: blank,
			want:    "ok: compaction turn 3 compacted 2 turns/4 messages; retained 1 turn/2 messages",
		},
		{
			name: "fallback used appends model, status, and reason",
			payload: ContextCompactionEvent{
				Scope:             applied.Scope,
				Turn:              applied.Turn,
				Severity:          applied.Severity,
				CompactedTurns:    applied.CompactedTurns,
				CompactedMessages: applied.CompactedMessages,
				RetainedTurns:     applied.RetainedTurns,
				RetainedMessages:  applied.RetainedMessages,
				CompactionModel:   "claude-haiku-5-5",
				FallbackStatus:    CompactionFallbackUsed,
				FallbackReason:    "primary over budget",
			},
			want: "ok: compaction turn 3 compacted 2 turns/4 messages; retained 1 turn/2 messages; compaction_model=claude-haiku-5-5; fallback_status=used; fallback_reason=primary over budget",
		},
		{
			name: "fallback skipped appends status and reason",
			payload: ContextCompactionEvent{
				Scope:          skipped.Scope,
				Turn:           skipped.Turn,
				Severity:       skipped.Severity,
				FallbackStatus: CompactionFallbackSkipped,
				FallbackReason: "no fallback configured",
			},
			want: "skipped: compaction turn 3; fallback_status=skipped; fallback_reason=no fallback configured",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatAnyContextDiagnosticsEvent(tt.payload); got != tt.want {
				t.Errorf("typed summary = %q, want %q", got, tt.want)
			}
			if got := formatContextDiagnosticsEvent(tt.payload.toLegacyContextDiagnostics()); got != tt.want {
				t.Errorf("legacy summary = %q, want %q", got, tt.want)
			}
		})
	}
}
