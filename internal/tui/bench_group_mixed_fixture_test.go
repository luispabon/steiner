package tui

import "testing"

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
	if n := len(groups[0].entries); n != 3 {
		t.Fatalf("group entries = %d, want 3", n)
	}
	for _, dd := range groups[0].entries {
		if dd.status != "active" {
			t.Errorf("entry status = %q, want active", dd.status)
		}
	}
}
