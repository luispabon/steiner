package tui

import (
	"strings"
	"testing"
)

// TestMixedGroupCRFixtureSettledRowsSanitized checks that a settled entry of the
// CR fixture stores its carriage return, while the rows the group box draws hold
// no raw CR and keep the visible output text.
func TestMixedGroupCRFixtureSettledRowsSanitized(t *testing.T) {
	m := mixedGroupCRModel()
	found := false
	for _, dd := range mixedEntries(m) {
		if dd.status == "active" || !strings.Contains(dd.output, "\r") {
			continue
		}
		found = true
		rows := strings.Join(m.content.renderDelegationBoxRows(dd, 80), "\n")
		if strings.Contains(rows, "\r") {
			t.Errorf("rows for %q hold a raw carriage return", dd.agentID)
		}
		if !strings.Contains(stripANSI(rows), "progress 100% done") {
			t.Errorf("rows for %q lost visible output text", dd.agentID)
		}
	}
	if !found {
		t.Error("no settled entry stores a carriage return; the fixture would not test CR sanitizing")
	}
}

// TestSpinnerAdvanceFixtureIsUnnamedGroup checks that benchSpinnerAdvanceUnnamed
// builds one unnamed group of three active entries, the shape it is meant to
// measure.
func TestSpinnerAdvanceFixtureIsUnnamedGroup(t *testing.T) {
	m := spinnerAdvanceModel()
	var groups []*delegationGroupSegment
	for i := range m.content.segments {
		if g := m.content.segments[i].delegGroupData; g != nil {
			groups = append(groups, g)
		}
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	group := groups[0]
	if n := len(group.entries); n != 3 {
		t.Fatalf("group entries = %d, want 3", n)
	}
	if name := delegationVisualGroupName(group); name != "" {
		t.Errorf("group name = %q, want unnamed", name)
	}
	for _, dd := range group.entries {
		if _, named := acceptedMembership(dd); named {
			t.Errorf("entry %q has named membership, want none", dd.agentID)
		}
		if dd.status != "active" {
			t.Errorf("entry %q status = %q, want active", dd.agentID, dd.status)
		}
		if !dd.collapsed {
			t.Errorf("entry %q collapsed = false, want true", dd.agentID)
		}
	}
}
