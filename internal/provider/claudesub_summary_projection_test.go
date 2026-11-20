package provider

import (
	"errors"
	"testing"
)

// TestClaudeSubPlanSummaryMatchesBlockProjection proves an in-place summary plan
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
			_, err := s.planSummary([]Message{tt.source, answer, instruction}, 2)
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
