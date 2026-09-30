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

func TestRenderDelegationGroupFooter(t *testing.T) {
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
		{"tiny", groupOf("discovery", "complete", "complete"), 1, nil, []string{"discovery", "agents"}},
		{"full width", groupOf("discovery", "complete", "complete"), 11, []string{"discovery"}, []string{"agents"}},
		{"wide name", groupOf("探索作業探索作業探索作業", "complete", "complete"), 30, []string{"…", "2 agents"}, []string{"探索作業探索作業探索作業"}},
		{"singular", groupOf("discovery", "complete"), 60, []string{"1 agent · ✓ 1/1"}, []string{"agents"}},
		{"active wins", groupOf("discovery", "active", "failed"), 60, []string{"0/2"}, []string{"✗", "failed"}},
	}
	b := groupTestBuffer()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Lock per subtest: a parent holding tuiProfileMu while its parallel
			// subtests wait for a slot deadlocks once lock waiters fill them all.
			useTrueColor(t)
			line := b.renderDelegationGroupFooter(tt.group, tt.inner, lipgloss.Color("#00aa88"))
			plain := ansi.Strip(line)
			lines := strings.Split(plain, "\n")
			for _, row := range lines {
				if got := lipgloss.Width(row); got != tt.inner+2 {
					t.Errorf("width = %d, want %d: %q", got, tt.inner+2, row)
				}
			}
			if tt.inner == 1 {
				if plain != "└─┘" {
					t.Errorf("empty footer = %q, want plain bottom border", plain)
				}
			} else {
				if len(lines) != 3 {
					t.Fatalf("footer has %d lines, want 3: %q", len(lines), plain)
				}
				top := strings.TrimLeft(lines[0], "└─")
				if !strings.HasPrefix(top, "┬") && !strings.HasPrefix(top, "├") || !strings.HasSuffix(top, "┤") {
					t.Errorf("tab not attached to bottom border: %q", lines[0])
				}
				label := strings.TrimLeft(lines[1], " ")
				if !strings.HasPrefix(label, "│ ") || !strings.HasSuffix(label, " │") {
					t.Errorf("label not enclosed and padded: %q", lines[1])
				}
				bottom := strings.TrimLeft(lines[2], " ")
				if !strings.HasPrefix(bottom, "╰") || !strings.HasSuffix(bottom, "╯") {
					t.Errorf("tab corners not rounded: %q", lines[2])
				}
				if lipgloss.Width(label) != lipgloss.Width(bottom) {
					t.Errorf("tab edges misaligned: %q, %q", label, bottom)
				}
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

func TestRenderDelegationGroupFooterRequiresSharedGroupAndBatch(t *testing.T) {
	t.Parallel()
	useTrueColor(t)
	b := groupTestBuffer()
	for _, tc := range []struct {
		name      string
		mutate    func(*delegationGroupSegment)
		wantNamed bool
	}{
		{"same group and batch", func(*delegationGroupSegment) {}, true},
		{"different group", func(g *delegationGroupSegment) { g.entries[1].group = "other" }, false},
		{"different batch", func(g *delegationGroupSegment) { g.entries[1].batch++ }, false},
		{"empty group", func(g *delegationGroupSegment) { g.entries[0].group = "" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := groupOf("discovery", "complete", "complete")
			tc.mutate(g)
			out := ansi.Strip(b.renderDelegationGroupSegment(contentSegment{kind: segmentDelegationGroup, delegGroupData: g}, 60))
			if got := strings.Contains(out, "discovery"); got != tc.wantNamed {
				t.Errorf("named footer = %v, want %v: %q", got, tc.wantNamed, out)
			}
			if !tc.wantNamed && !strings.HasSuffix(strings.TrimSuffix(out, "\n"), "┘") {
				t.Errorf("ineligible footer did not use plain bottom border: %q", out)
			}
		})
	}
}

func TestRenderDelegationGroupSegmentTitleInFooter(t *testing.T) {
	t.Parallel()
	useTrueColor(t)
	b := groupTestBuffer()
	seg := contentSegment{kind: segmentDelegationGroup, delegGroupData: groupOf("discovery", "complete", "complete")}
	out := b.renderDelegationGroupSegment(seg, 60)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	first := ansi.Strip(lines[0])
	if first != "┌"+strings.Repeat("─", lipgloss.Width(first)-2)+"┐" {
		t.Errorf("top border not continuous: %q", first)
	}
	if !strings.Contains(ansi.Strip(lines[len(lines)-2]), "discovery") {
		t.Errorf("footer missing group name: %q", lines[len(lines)-2])
	}
	for _, line := range lines {
		if lipgloss.Width(line) != lipgloss.Width(first) {
			t.Errorf("line width differs from box width: %q", line)
		}
	}
	if strings.Contains(ansi.Strip(out), "group discovery") {
		t.Errorf("old standalone group line still rendered")
	}
	if lipgloss.Width(lines[0]) != lipgloss.Width(lines[len(lines)-1]) {
		t.Errorf("top and bottom border widths differ: %d vs %d", lipgloss.Width(lines[0]), lipgloss.Width(lines[len(lines)-1]))
	}
	name := b.styles.DelegateTagStyles["explore"].Background(lipgloss.Color(b.styles.Palette.ContentBG)).Render("discovery")
	if !strings.Contains(lines[len(lines)-2], name) {
		t.Errorf("group name missing bold bright agent colour: %q", lines[len(lines)-2])
	}
}

func TestRenderDelegationGroupFooterGlyphColours(t *testing.T) {
	t.Parallel()
	useTrueColor(t)
	b := groupTestBuffer()
	bg := lipgloss.Color(b.styles.Palette.ContentBG)
	tests := []struct {
		name     string
		statuses []string
		glyph    string
		style    lipgloss.Style
	}{
		{"success", []string{"complete", "complete"}, "✓", b.styles.SuccessStyle},
		{"error", []string{"complete", "failed"}, "✗", b.styles.ErrorStyle},
		{"budget", []string{"budget_exhausted", "complete"}, "!", b.styles.Warn},
		{"active overrides error", []string{"active", "failed"}, spinnerFrames[0], b.styles.FgMute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := groupOf("discovery", tt.statuses...)
			out := b.renderDelegationGroupFooter(g, 60, lipgloss.Color("#00aa88"))
			want := tt.style.Background(bg).Render(tt.glyph)
			if !strings.Contains(out, want) {
				t.Errorf("footer missing state-coloured glyph %q: %q", want, out)
			}
			if strings.Contains(out, b.styles.FgDim.Background(bg).Render(tt.glyph)) {
				t.Errorf("glyph uses label colour instead of state colour: %q", out)
			}
		})
	}
}

