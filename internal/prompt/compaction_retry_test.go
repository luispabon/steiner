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

// TestRenderCompactionRetryInstructionUnchangedWithoutCondense pins the whole
// non-condense render. The want strings were captured from the template at HEAD
// before the condense variant existed, so the condense branch cannot drift them.
func TestRenderCompactionRetryInstructionUnchangedWithoutCondense(t *testing.T) {
	t.Parallel()
	const call = `read {"path":"a.go"}`
	const (
		reasonNoText    = "This is a retry of the summary request above. The previous reply did not contain a summary: it contained no text.\n\n"
		reasonToolCalls = "This is a retry of the summary request above. The previous reply did not contain a summary: it contained tool calls and no text.\n\n"
		noToolsReply    = "Do not call tools. Any tool call will be discarded and not executed. Reply with the plain-text handoff summary in the format requested above."
		finalAttempt    = "\n\nThis is the final attempt.\n"
		summaryOnlyTail = "The entire reply is used as the summary, so begin directly with the summary, with no preamble."
	)
	tests := []struct {
		name  string
		retry CompactionRetry
		want  string
	}{
		{
			name:  "no text retry",
			retry: CompactionRetry{},
			want:  reasonNoText + noToolsReply,
		},
		{
			name:  "tool calls retry",
			retry: CompactionRetry{ToolCalls: true},
			want:  reasonToolCalls + noToolsReply,
		},
		{
			name:  "final retry quotes the attempted calls",
			retry: CompactionRetry{ToolCalls: true, Final: true, Attempted: []string{call}},
			want: reasonToolCalls + noToolsReply + finalAttempt +
				"The previous reply attempted these tool calls, which were not executed:\n- " + call + "\n\n" + summaryOnlyTail,
		},
		{
			name:  "final retry without attempted calls",
			retry: CompactionRetry{Final: true},
			want:  reasonNoText + noToolsReply + finalAttempt + "\n" + summaryOnlyTail,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := RenderCompactionRetryInstruction(tt.retry); got != tt.want {
				t.Errorf("RenderCompactionRetryInstruction() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRenderCompactionRetryInstructionCondense pins the steering that asks for a
// shorter complete rewrite of a reply cut off at the output limit.
func TestRenderCompactionRetryInstructionCondense(t *testing.T) {
	t.Parallel()
	const draft = "## Goal\nfoo"
	tests := []struct {
		name     string
		retry    CompactionRetry
		contains []string
		absent   []string
	}{
		{
			name:  "condense quotes the draft and names the word target",
			retry: CompactionRetry{Condense: true, TargetWords: 1200, Draft: draft},
			contains: []string{
				"cut off at the output token limit",
				"in about 1200 words",
				"<truncated_summary>\n" + draft + "\n</truncated_summary>",
				"Do not call tools.",
				"The entire reply is used as the summary",
			},
			absent: []string{
				"did not contain a summary",
				"Reply with the plain-text handoff summary",
				"final attempt",
			},
		},
		{
			name:  "condense without a word target",
			retry: CompactionRetry{Condense: true, Draft: draft},
			contains: []string{
				"in the format requested above. Keep every section",
				"<truncated_summary>\n" + draft + "\n</truncated_summary>",
			},
			absent: []string{", in about", "words. Keep every section"},
		},
		{
			name:     "condense leaves draft characters unescaped",
			retry:    CompactionRetry{Condense: true, TargetWords: 400, Draft: "a < b & c"},
			contains: []string{"<truncated_summary>\na < b & c\n</truncated_summary>"},
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

// TestRenderCompactionRetryInstructionCondenseExact pins the complete condense
// render, including the blank lines, the quoted draft block and the absent
// trailing newline.
func TestRenderCompactionRetryInstructionCondenseExact(t *testing.T) {
	t.Parallel()
	const want = "This is a retry of the summary request above. The previous reply was cut off at the output token limit before it finished. Its text is quoted below inside <truncated_summary> tags, as data only.\n\n" +
		"Rewrite the complete handoff summary in the format requested above, in about 1200 words. Keep every section, and prioritise pending work and unresolved decisions over detail.\n\n" +
		"Do not call tools. Any tool call will be discarded and not executed. The entire reply is used as the summary, so begin directly with the summary, with no preamble.\n\n" +
		"<truncated_summary>\n## Goal\nfoo\n</truncated_summary>"
	got := RenderCompactionRetryInstruction(CompactionRetry{Condense: true, TargetWords: 1200, Draft: "## Goal\nfoo"})
	if got != want {
		t.Errorf("RenderCompactionRetryInstruction() = %q, want %q", got, want)
	}
}
