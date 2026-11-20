package output

import "testing"

func TestFormatCompactionFallbackSummary(t *testing.T) {
	applied := ContextCompactionEvent{
		Scope:                "conversation",
		Turn:                 3,
		Severity:             "ok",
		SummarizedMessages:   4,
		RetainedMessages:     2,
		RetainedUnits:        1,
		RetainedUserMessages: 1,
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
			want:    "ok: compaction turn 3 4 messages summarized; 2 messages retained in 1 units; 1 user / 0 sub-agent result messages retained; retained units=1",
		},
		{
			name:    "whitespace-only fallback fields are omitted",
			payload: blank,
			want:    "ok: compaction turn 3 4 messages summarized; 2 messages retained in 1 units; 1 user / 0 sub-agent result messages retained; retained units=1",
		},
		{
			name: "fallback used appends model, status, and reason",
			payload: ContextCompactionEvent{
				Scope:                applied.Scope,
				Turn:                 applied.Turn,
				Severity:             applied.Severity,
				SummarizedMessages:   applied.SummarizedMessages,
				RetainedMessages:     applied.RetainedMessages,
				RetainedUnits:        applied.RetainedUnits,
				RetainedUserMessages: applied.RetainedUserMessages,
				CompactionModel:      "claude-haiku-5-5",
				CompactionModelAlias: "haiku",
				FallbackStatus:       CompactionFallbackUsed,
				FallbackReason:       "primary over budget",
			},
			want: "ok: compaction turn 3 4 messages summarized; 2 messages retained in 1 units; 1 user / 0 sub-agent result messages retained; retained units=1; compaction_model=claude-haiku-5-5; compaction_model_alias=haiku; fallback_status=used; fallback_reason=primary over budget",
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
