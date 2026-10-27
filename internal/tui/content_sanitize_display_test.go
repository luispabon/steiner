package tui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// sgrSequence matches the SGR codes lipgloss writes. Rendered output has these
// removed before the raw-control check, so a raw ESC that is not an SGR still
// fails.
var sgrSequence = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

// requireNoRawControls fails when s contains a C0, DEL, or C1 control other
// than tab and newline.
func requireNoRawControls(t *testing.T, label, s string) {
	t.Helper()
	for _, r := range s {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Errorf("%s = %q, contains control %U", label, s, r)
			return
		}
	}
}

// requireOnlySGR fails when rendered output contains a raw control other than
// the SGR codes lipgloss writes.
func requireOnlySGR(t *testing.T, label, s string) {
	t.Helper()
	requireNoRawControls(t, label, sgrSequence.ReplaceAllString(s, ""))
}

func TestApprovalPreviewSanitizedAtRender(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		wantSub string
	}{
		{name: "JSON value with OSC and CR", raw: "{\"query\":\"\x1b]0;pwned\x07visible\r\"}", wantSub: "visible"},
		{name: "plain text with CSI", raw: "\x1b[2Jvisible text", wantSub: "visible text"},
		{name: "incomplete CSI at end", raw: "visible\x1b[31", wantSub: "visible"},
		{name: "unterminated OSC hides rest", raw: "visible\x1b]0;pwned", wantSub: "visible"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
			got := b.renderApprovalPreview(tt.raw, 80)
			requireOnlySGR(t, "approval preview", got)
			plain := stripANSI(got)
			if !strings.Contains(plain, tt.wantSub) || strings.Contains(plain, "pwned") {
				t.Errorf("approval preview = %q, want %q and no OSC payload", plain, tt.wantSub)
			}
		})
	}
}

// TestApprovalBlockSanitizesChildScopedMCPPreview covers an MCP approval raised
// by a delegated child agent. The block shows the child label, and the stored
// preview keeps the original bytes.
func TestApprovalBlockSanitizesChildScopedMCPPreview(t *testing.T) {
	t.Parallel()

	const raw = "{\"query\":\"\x1b]0;pwned\x07visible\r\"}"
	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	tc := &toolCallSegment{
		tool:            "mcp__srv__search",
		callID:          "call-1",
		approvalPending: true,
		approvalMode:    "ask",
		approvalPreview: raw,
		approvalKind:    "mcp",
		approvalServer:  "srv",
		approvalMCPTool: "search",
		approvalAgentID: "child-x",
	}

	got := b.renderToolApprovalBlock(tc, 80)
	requireOnlySGR(t, "approval block", got)
	plain := stripANSI(got)
	if !strings.Contains(plain, "visible") || !strings.Contains(plain, "child-x") || strings.Contains(plain, "pwned") {
		t.Errorf("approval block = %q, want visible text, child label, and no OSC payload", plain)
	}
	if tc.approvalPreview != raw {
		t.Errorf("approvalPreview = %q, want unchanged %q", tc.approvalPreview, raw)
	}
}

// TestDisplayFileImageAndBinaryCaptionSanitized covers image and binary
// display_file previews. Their path and lines are not built by the normalizing
// formatters. The stored payload keeps the original bytes.
func TestDisplayFileImageAndBinaryCaptionSanitized(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  string
	}{
		{name: "image", msg: "image preview unavailable in text overlay"},
		{name: "binary", msg: "binary file omitted from text preview"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			const raw = "img\x1b]0;pwned\x07\x1b[2J.png"
			line := tt.msg + "\x1b[31m\r"
			payload := output.DisplayFilePayload{
				Path: raw,
				Preview: output.PreviewDocument{
					Kind:     output.PreviewFormatKindFile,
					Language: "plain",
					Lines:    []output.PreviewLine{{Kind: output.PreviewLineKindText, Spans: []output.PreviewSpan{{Text: line}}}},
				},
			}

			b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
			b.AppendEvent(output.NewDisplayFileEvent(payload))
			got := b.String(100)
			requireOnlySGR(t, "display file", got)
			plain := stripANSI(got)
			for _, want := range []string{"img.png", tt.msg} {
				if !strings.Contains(plain, want) {
					t.Errorf("display file = %q, want %q", plain, want)
				}
			}
			if strings.Contains(plain, "pwned") {
				t.Errorf("display file = %q, OSC payload leaked", plain)
			}
			if payload.Path != raw || payload.Preview.Lines[0].Spans[0].Text != line {
				t.Errorf("stored display payload changed: path %q, line %q", payload.Path, payload.Preview.Lines[0].Spans[0].Text)
			}
		})
	}
}

