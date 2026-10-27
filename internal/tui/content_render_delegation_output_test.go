package tui

import "testing"

func TestDelegationOutputDuplicatesTranscript(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output string
		body   string
		want   bool
	}{
		{name: "output differs only by escape and spacing", output: "a \x1b[0m b", body: "a b", want: true},
		{name: "output has an extra word", output: "a b c", body: "a b", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := newTestBuffer(t)
			dd := &delegationDisplayState{
				output:  tt.output,
				entries: []delegationTranscriptEntry{{kind: delegationTranscriptEntryAssistant, body: tt.body}},
			}
			if got := b.delegationOutputDuplicatesTranscript(dd, tt.output); got != tt.want {
				t.Errorf("delegationOutputDuplicatesTranscript(%q) = %v, want %v", tt.output, got, tt.want)
			}
			// renderDelegationOutput returns no section (nil) exactly when the output duplicates the transcript.
			if suppressed := b.renderDelegationOutput(dd, 80) == nil; suppressed != tt.want {
				t.Errorf("output section suppressed = %v, want %v", suppressed, tt.want)
			}
		})
	}
}
