package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestStatusBarRendersPhaseSegment(t *testing.T) {
	s := statusState{oneshotPhase: "plan"}
	result := s.view(80)
	if !strings.Contains(result, "phase") || !strings.Contains(result, "plan") {
		t.Errorf("status bar should contain 'phase' and 'plan', got: %s", result)
	}
}

func TestStatusBarOmitsPhaseWhenEmpty(t *testing.T) {
	s := statusState{oneshotPhase: ""}
	result := s.view(80)
	if strings.Contains(result, "phase ·") {
		t.Errorf("status bar should not contain 'phase ·', got: %s", result)
	}
}

func TestRenderModeBadge(t *testing.T) {
	cases := []struct {
		name  string
		mode  string
		want  string
		empty bool
	}{
		{name: "plan", mode: "plan", want: "plan "},
		{name: "build", mode: "build", want: "build"},
		{name: "unset", mode: "", empty: true},
		{name: "unrecognized", mode: "bogus", empty: true},
	}
	styles := theme.BuildStyles(theme.AccentAmber)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderModeBadge(styles, tc.mode)
			if tc.empty {
				if got != "" {
					t.Errorf("renderModeBadge(%q) = %q, want empty", tc.mode, got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("renderModeBadge(%q) = %q, want to contain %q", tc.mode, got, tc.want)
			}
		})
	}
}

func TestStatusBarRendersModeBadge(t *testing.T) {
	styles := theme.BuildStyles(theme.AccentAmber)
	s := statusState{execMode: "plan", styles: styles}
	result := s.view(120)
	if !strings.Contains(result, "plan") {
		t.Errorf("status bar should contain mode badge 'plan', got: %s", result)
	}
}

func TestStatusBarOmitsModeBadgeWhenUnset(t *testing.T) {
	styles := theme.BuildStyles(theme.AccentAmber)
	s := statusState{execMode: "", styles: styles}
	result := s.view(120)
	// When execMode is unset, renderModeBadge returns "" so no styled mode
	// label appears. "plan " (with trailing space) guards against the badge;
	// the bare word "plan" may legitimately appear in the oneshot phase segment.
	if strings.Contains(result, "plan ") || strings.Contains(result, "build") {
		t.Errorf("status bar should not contain mode badge when unset, got: %s", result)
	}
}
