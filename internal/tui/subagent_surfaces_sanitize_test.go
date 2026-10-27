package tui

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
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

// TestSidebarSingleLineGroupNormalized covers newline and tab in a sidebar group
// label. The group heading must stay on one line and match the clean rendering.
// The row targets must match the clean rendering too.
func TestSidebarSingleLineGroupNormalized(t *testing.T) {
	t.Parallel()

	entries := func(group string) []rosterEntry {
		return []rosterEntry{{agentID: "child-a", agentType: "explore", admitted: true, group: group, status: rosterRunning, startTime: 1}}
	}
	hostileLines, hostileTargets := rosterSidebar(entries("gr\noup\tA"), 2).subAgentsRows(80)
	cleanLines, cleanTargets := rosterSidebar(entries("gr oup A"), 2).subAgentsRows(80)
	for _, line := range hostileLines {
		if strings.ContainsAny(line, "\n\t") {
			t.Errorf("sidebar line %q contains a line break or tab", line)
		}
	}
	rest := requireOSC8Links(t, "sidebar group", strings.Join(hostileLines, "\n"), []string{"child-a"})
	cleanRest := requireOSC8Links(t, "clean sidebar group", strings.Join(cleanLines, "\n"), []string{"child-a"})
	if got, want := stripANSI(rest), stripANSI(cleanRest); got != want {
		t.Errorf("sidebar rows = %q, want %q", got, want)
	}
	if !slices.Equal(hostileTargets, cleanTargets) {
		t.Errorf("targets = %q, want %q", hostileTargets, cleanTargets)
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

// TestSubAgentPickerCardIDTypeSingleLine covers newline and tab in a picker
// card's agent ID and type. The card must stay on one line and match the clean
// rendering. The stored fields keep their original bytes.
func TestSubAgentPickerCardIDTypeSingleLine(t *testing.T) {
	t.Parallel()

	hostile := &delegationDisplayState{agentID: "a\nb\tc", agentType: "ex\tp", status: "complete", taskPreview: "task"}
	clean := &delegationDisplayState{agentID: "a b c", agentType: "ex p", status: "complete", taskPreview: "task"}

	got := cardRowParts(subAgentPickerRow{dd: hostile}, 120, 0).join()
	want := cardRowParts(subAgentPickerRow{dd: clean}, 120, 0).join()
	if strings.ContainsAny(got, "\n\t") || got != want {
		t.Errorf("picker card = %q, want %q", got, want)
	}
	if hostile.agentID != "a\nb\tc" || hostile.agentType != "ex\tp" {
		t.Errorf("stored identity changed: agentID %q, agentType %q", hostile.agentID, hostile.agentType)
	}
}

// TestDelegationHeaderIdentitySingleLineSanitized covers newline and tab in a
// delegation header's type, agent ID, and follow-up ID. The header must stay on
// one line and match the clean rendering. The stored fields keep their bytes.
func TestDelegationHeaderIdentitySingleLineSanitized(t *testing.T) {
	t.Parallel()

	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	tests := []struct {
		name           string
		hostile, clean delegationDisplayState
	}{
		{
			name:    "agent id and type",
			hostile: delegationDisplayState{agentID: "a\nb\tc", agentType: "ex\tp"},
			clean:   delegationDisplayState{agentID: "a b c", agentType: "ex p"},
		},
		{
			name:    "follow up id",
			hostile: delegationDisplayState{agentID: "a b c", agentType: "ex p", isFollowUp: true, followUpAgentID: "f\n1\t2"},
			clean:   delegationDisplayState{agentID: "a b c", agentType: "ex p", isFollowUp: true, followUpAgentID: "f 1 2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			hostile := tt.hostile
			got := b.renderDelegationHeaderIdentity(&hostile)
			clean := tt.clean
			want := b.renderDelegationHeaderIdentity(&clean)
			if strings.ContainsAny(got, "\n\t") || stripANSI(got) != stripANSI(want) {
				t.Errorf("header = %q, want %q", stripANSI(got), stripANSI(want))
			}
			if hostile.agentID != tt.hostile.agentID || hostile.agentType != tt.hostile.agentType || hostile.followUpAgentID != tt.hostile.followUpAgentID {
				t.Errorf("stored identity changed: %+v", hostile)
			}
		})
	}
}

// TestDelegateCancelRowSingleLineSanitized covers newline and tab in a cancel
// selector row's type and agent ID. The row must stay on one line and match the
// clean rendering. The stored row keeps its bytes.
func TestDelegateCancelRowSingleLineSanitized(t *testing.T) {
	t.Parallel()

	m := &Model{styles: testStyles(theme.AccentAmber)}
	hostile := delegateActiveRow{agentID: "a\nb\tc", agentType: "ex\tp", taskPreview: "task"}
	clean := delegateActiveRow{agentID: "a b c", agentType: "ex p", taskPreview: "task"}

	got := m.renderDelegateCancelRow(hostile, 120)
	if want := m.renderDelegateCancelRow(clean, 120); strings.ContainsAny(got, "\n\t") || got != want {
		t.Errorf("cancel row = %q, want %q", got, want)
	}
	if hostile.agentID != "a\nb\tc" || hostile.agentType != "ex\tp" {
		t.Errorf("stored row changed: %+v", hostile)
	}
}

// TestDelegationGroupFooterSingleLineSanitized covers a newline and tab in a
// delegation group name. The footer must match the clean group rendering. The
// stored group name keeps its bytes.
func TestDelegationGroupFooterSingleLineSanitized(t *testing.T) {
	t.Parallel()

	render := func(name string) (string, string) {
		m := newContentBenchModel()
		for j := range 2 {
			m = appendGroupChild(m, name, j, false)
		}
		var group *delegationGroupSegment
		for i := range m.content.segments {
			if g := m.content.segments[i].delegGroupData; g != nil {
				group = g
			}
		}
		if group == nil {
			t.Fatalf("no group segment built for %q", name)
		}
		b := &m.content
		return b.renderDelegationGroupSegment(contentSegment{delegGroupData: group}, 80), group.entries[0].admission.Group
	}
	got, stored := render("a\nb\tc")
	if stored != "a\nb\tc" {
		t.Fatalf("stored group = %q, want the raw name kept", stored)
	}
	want, _ := render("a b c")
	if stripANSI(got) != stripANSI(want) {
		t.Errorf("group segment = %q, want %q", stripANSI(got), stripANSI(want))
	}
}

// TestDeliveredRowsSingleLineSanitized covers an ESC sequence, newline, and tab
// in a delivered group label and failure reason. The group header and member
// reason must stay on one line. The delivered items keep their original bytes.
func TestDeliveredRowsSingleLineSanitized(t *testing.T) {
	t.Parallel()

	newItems := func() []output.DeliveredSubAgent {
		return []output.DeliveredSubAgent{
			{AgentID: "child-a", AgentType: "explore", Status: "failed"},
			{AgentID: "child-b", AgentType: "explore", Status: "failed"},
		}
	}
	const hostileGroup = "team\x1b[31m\nA\tx"
	const hostileReason = "bo\tom\x1b[2J\nsecond line"
	items := newItems()
	rows := buildDeliveredRows(items, func(output.DeliveredSubAgent) deliveredLookup {
		return deliveredLookup{group: hostileGroup, groupSize: 2, reason: hostileReason}
	})
	if want := "group team A x (2 of 2)"; rows.header != want {
		t.Errorf("header = %q, want %q", rows.header, want)
	}
	for _, m := range rows.members {
		if want := "bo om"; m.reason != want {
			t.Errorf("member %q reason = %q, want %q", m.label, m.reason, want)
		}
	}
	if !slices.Equal(items, newItems()) {
		t.Errorf("delivered items changed: %+v", items)
	}

	b := &contentBuffer{styles: testStyles(theme.AccentAmber)}
	out := b.renderSubAgentsFinishedSegment(contentSegment{deliveredData: &rows}, 120)
	requireOnlySGR(t, "delivered rows", out)
	if plain := stripANSI(out); !strings.Contains(plain, "group team A x (2 of 2)") || !strings.Contains(plain, "bo om") {
		t.Errorf("delivered rows = %q, want single-line header and reason", plain)
	}
}

// TestDeliveredMemberLabelSingleLineSanitized covers an ESC sequence, newline,
// and tab in a delivered member's agent type and ID. The member label must stay
// on one line and match the clean label. The delivered items keep their bytes.
func TestDeliveredMemberLabelSingleLineSanitized(t *testing.T) {
	t.Parallel()

	newItems := func() []output.DeliveredSubAgent {
		return []output.DeliveredSubAgent{
			{AgentID: "a\nb\tc\x1b[0m", AgentType: "ex\x1b[1m\tp", Status: "complete"},
		}
	}
	items := newItems()
	rows := buildDeliveredRows(items, nil)
	if len(rows.members) != 1 {
		t.Fatalf("members = %d, want 1", len(rows.members))
	}
	if want := "ex p a b c"; rows.members[0].label != want {
		t.Errorf("label = %q, want %q", rows.members[0].label, want)
	}
	if !slices.Equal(items, newItems()) {
		t.Errorf("delivered items changed: %+v", items)
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
