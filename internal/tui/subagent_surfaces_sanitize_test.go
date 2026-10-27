package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// TestSidebarSubAgentIdentitySanitized covers model-supplied agent ID, type,
// and group text in the sidebar roster. The displayed copies are sanitized,
// while the stored entries and the row click targets keep the original bytes.
func TestSidebarSubAgentIdentitySanitized(t *testing.T) {
	t.Parallel()

	entries := []rosterEntry{
		{
			agentID:    identityMalicious,
			agentType:  identityMalicious,
			admitted:   true,
			occurrence: occurrenceKey{BatchID: "batch", CallID: "call-a"},
			group:      identityMalicious,
			status:     rosterRunning,
			startTime:  1,
		},
		{
			agentID:    "child-b",
			agentType:  "explore",
			admitted:   true,
			occurrence: occurrenceKey{BatchID: "batch", CallID: "call-b"},
			group:      identityMalicious,
			status:     rosterRunning,
			startTime:  1,
		},
	}

	lines, targets := rosterSidebar(entries, 2).subAgentsRows(80)
	plain := stripANSI(strings.Join(lines, "\n"))
	requireNoRawControls(t, "sidebar roster", plain)
	if strings.Contains(plain, "pwned") || !strings.Contains(plain, "┌ visible") || !strings.Contains(plain, "visible") {
		t.Errorf("sidebar roster = %q, want sanitized group heading and identity text and no OSC payload", plain)
	}
	if !slices.Contains(targets, identityMalicious) {
		t.Errorf("targets = %q, want raw agent ID kept as click target", targets)
	}
	if entries[0].agentID != identityMalicious || entries[0].agentType != identityMalicious || entries[0].group != identityMalicious {
		t.Errorf("stored roster entry changed: %+v", entries[0])
	}
}

// TestSubAgentPickerGroupHeadingSanitized covers the group header row in the
// sub-agent picker. The stored heading keeps the original bytes.
func TestSubAgentPickerGroupHeadingSanitized(t *testing.T) {
	t.Parallel()

	s := newSubAgentPickerOverlay(testStyles(theme.AccentAmber))
	s.rows = []subAgentPickerRow{{groupHdr: true, heading: identityMalicious}}
	requireIdentitySanitized(t, "picker group heading", s.renderRow(0, 80, 0))
	if s.rows[0].heading != identityMalicious {
		t.Errorf("stored heading = %q, want unchanged %q", s.rows[0].heading, identityMalicious)
	}
}

// TestApprovalPillLabelSanitized covers model-supplied tool, mode, and agent
// ID text in the approval pill label, both pending and resolved. The stored
// approval data keeps the original bytes.
func TestApprovalPillLabelSanitized(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		resolved bool
	}{
		{name: "pending", resolved: false},
		{name: "resolved", resolved: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
			ad := &approvalPillData{
				tool:     identityMalicious,
				mode:     identityMalicious,
				agentID:  identityMalicious,
				resolved: tt.resolved,
				accepted: true,
			}
			requireIdentitySanitized(t, "approval pill", b.renderApprovalPill(ad, 80))
			if ad.tool != identityMalicious || ad.mode != identityMalicious || ad.agentID != identityMalicious {
				t.Errorf("stored approval data changed: %+v", ad)
			}
		})
	}
}