// TestDelegationChildArgsAndOperationSanitized covers a delegated child's tool
// call. Its args and the card's current operation are display text, so raw
// controls are removed there. The scoped event keeps the original arguments.
func TestDelegationChildArgsAndOperationSanitized(t *testing.T) {
	t.Parallel()

	const raw = "ls \x1b]0;pwned\x07 -la\r"
	m := newIdentityTestModel()
	m.content.styles = testStyles(theme.AccentAmber)
	m.applyEvent(output.NewToolCallStartedEvent(1, "sub_agent", "replay-call", subAgentArgs("review")))
	occ := output.DelegationOccurrence{CallID: "replay-call", BatchID: "batch", AgentID: "child-x"}
	m.applyEvent(output.NewDelegationAcceptedEvent(occ, "review"))
	m.applyEvent(output.NewDelegationStartedEvent(occ, "find files", "", "explore"))
	childEvent := output.WithAgentScope(output.NewToolCallStartedEvent(2, "bash", "child-call", map[string]any{"command": raw}), "child-x")
	m.applyEvent(childEvent)

	loc, ok := m.content.activeDelegations["child-x"]
	if !ok || loc.dd == nil || len(loc.dd.entries) == 0 {
		t.Fatalf("child card = %#v, want active card with transcript entries", loc)
	}
	if got := loc.dd.currentOperation; got != "bash: ls -la" {
		t.Errorf("currentOperation = %q, want %q", got, "bash: ls -la")
	}
	if got := loc.dd.entries[len(loc.dd.entries)-1].args; got != "ls  -la" {
		t.Errorf("child args = %q, want %q", got, "ls  -la")
	}

	rendered := m.content.String(120)
	requireOnlySGR(t, "delegation card", rendered)
	if strings.Contains(stripANSI(rendered), "pwned") {
		t.Error("delegation card leaked OSC payload")
	}
	if got := childEvent.Payload.(output.ToolCallStartedEvent).Arguments["command"]; got != raw {
		t.Errorf("scoped event command = %q, want unchanged %q", got, raw)
	}
}

func TestDelegationHeaderOperationSanitizesChildText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dd   *delegationDisplayState
		want string
	}{
		{name: "advisor question with OSC and CR", dd: &delegationDisplayState{isAdvisor: true, advisorQuestion: "should \x1b]0;pwned\x07we\r\nnow?"}, want: "should we now?"},
		{name: "task preview with CSI", dd: &delegationDisplayState{status: "active", taskPreview: "find \x1b[2Jfiles\r"}, want: "find files"},
		{name: "advisor question unterminated OSC hides rest", dd: &delegationDisplayState{isAdvisor: true, advisorQuestion: "ask \x1b]0;pwned"}, want: "ask"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
			got := b.renderDelegationHeaderOperation(tt.dd, 120)
			requireOnlySGR(t, "header operation", got)
			plain := stripANSI(got)
			if !strings.Contains(plain, tt.want) || strings.Contains(plain, "pwned") {
				t.Errorf("header operation = %q, want %q and no OSC payload", plain, tt.want)
			}
		})
	}
}

// TestDelegationHeaderOperationAdvisorFilesSanitizesBeforeTruncation puts an escape at the
// truncation point. Sanitizing first gives "check the plan", cut to "check the …" before the
// suffix. Truncating the raw text first left "check  the…" with a double space.
func TestDelegationHeaderOperationAdvisorFilesSanitizesBeforeTruncation(t *testing.T) {
	t.Parallel()

	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	dd := &delegationDisplayState{
		isAdvisor:       true,
		advisorQuestion: "check \x1b[0m the plan",
		advisorFiles:    []string{"a.go"},
	}
	got := b.renderDelegationHeaderOperation(dd, 20)
	requireOnlySGR(t, "header operation", got)
	if plain, want := stripANSI(got), "check the … · 1 file"; plain != want {
		t.Errorf("header operation = %q, want %q", plain, want)
	}
}

// TestDelegationHeaderOperationSanitizedEmptyFallsThrough checks that text which sanitizes to
// empty acts like an empty field: the next fallback applies and no leading separator shows.
func TestDelegationHeaderOperationSanitizedEmptyFallsThrough(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dd   *delegationDisplayState
		want string
	}{
		{name: "advisor question with files falls back to steering", dd: &delegationDisplayState{isAdvisor: true, advisorQuestion: "\x1b[0m\x07", advisorFiles: []string{"a.go"}}, want: "stronger-model steering · 1 file"},
		{name: "advisor question falls back to current operation", dd: &delegationDisplayState{isAdvisor: true, advisorQuestion: "\x1b[0m\x07", currentOperation: "reading files"}, want: "reading files"},
		{name: "current operation falls back to task preview", dd: &delegationDisplayState{status: "active", currentOperation: "\x1b[0m", taskPreview: "find files"}, want: "find files"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
			got := b.renderDelegationHeaderOperation(tt.dd, 40)
			requireOnlySGR(t, "header operation", got)
			if plain := stripANSI(got); plain != tt.want {
				t.Errorf("header operation = %q, want %q", plain, tt.want)
			}
		})
	}
}
