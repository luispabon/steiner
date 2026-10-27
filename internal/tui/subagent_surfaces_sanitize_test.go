package tui

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// osc8Link matches an OSC 8 hyperlink open or close sequence and captures its URI.
var osc8Link = regexp.MustCompile(`\x1b\]8;[^;\x1b\x07]*;([^\x1b\x07]*)(?:\x1b\\|\x07)`)

// requireOSC8Links checks that the non-empty OSC 8 URIs in raw are the roster
// links for ids, in order. It returns raw without the OSC 8 sequences and fails
// on any other OSC or raw control left in the remainder.
func requireOSC8Links(t *testing.T, label, raw string, ids []string) string {
	t.Helper()
	var uris []string
	for _, m := range osc8Link.FindAllStringSubmatch(raw, -1) {
		if m[1] != "" {
			uris = append(uris, m[1])
		}
	}
	want := make([]string, 0, len(ids))
	for _, id := range ids {
		want = append(want, "steiner://agent/"+url.PathEscape(id))
	}
	if !slices.Equal(uris, want) {
		t.Errorf("%s hyperlinks = %q, want %q", label, uris, want)
	}
	rest := osc8Link.ReplaceAllString(raw, "")
	requireOnlySGR(t, label, rest)
	return rest
}

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
	rest := requireOSC8Links(t, "sidebar roster", strings.Join(lines, "\n"), []string{identityMalicious, "child-b"})
	plain := stripANSI(rest)
	requireNoRawControls(t, "sidebar roster", plain)
	if strings.Contains(plain, "pwned") || !strings.Contains(plain, "┌ visible") {
		t.Errorf("sidebar roster = %q, want sanitized group heading and no OSC payload", plain)
	}
	if want := []string{"", "", "", identityMalicious, "child-b"}; !slices.Equal(targets, want) {
		t.Errorf("targets = %q, want %q", targets, want)
	}
	if entries[0].agentID != identityMalicious || entries[0].agentType != identityMalicious || entries[0].group != identityMalicious {
		t.Errorf("stored roster entry changed: %+v", entries[0])
	}
}

// TestSidebarSingleLineIdentityNormalized covers newline and tab in an agent
// ID and type. The row must stay on one line and match the clean rendering,
// and its click target must keep the raw agent ID.
func TestSidebarSingleLineIdentityNormalized(t *testing.T) {
	t.Parallel()

	const hostileID = "a\nb\tc"
	hostile := []rosterEntry{{agentID: hostileID, agentType: "ex\tp", admitted: true, status: rosterRunning, startTime: 1}}
	clean := []rosterEntry{{agentID: "a b c", agentType: "ex p", admitted: true, status: rosterRunning, startTime: 1}}

	hostileLines, targets := rosterSidebar(hostile, 2).subAgentsRows(80)
	cleanLines, _ := rosterSidebar(clean, 2).subAgentsRows(80)
	for _, line := range hostileLines {
		if strings.ContainsAny(line, "\n\t") {
			t.Errorf("sidebar line %q contains a line break or tab", line)
		}
	}
	rest := requireOSC8Links(t, "sidebar row", strings.Join(hostileLines, "\n"), []string{hostileID})
	if got, want := stripANSI(rest), stripANSI(strings.Join(cleanLines, "\n")); got != want {
		t.Errorf("sidebar rows = %q, want %q", got, want)
	}
	if want := []string{"", "", hostileID}; !slices.Equal(targets, want) {
		t.Errorf("targets = %q, want %q", targets, want)
	}
}

// TestSubAgentPickerGroupHeadingSanitized covers the picker group header with
// OSC and CSI controls. The stored heading keeps the original bytes.
func TestSubAgentPickerGroupHeadingSanitized(t *testing.T) {
	t.Parallel()

	s := newSubAgentPickerOverlay(testStyles(theme.AccentAmber))
	s.rows = []subAgentPickerRow{{groupHdr: true, heading: identityMalicious}}
	requireIdentitySanitized(t, "picker group heading", s.renderRow(0, 80, 0))
	if s.rows[0].heading != identityMalicious {
		t.Errorf("stored heading = %q, want unchanged %q", s.rows[0].heading, identityMalicious)
	}
}

// TestSubAgentPickerGroupHeadingSingleLine covers newline and tab in a picker
// group heading. The header must stay on one line and match the clean rendering.
func TestSubAgentPickerGroupHeadingSingleLine(t *testing.T) {
	t.Parallel()

	hostile := newSubAgentPickerOverlay(testStyles(theme.AccentAmber))
	hostile.rows = []subAgentPickerRow{{groupHdr: true, heading: "vis\nible\tgroup"}}
	clean := hostile
	clean.rows = []subAgentPickerRow{{groupHdr: true, heading: "vis ible group"}}

	got := hostile.renderRow(0, 80, 0)
	if want := clean.renderRow(0, 80, 0); strings.ContainsAny(got, "\n\t") || got != want {
		t.Errorf("picker group heading = %q, want %q", got, want)
	}
	if hostile.rows[0].heading != "vis\nible\tgroup" {
		t.Errorf("stored heading = %q, want unchanged", hostile.rows[0].heading)
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

// TestApprovalMCPLabelSingleLineSanitized covers an MCP approval label whose
// server, tool, and agent ID carry an OSC payload, newline, and tab. The label
// must render exactly as the clean label, and the stored data keeps the original
// bytes.
func TestApprovalMCPLabelSingleLineSanitized(t *testing.T) {
	t.Parallel()

	const hostileServer = "srv\x1b]0;pwned\x07\n"
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
			hostile := &approvalPillData{kind: "mcp", server: hostileServer, mcpToolName: "tool\tname\r", agentID: "child\nx", resolved: tt.resolved, accepted: true}
			clean := &approvalPillData{kind: "mcp", server: "srv ", mcpToolName: "tool name", agentID: "child x", resolved: tt.resolved, accepted: true}
			got := b.renderApprovalPill(hostile, 80)
			requireOnlySGR(t, "mcp approval pill", got)
			if want := b.renderApprovalPill(clean, 80); got != want {
				t.Errorf("mcp approval pill = %q, want %q", stripANSI(got), stripANSI(want))
			}
			if hostile.server != hostileServer || hostile.mcpToolName != "tool\tname\r" || hostile.agentID != "child\nx" {
				t.Errorf("stored approval data changed: %+v", hostile)
			}
		})
	}
}
