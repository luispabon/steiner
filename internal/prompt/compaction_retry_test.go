package prompt

import (
	"strings"
	"testing"
)

// TestRenderCompactionRetryInstructionWording pins the steering each retry
// variant carries: the reason for the failed reply, the no-tools warning, the
// final-attempt anchor and the quoted tool calls.
func TestRenderCompactionRetryInstructionWording(t *testing.T) {
	t.Parallel()
	const call = `read {"path":"a.go"}`
	tests := []struct {
		name     string
		retry    CompactionRetry
		contains []string
		absent   []string
	}{
		{
			name:     "no text retry",
			retry:    CompactionRetry{},
			contains: []string{"contained no text.", "Do not call tools.", "in the format requested above."},
			absent:   []string{"tool calls", "final attempt", "previous reply attempted"},
		},
		{
			name:     "tool calls retry",
			retry:    CompactionRetry{ToolCalls: true},
			contains: []string{"contained tool calls and no text.", "Any tool call will be discarded and not executed."},
			absent:   []string{"contained no text", "final attempt"},
		},
		{
			name:     "final retry quotes the attempted calls",
			retry:    CompactionRetry{ToolCalls: true, Final: true, Attempted: []string{call}},
			contains: []string{"This is the final attempt.", "which were not executed:\n- " + call, "begin directly with the summary, with no preamble."},
		},
		{
			name:     "final retry without attempted calls",
			retry:    CompactionRetry{Final: true},
			contains: []string{"This is the final attempt.\n\nThe entire reply is used as the summary"},
			absent:   []string{"previous reply attempted"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := RenderCompactionRetryInstruction(tt.retry)
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("RenderCompactionRetryInstruction() = %q, want it to contain %q", got, want)
				}
			}
			for _, bad := range tt.absent {
				if strings.Contains(got, bad) {
					t.Errorf("RenderCompactionRetryInstruction() = %q, want it not to contain %q", got, bad)
				}
			}
		})
	}
}
