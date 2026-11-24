package output

import (
	"slices"
	"testing"
)

func TestFormatCompactionFailure(t *testing.T) {
	tests := []struct {
		name    string
		payload ContextDiagnosticsEvent
		want    []string
	}{
		{
			name:    "failed empty summary reports result, attempts, reason and tool calls",
			payload: ContextDiagnosticsEvent{CompactionResult: CompactionResultFailed, SummaryAttempts: 3, SummaryFailureReason: "tool_calls", SummaryToolCalls: 2},
			want:    []string{"compaction_result=failed", "summary_attempts=3", "summary_failure_reason=tool_calls", "summary_tool_calls=2"},
		},
		{
			name:    "zero fields are omitted",
			payload: ContextDiagnosticsEvent{},
			want:    []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatCompactionFailure(tt.payload); !slices.Equal(got, tt.want) {
				t.Errorf("formatCompactionFailure() = %q, want %q", got, tt.want)
			}
		})
	}
}
