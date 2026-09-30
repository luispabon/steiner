package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func groupTestBuffer() *contentBuffer {
	return &contentBuffer{collapseState: make(map[int]bool), styles: testStyles("#5599ff")}
}

func groupOf(label string, statuses ...string) *delegationGroupSegment {
	g := &delegationGroupSegment{}
	for i, st := range statuses {
		g.entries = append(g.entries, &delegationDisplayState{
			agentID: "child-" + string(rune('1'+i)), toolLabel: "explore",
			group: label, status: st, collapsed: true,
		})
	}
	return g
}

func TestRenderDelegationGroupTopBorder(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		group    *delegationGroupSegment
		inner    int
		wantSubs []string
		wantNot  []string
	}{
		{"complete", groupOf("discovery", "complete", "complete"), 60, []string{"discovery", "2 agents", "✓ 2/2"}, nil},
		{"running", groupOf("discovery", "complete", "active"), 60, []string{"discovery", "1/2"}, []string{"✓"}},
		{"failed", groupOf("discovery", "complete", "failed"), 60, []string{"✗ 1 failed"}, nil},
		{"budget", groupOf("discovery", "budget_exhausted", "complete"), 60, []string{"! 1 budget exhausted"}, nil},
		{"unlabelled", groupOf("", "complete", "complete"), 60, []string{"2 agents"}, nil},
		{"name truncated first", groupOf("a-very-long-group-name", "complete", "complete"), 30, []string{"…", "2 agents"}, []string{"a-very-long-group-name"}},
		{"stats dropped", groupOf("discovery", "complete", "complete"), 18, []string{"discovery"}, []string{"agents"}},
		{"tiny", groupOf("discovery", "complete", "complete"), 4, nil, []string{"discovery", "agents"}},
	}
	b := groupTestBuffer()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Lock per subtest: a parent holding tuiProfileMu while its parallel
			// subtests wait for a slot deadlocks once lock waiters fill them all.
			useTrueColor(t)
			line := b.renderDelegationGroupTopBorder(tt.group, tt.inner, lipgloss.Color("#00aa88"))
			plain := ansi.Strip(line)
			if got := lipgloss.Width(plain); got != tt.inner+2 {
				t.Errorf("width = %d, want %d: %q", got, tt.inner+2, plain)
			}
			if !strings.HasPrefix(plain, "┌") || !strings.HasSuffix(plain, "┐") {
				t.Errorf("corners not closed: %q", plain)
			}
			for _, s := range tt.wantSubs {
				if !strings.Contains(plain, s) {
					t.Errorf("missing %q in %q", s, plain)
				}
			}
			for _, s := range tt.wantNot {
				if strings.Contains(plain, s) {
					t.Errorf("unexpected %q in %q", s, plain)
				}
			}
		})
	}
}

func TestRenderDelegationGroupSegmentTitleInBorder(t *testing.T) {
	t.Parallel()
	useTrueColor(t)
	b := groupTestBuffer()
	seg := contentSegment{kind: segmentDelegationGroup, delegGroupData: groupOf("discovery", "complete", "complete")}
	out := b.renderDelegationGroupSegment(seg, 60)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	first := ansi.Strip(lines[0])
	if !strings.Contains(first, "discovery") {
		t.Errorf("top border missing group name: %q", first)
	}
	if strings.Contains(ansi.Strip(out), "group discovery") {
		t.Errorf("old standalone group line still rendered")
	}
	if lipgloss.Width(lines[0]) != lipgloss.Width(lines[len(lines)-1]) {
		t.Errorf("top and bottom border widths differ: %d vs %d", lipgloss.Width(lines[0]), lipgloss.Width(lines[len(lines)-1]))
	}
	if !strings.Contains(lines[0], "\x1b[1") {
		t.Errorf("group name is not bold: %q", lines[0])
	}
}

func TestDelegationGroupEntryAtRowLabelledGroup(t *testing.T) {
	t.Parallel()
	b := groupTestBuffer()
	g := groupOf("discovery", "complete", "complete")
	first := len(b.delegationContentRows(g.entries[0], 60))
	if e, r := b.delegationGroupEntryAtRow(g, 1, 60); e != 0 || r != 0 {
		t.Errorf("row 1 = (%d,%d), want (0,0): header row of first child", e, r)
	}
	if e, _ := b.delegationGroupEntryAtRow(g, 1+first, 60); e != -1 {
		t.Errorf("divider row mapped to entry %d, want -1", e)
	}
	if e, r := b.delegationGroupEntryAtRow(g, 2+first, 60); e != 1 || r != 0 {
		t.Errorf("second child header = (%d,%d), want (1,0)", e, r)
	}
}
