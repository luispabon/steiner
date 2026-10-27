package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// delegationMaliciousText wraps the visible words "visible text" in an OSC
// title, a CSI clear, a BEL, and a bare CR.
const delegationMaliciousText = "visible\x1b]0;pwned\x07 \x1b[2Jtext\r"

// overlayTaskPreviews adds malformed and incomplete escapes to the malicious
// text. Each one must render as "visible text".
var overlayTaskPreviews = []struct {
	name string
	raw  string
}{
	{name: "malicious", raw: delegationMaliciousText},
	{name: "unterminated OSC", raw: "visible text\x1b]0;pwned"},
	{name: "incomplete CSI", raw: "visible text\x1b[31"},
}

// requireSanitizedRender fails when rendered output keeps a raw control, leaks
// the OSC payload, or drops the visible words.
func requireSanitizedRender(t *testing.T, label, rendered string) {
	t.Helper()
	requireOnlySGR(t, label, rendered)
	plain := stripANSI(rendered)
	if strings.Contains(plain, "pwned") {
		t.Errorf("%s = %q, OSC payload leaked", label, plain)
	}
	if !strings.Contains(plain, "visible text") {
		t.Errorf("%s = %q, want visible text kept", label, plain)
	}
}

func TestExpandedDelegationPromptSanitized(t *testing.T) {
	t.Parallel()

	dd := &delegationDisplayState{promptText: delegationMaliciousText}
	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	requireSanitizedRender(t, "prompt body", strings.Join(b.renderDelegationPromptBody(dd, 80), "\n"))
	if dd.promptText != delegationMaliciousText {
		t.Errorf("promptText = %q, want unchanged", dd.promptText)
	}
}

func TestExpandedAdvisorQuestionSanitized(t *testing.T) {
	t.Parallel()

	dd := &delegationDisplayState{isAdvisor: true, advisorQuestion: delegationMaliciousText}
	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	requireSanitizedRender(t, "advisor question", strings.Join(b.renderAdvisorQuestionBody(dd, 80), "\n"))
	if dd.advisorQuestion != delegationMaliciousText {
		t.Errorf("advisorQuestion = %q, want unchanged", dd.advisorQuestion)
	}
}

func TestExpandedDelegationBriefSanitized(t *testing.T) {
	t.Parallel()

	dd := &delegationDisplayState{
		briefObjective:   "objective",
		briefContext:     delegationMaliciousText,
		briefDeliverable: delegationMaliciousText,
		briefConstraints: []string{delegationMaliciousText},
		briefChecks:      []string{delegationMaliciousText},
	}
	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	requireSanitizedRender(t, "brief body", strings.Join(b.renderDelegationBriefBody(dd, 80), "\n"))
	if dd.briefContext != delegationMaliciousText || dd.briefConstraints[0] != delegationMaliciousText {
		t.Errorf("brief fields changed: context %q, constraints %q", dd.briefContext, dd.briefConstraints)
	}
}

func TestExpandedDelegationCardSanitized(t *testing.T) {
	t.Parallel()

	dd := &delegationDisplayState{agentID: "child-x", agentType: "explore", status: "active", promptText: delegationMaliciousText}
	dd.collapsed = false
	dd.promptCollapsed = false
	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	requireSanitizedRender(t, "expanded card", b.renderDelegationSegment(contentSegment{delegData: dd}, 100))
	if dd.promptText != delegationMaliciousText {
		t.Errorf("promptText = %q, want unchanged", dd.promptText)
	}
}

// TestExpandedDelegationCardReplaySanitized replays a sub_agent call with a
// malicious objective through the event path, then renders the expanded card.
// The stored tool-call arguments must keep the original bytes.
func TestExpandedDelegationCardReplaySanitized(t *testing.T) {
	t.Parallel()

	m := newIdentityTestModel()
	m.content.styles = testStyles(theme.AccentAmber)
	start := output.NewToolCallStartedEvent(1, "sub_agent", "replay-call", map[string]any{"type": "explore", "objective": delegationMaliciousText})
	m.applyEvent(start)
	occ := output.DelegationOccurrence{CallID: "replay-call", BatchID: "batch", AgentID: "child-x"}
	m.applyEvent(output.NewDelegationAcceptedEvent(occ, "review"))
	m.applyEvent(output.NewDelegationStartedEvent(occ, delegationMaliciousText, "", "explore"))

	loc, ok := m.content.activeDelegations["child-x"]
	if !ok || loc.dd == nil {
		t.Fatalf("child card missing after replay: %#v", loc)
	}
	loc.dd.collapsed = false
	loc.dd.promptCollapsed = false
	requireSanitizedRender(t, "replayed expanded card", m.content.String(120))
	if got := start.Payload.(output.ToolCallStartedEvent).Arguments["objective"]; got != delegationMaliciousText {
		t.Errorf("stored objective = %q, want unchanged", got)
	}
}

func TestDelegateCancelRowTaskPreviewSanitized(t *testing.T) {
	t.Parallel()

	for _, tt := range overlayTaskPreviews {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := &Model{styles: testStyles(theme.AccentAmber)}
			row := delegateActiveRow{agentID: "child-x", agentType: "explore", taskPreview: tt.raw}
			requireSanitizedRender(t, "cancel row", m.renderDelegateCancelRow(row, 120))
		})
	}
}

func TestSubAgentPickerTaskPreviewSanitized(t *testing.T) {
	t.Parallel()

	for _, tt := range overlayTaskPreviews {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dd := &delegationDisplayState{agentID: "child-x", agentType: "explore", status: "active", taskPreview: tt.raw}
			requireSanitizedRender(t, "picker card", cardRowParts(subAgentPickerRow{dd: dd}, 120, 0).join())
			if dd.taskPreview != tt.raw {
				t.Errorf("taskPreview = %q, want unchanged", dd.taskPreview)
			}
		})
	}
}
