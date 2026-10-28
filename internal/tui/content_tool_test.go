package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestSpecializedDelegateToolAccessor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tool string
		want bool
	}{
		{name: "explore", tool: "explore", want: false},
		{name: "research", tool: "research", want: false},
		{name: "code", tool: "code", want: false},
		{name: "evaluate", tool: "evaluate", want: false},
		{name: "sanity_check", tool: "sanity_check", want: false},
		{name: "review", tool: "review", want: false},
		{name: "sub_agent", tool: "sub_agent", want: true},
		{name: "follow_up", tool: "follow_up", want: true},
		{name: "uppercase tool", tool: "Sub_Agent", want: true},
		{name: "mixed-case and whitespace normalization", tool: " Sub_Agent ", want: true},
		{name: "trimmed_tool", tool: "  sub_agent  ", want: true},
		{name: "delegate", tool: "delegate", want: false},
		{name: "read", tool: "read", want: false},
		{name: "empty", tool: "", want: false},
		{name: "unknown", tool: "debug", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isSpecializedDelegateTool(tt.tool); got != tt.want {
				t.Fatalf("isSpecializedDelegateTool(%q) = %v, want %v", tt.tool, got, tt.want)
			}
		})
	}

	wantTools := []string{"sub_agent", "follow_up"}
	if got := delegation.AllSpecializedDelegateTools(); !slices.Equal(got, wantTools) {
		t.Fatalf("delegation.AllSpecializedDelegateTools() = %v, want %v", got, wantTools)
	}
}

func TestRenderToolCallBoxKeepsRequestedWidth(t *testing.T) {
	t.Parallel()
	useTrueColor(t)
	buffer := &contentBuffer{
		styles: testStyles(theme.AccentAmber),
	}
	segment := &toolCallSegment{
		tool: "bash",
		args: "printf hello",
		meta: "✓",
	}

	rendered := strings.TrimSuffix(buffer.renderToolCall(segment, 50), "\n")
	for _, line := range strings.Split(rendered, "\n") {
		if lipgloss.Width(line) != 50 {
			t.Fatalf("line width = %d, want 50 for line %q", lipgloss.Width(line), stripANSI(line))
		}
	}
}

