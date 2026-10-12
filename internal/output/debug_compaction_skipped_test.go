package output

import (
	"strings"
	"testing"
)

func TestFormatCompactionSkippedSummary(t *testing.T) {
	skipped := ContextDiagnosticsEvent{
		Kind:     "compaction",
		Scope:    "conversation",
		Turn:     3,
		Severity: "skipped",
		Notes:    []string{"compaction skipped: no stage applied"},
	}
	applied := ContextDiagnosticsEvent{
		Kind:              "compaction",
		Scope:             "conversation",
		Turn:              3,
		Severity:          "ok",
		CompactedTurns:    2,
		CompactedMessages: 4,
	}

	tests := []struct {
		name        string
		payload     ContextDiagnosticsEvent
		wantPrefix  string
		wantContain []string
		wantAbsent  []string
	}{
		{
			name:        "skipped reports no compaction",
			payload:     skipped,
			wantPrefix:  "skipped: compaction turn 3",
			wantContain: []string{"compaction skipped: no stage applied"},
			wantAbsent:  []string{"compacted "},
		},
		{
			name:        "applied keeps compacted counts",
			payload:     applied,
			wantPrefix:  "ok: compaction turn 3",
			wantContain: []string{"compacted 2 turns/4 messages"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatContextDiagnosticsEvent(tt.payload)
			if !strings.HasPrefix(got, tt.wantPrefix) {
				t.Errorf("summary = %q, want prefix %q", got, tt.wantPrefix)
			}
			for _, want := range tt.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("summary = %q, want %q", got, want)
				}
			}
			for _, bad := range tt.wantAbsent {
				if strings.Contains(got, bad) {
					t.Errorf("summary = %q, must not contain %q", got, bad)
				}
			}
		})
	}
}