func TestRenderDelegationGroupFooterNameColours(t *testing.T) {
	t.Parallel()
	useTrueColor(t)
	b := groupTestBuffer()
	bg := lipgloss.Color(b.styles.Palette.ContentBG)
	for key, tag := range b.styles.DelegateTagStyles {
		t.Run(key, func(t *testing.T) {
			g := groupOf("discovery", "complete")
			g.entries[0].toolLabel = strings.ToUpper(key)
			out := b.renderDelegationGroupFooter(g, 60, b.styles.DelegateBorderStyles[key].GetForeground())
			want := tag.Background(bg).Bold(true).Render("discovery")
			if !strings.Contains(out, want) {
				t.Errorf("footer missing bold bright name %q: %q", want, out)
			}
		})
	}
	for _, label := range []string{"mixed", "unknown"} {
		t.Run(label, func(t *testing.T) {
			g := groupOf("discovery", "complete", "complete")
			g.entries[0].toolLabel = "unknown"
			if label == "unknown" {
				g.entries[1].toolLabel = "unknown"
			}
			out := b.renderDelegationGroupFooter(g, 60, b.styles.DelegateBorderDefault.GetForeground())
			want := b.styles.FgDim.Foreground(b.styles.AccentColor).Background(bg).Bold(true).Render("discovery")
			if !strings.Contains(out, want) {
				t.Errorf("default footer missing bold accent name %q: %q", want, out)
			}
		})
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
	footerStart := 2 + first + len(b.delegationContentRows(g.entries[1], 60))
	for row := footerStart; row < footerStart+3; row++ {
		if e, r := b.delegationGroupEntryAtRow(g, row, 60); e != -1 || r != -1 {
			t.Errorf("footer row %d = (%d,%d), want (-1,-1)", row, e, r)
		}
	}
}
