package output

import (
	"slices"
	"testing"
)

// summaryTokens returns a pointer to n, for the CompletionTokens of a reported usage.
func summaryTokens(n int) *int { return &n }

// TestCompactionSummaryAttemptString proves String keeps a reported zero and omits
// unreported completion tokens and empty fields.
func TestCompactionSummaryAttemptString(t *testing.T) {
	tests := []struct {
		name    string
		attempt CompactionSummaryAttempt
		want    string
	}{
		{
			name:    "reported zero tokens and every field",
			attempt: CompactionSummaryAttempt{Attempt: 2, FinishReason: "stop", CompletionTokens: summaryTokens(0), ToolCalls: 1, ReasoningContent: true, Reason: "tool_calls"},
			want:    "attempt=2 finish_reason=stop completion_tokens=0 tool_calls=1 reasoning_content=true reason=tool_calls",
		},
		{
			name:    "unreported usage and empty fields are omitted",
			attempt: CompactionSummaryAttempt{Attempt: 1},
			want:    "attempt=1 tool_calls=0 reasoning_content=false",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.attempt.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatCompactionSummaryDetailsFlags(t *testing.T) {
	tests := []struct {
		name    string
		payload ContextDiagnosticsEvent
		want    []string
	}{
		{
			name:    "unset flags are omitted",
			payload: ContextDiagnosticsEvent{SummaryBytes: 120},
			want:    []string{"summary 120 bytes"},
		},
		{
			name:    "truncated and condensed summaries are reported",
			payload: ContextDiagnosticsEvent{SummaryBytes: 120, SummaryTruncated: true, SummaryCondensed: true},
			want:    []string{"summary 120 bytes", "summary_truncated=true", "summary_condensed=true"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatCompactionSummaryDetails(tt.payload); !slices.Equal(got, tt.want) {
				t.Errorf("formatCompactionSummaryDetails() = %q, want %q", got, tt.want)
			}
		})
	}
}

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
			name:    "failed attempts list each reply",
			payload: ContextDiagnosticsEvent{CompactionResult: CompactionResultFailed, SummaryAttempts: 1, SummaryFailureReason: "length", SummaryAttemptDetails: []CompactionSummaryAttempt{{Attempt: 1, FinishReason: "length", CompletionTokens: summaryTokens(4096), Reason: "length"}}},
			want:    []string{"compaction_result=failed", "summary_attempts=1", "summary_failure_reason=length", "summary_attempt attempt=1 finish_reason=length completion_tokens=4096 tool_calls=0 reasoning_content=false reason=length"},
		},
		{
			name:    "failed summary reports the provider error class and HTTP status",
			payload: ContextDiagnosticsEvent{CompactionResult: CompactionResultFailed, SummaryFailureReason: "no_text", ProviderErrorClass: "http_4xx", HTTPStatus: 429},
			want:    []string{"compaction_result=failed", "summary_failure_reason=no_text", "provider_error_class=http_4xx", "http_status=429"},
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
