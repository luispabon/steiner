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

func TestDelegationOutputSanitized(t *testing.T) {
	t.Parallel()

	dd := &delegationDisplayState{status: "complete", output: delegationMaliciousText}
	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	requireSanitizedRender(t, "child output", strings.Join(b.renderDelegationOutput(dd, 80), "\n"))
	if dd.output != delegationMaliciousText {
		t.Errorf("output = %q, want unchanged", dd.output)
	}
}

func TestDelegationTranscriptBodiesSanitized(t *testing.T) {
	t.Parallel()

	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	entries := []delegationTranscriptEntry{
		{kind: delegationTranscriptEntryAssistant, body: delegationMaliciousText},
		{kind: delegationTranscriptEntryThinking, body: delegationMaliciousText},
	}
	for _, entry := range entries {
		requireSanitizedRender(t, "transcript body", strings.Join(b.renderDelegationEntry(entry, 80), "\n"))
		if entry.body != delegationMaliciousText {
			t.Errorf("body = %q, want unchanged", entry.body)
		}
	}
}

// identityMalicious hides an OSC payload and a CSI clear inside the identity
// "visible", with a trailing bare CR.
const identityMalicious = "vis\x1b]0;pwned\x07ible\x1b[2J\r"

// requireIdentitySanitized checks identity text renders as "visible" with no
// payload and no raw controls.
func requireIdentitySanitized(t *testing.T, label, rendered string) {
	t.Helper()
	requireOnlySGR(t, label, rendered)
	plain := stripANSI(rendered)
	if strings.Contains(plain, "pwned") {
		t.Errorf("%s = %q, OSC payload leaked", label, plain)
	}
	if !strings.Contains(plain, "visible") {
		t.Errorf("%s = %q, want identity text kept", label, plain)
	}
}

func TestDelegationHeaderIdentitySanitized(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dd   delegationDisplayState
	}{
		{name: "agent id", dd: delegationDisplayState{agentID: identityMalicious, agentType: "explore"}},
		{name: "type label", dd: delegationDisplayState{agentID: "child-x", agentType: identityMalicious}},
		{name: "follow up id", dd: delegationDisplayState{agentID: "child-x", agentType: "explore", isFollowUp: true, followUpAgentID: identityMalicious}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
			dd := tt.dd
			requireIdentitySanitized(t, "header", b.renderDelegationHeaderIdentity(&dd))
			if dd.agentID != tt.dd.agentID || dd.agentType != tt.dd.agentType || dd.followUpAgentID != tt.dd.followUpAgentID {
				t.Errorf("identity fields changed: %+v", dd)
			}
		})
	}
}

func TestDelegationGroupNameSanitized(t *testing.T) {
	t.Parallel()

	m := newContentBenchModel()
	for j := range 2 {
		m = appendGroupChild(m, identityMalicious, j, false)
	}
	var group *delegationGroupSegment
	for i := range m.content.segments {
		if g := m.content.segments[i].delegGroupData; g != nil {
			group = g
		}
	}
	if group == nil {
		t.Fatal("no group segment built")
	}
	// Ingestion trims the trailing CR, so the stored group is compared with
	// its value before rendering rather than with identityMalicious.
	stored := make([]string, 0, len(group.entries))
	for _, dd := range group.entries {
		if dd.admission == nil || !strings.Contains(dd.admission.Group, "\x1b]0;pwned") {
			t.Fatalf("stored group for %q lacks the malicious payload", dd.agentID)
		}
		stored = append(stored, dd.admission.Group)
	}
	b := &m.content
	requireIdentitySanitized(t, "group footer", b.renderDelegationGroupSegment(contentSegment{delegGroupData: group}, 80))
	for i, dd := range group.entries {
		if dd.admission.Group != stored[i] {
			t.Errorf("stored group changed for %q: %q", dd.agentID, dd.admission.Group)
		}
	}
}

func TestDelegateCancelIdentitySanitized(t *testing.T) {
	t.Parallel()

	row := delegateActiveRow{agentID: identityMalicious, agentType: identityMalicious, taskPreview: "task"}
	m := &Model{styles: testStyles(theme.AccentAmber)}
	requireIdentitySanitized(t, "cancel row", m.renderDelegateCancelRow(row, 120))
	m.delegateCancelModal.rows = []delegateActiveRow{row}
	m.delegateCancelModal.target = 0
	requireIdentitySanitized(t, "cancel target", m.delegateCancelTargetBody(120))
	if row.agentID != identityMalicious || row.agentType != identityMalicious {
		t.Errorf("row identity changed: %+v", row)
	}
}

func TestSubAgentPickerIdentitySanitized(t *testing.T) {
	t.Parallel()

	dd := &delegationDisplayState{agentID: identityMalicious, agentType: identityMalicious, status: "active"}
	requireIdentitySanitized(t, "picker card", cardRowParts(subAgentPickerRow{dd: dd}, 120, 0).join())
	if dd.agentID != identityMalicious || dd.agentType != identityMalicious {
		t.Errorf("identity fields changed: agentID %q, agentType %q", dd.agentID, dd.agentType)
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