// TestRenderToolCallFrameCollapsesMultilineArgs proves the collapsed tool box
// header stays a single line even when the argument summary contains real line
// breaks (e.g. a multiline bash command), collapsing them to the ⏎ separator
// while preserving literal backslash-n sequences and the existing rune-count
// truncation with "…".
func TestRenderToolCallFrameCollapsesMultilineArgs(t *testing.T) {
	t.Parallel()
	useTrueColor(t)

	tests := []struct {
		name   string
		args   string
		width  int
		assert func(t *testing.T, plain string)
	}{
		{
			name:  "multiline command collapses to one line",
			args:  "set -euo pipefail\nprintf 'CONTEXT\\n'; kubectl config current-context\nkubectl get ns",
			width: 200,
			assert: func(t *testing.T, plain string) {
				t.Helper()
				if strings.Contains(plain, "\n") {
					t.Fatalf("header contains a newline, want single line: %q", plain)
				}
				if !strings.Contains(plain, "⏎") {
					t.Fatalf("header = %q, want collapsed line-break separator ⏎", plain)
				}
				if !strings.Contains(plain, "printf 'CONTEXT\\n'") {
					t.Fatalf("header = %q, want literal backslash-n preserved", plain)
				}
			},
		},
		{
			name:  "crlf collapses too",
			args:  "line one\r\nline two\r",
			width: 200,
			assert: func(t *testing.T, plain string) {
				t.Helper()
				if strings.Contains(plain, "\n") || strings.Contains(plain, "\r") {
					t.Fatalf("header contains a line break, want single line: %q", plain)
				}
				if strings.Count(plain, "⏎") != 2 {
					t.Fatalf("header = %q, want 2 ⏎ separators", plain)
				}
			},
		},
		{
			name:  "long single-line command still truncates with ellipsis",
			args:  strings.Repeat("x", 500),
			width: 40,
			assert: func(t *testing.T, plain string) {
				t.Helper()
				if !strings.Contains(plain, "…") {
					t.Fatalf("header = %q, want trailing ellipsis after truncation", plain)
				}
				if strings.Contains(plain, "\n") {
					t.Fatalf("header contains a newline, want single line: %q", plain)
				}
			},
		},
		{
			name:  "short single-line command unchanged",
			args:  "printf hello",
			width: 200,
			assert: func(t *testing.T, plain string) {
				t.Helper()
				if !strings.Contains(plain, "printf hello") {
					t.Fatalf("header = %q, want short command preserved", plain)
				}
				if strings.Contains(plain, "⏎") || strings.Contains(plain, "\n") {
					t.Fatalf("header = %q, want no collapse markers on single-line input", plain)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buffer := &contentBuffer{styles: testStyles(theme.AccentAmber)}
			segment := &toolCallSegment{
				tool:      "bash",
				args:      tt.args,
				meta:      "✓",
				collapsed: true,
			}
			rendered := buffer.renderToolCallFrame(segment, tt.width)
			tt.assert(t, stripANSI(rendered))
		})
	}
}
func TestRenderToolCallGroupMixedUsesDefaultBorder(t *testing.T) {
	t.Parallel()

	styles := testStyles(theme.AccentAmber)
	buffer := &contentBuffer{
		styles: styles,
		mcpToolOrigins: map[string]MCPToolOrigin{
			"mcp__server__read": {Server: "server", Tool: "read"},
		},
	}
	group := &toolCallGroupSegment{
		tool:  "mcp__server__read",
		mixed: true,
		entries: []*toolCallSegment{
			{tool: "mcp__server__read", args: "first", collapsed: true},
			{tool: "bash", args: "second", collapsed: true},
		},
	}
	const width = 60
	innerWidth := width - 4
	parts := []string{
		buffer.renderToolCallFrame(group.entries[0], innerWidth),
		buffer.renderToolCallDivider(width - 4),
		buffer.renderToolCallFrame(group.entries[1], innerWidth),
	}
	want := buffer.renderToolCallBox(strings.Join(parts, "\n"), "", width)

	if got := buffer.renderToolCallGroup(group, width); got != want {
		t.Fatalf("mixed group rendering did not use default border\ngot:  %q\nwant: %q", got, want)
	}
}

func TestApplyFinishedToolCallResultMutateStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		preview   output.ToolPreview
		err       error
		wantMeta  string
		wantError bool
	}{
		{
			name: "mutate all operations succeeded shows check",
			preview: output.ToolPreview{
				Kind:             output.ToolPreviewKindMutate,
				HunksApplied:     2,
				HunksFailed:      0,
				MutateOperations: []output.ToolPreviewMutateOperation{{Type: "replace", Path: "file.go"}},
			},
			wantMeta:  "✓",
			wantError: false,
		},
		{
			name: "mutate partial failures shows error",
			preview: output.ToolPreview{
				Kind:             output.ToolPreviewKindMutate,
				HunksApplied:     1,
				HunksFailed:      1,
				MutateOperations: []output.ToolPreviewMutateOperation{{Type: "replace", Path: "file.go"}},
			},
			wantMeta:  "✗",
			wantError: true,
		},
		{
			name: "mutate transport-level error still shows error",
			preview: output.ToolPreview{
				Kind:             output.ToolPreviewKindMutate,
				HunksApplied:     1,
				HunksFailed:      0,
				MutateOperations: []output.ToolPreviewMutateOperation{{Type: "replace", Path: "file.go"}},
			},
			err:       errors.New("boom"),
			wantMeta:  "✗",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			buffer := &contentBuffer{
				collapseState: make(map[int]bool),
			}

			buffer.AppendEvent(output.NewToolCallStartedEvent(1, "mutate", "call_1", map[string]any{
				"operations": []any{
					map[string]any{"type": "replace", "path": "file.go"},
				},
			}))
			buffer.AppendEvent(output.NewToolCallFinishedEventWithAdmission(1, "mutate", "call_1", `{"operations_applied":1,"operations_failed":0}`, tt.err, tt.preview, nil))

			if got := len(buffer.segments); got != 1 {
				t.Fatalf("segments = %d, want 1", got)
			}

			td := buffer.segments[0].toolData
			if td == nil {
				t.Fatal("toolData = nil, want mutate tool call")
			}
			if got := td.meta; got != tt.wantMeta {
				t.Fatalf("meta = %q, want %q", got, tt.wantMeta)
			}
			if got := td.hasError; got != tt.wantError {
				t.Fatalf("hasError = %v, want %v", got, tt.wantError)
			}
		})
	}
}

func TestRenderApprovalPreviewCJK(t *testing.T) {
	t.Parallel()
	buffer := &contentBuffer{
		collapseState: make(map[int]bool),
		styles:        testStyles("#5599ff"),
	}

	// Test that CJK keys are properly capitalized without byte-slicing corruption
	result := buffer.renderApprovalPreview(`{"界":"value"}`, 20)
	if !strings.Contains(stripANSI(result), "界") {
		t.Errorf("renderApprovalPreview with CJK key corrupted: got %q", stripANSI(result))
	}

	// Test that value width calculation uses display width, not byte length
	// With width=12, "界:" is 2+1=3 cells, leaving 12-3-2=7 cells for value
	// Five CJK chars (10 cells) should truncate to fit in 7 cells
	result = buffer.renderApprovalPreview(`{"界":"界界界界界"}`, 12)
	rendered := stripANSI(result)
	// The value should be truncated, not showing all 5 chars
	if strings.Count(rendered, "界") < 3 {
		t.Errorf("renderApprovalPreview with CJK key and value should truncate value: got %q", rendered)
	}
}
