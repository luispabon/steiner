package tui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

type statusState struct {
	model          string
	context        string
	reasoning      string
	mode           string
	execMode       string // execution mode: "plan" or "build"
	styles         *theme.Styles
	streaming      bool
	approvalActive bool
	promptUsed     int
	contextBudget  int
	oneshotPhase   string
	sandboxStatus  string
}

//nolint:gocyclo // fan-out by segment is structural, not accidental
func (s statusState) view(width int) string {
	sep := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(" │ ")

	var parts []string

	// Segment 0: oneshot phase indicator (first, accent-styled)
	if s.oneshotPhase != "" {
		phaseStr := fmt.Sprintf("phase · %s", s.oneshotPhase)
		parts = append(parts, lipgloss.NewStyle().Foreground(s.styles.AccentColor).Render(phaseStr))
	}
	// Segment 1: model (stable left)
	if s.model != "" {
		parts = append(parts, renderModelBadge(s.styles, s.model, s.reasoning))
	}

	// Segment 1b: execution mode badge
	if badge := renderModeBadge(s.styles, s.execMode); badge != "" {
		parts = append(parts, badge)
	}

	// Segment 1c: sandbox status badge
	if badge := renderSandboxBadge(s.styles, s.sandboxStatus); badge != "" {
		parts = append(parts, badge)
	}

	// Segments 2-3: stable commands and navigation
	parts = append(parts, s.styles.KeyChip.Render("^B")+" sidebar")
	parts = append(parts, s.styles.KeyChip.Render("?")+" help")

	// Segment 6: ctx (static, infrequently changing)
	if s.contextBudget > 0 || s.promptUsed > 0 {
		pct := 0
		if s.contextBudget > 0 {
			pct = (s.promptUsed * 100) / s.contextBudget
		}
		var ctxColor color.Color
		switch {
		case pct > 90:
			ctxColor = lipgloss.Color(theme.Removed)
		case pct > 70:
			ctxColor = lipgloss.Color(theme.Warn)
		default:
			ctxColor = s.styles.AccentColor
		}
		ctxStr := fmt.Sprintf("%d/%d · %d%%", s.promptUsed, s.contextBudget, pct)
		label := s.styles.FgMute.Render("ctx ")
		val := lipgloss.NewStyle().Foreground(ctxColor).Render(ctxStr)
		parts = append(parts, label+val)
	}

	// Segment 7: transient action hints (very end)
	if s.approvalActive {
		parts = append(parts, s.styles.KeyChip.Render("y/a/n")+" approve")
		parts = append(parts, s.styles.KeyChip.Render("tab")+" move")
		parts = append(parts, s.styles.KeyChip.Render("esc")+" deny")
	}

	text := strings.Join(parts, sep)
	if width > 0 && lipgloss.Width(text) > width {
		for len(parts) > 1 {
			parts = parts[:len(parts)-1]
			text = strings.Join(parts, sep)
			if lipgloss.Width(text) <= width {
				break
			}
		}
	}
	// WithBg is required: lipgloss resets inside the status bar content would
	// clear cell backgrounds in transparent terminals without it.
	if width > 0 {
		return theme.WithBg(s.styles.StatusBar.Width(width).Render(text), theme.BgElev)
	}
	return theme.WithBg(s.styles.StatusBar.Render(text), theme.BgElev)
}

// renderStatus memoizes statusState.view, keyed on the entire statusState
// value plus width. statusState is fully comparable (strings, bools, ints,
// and one *theme.Styles pointer), so the key IS the input: there is no
// invalidation logic that can go stale, because any change to any field
// changes the key. The cache lives on Model rather than statusState so that
// comparing the key never compares the cached output against itself.
func (m *Model) renderStatus(width int) string {
	if m.statusViewCacheSet && m.statusViewCacheKey == m.status && m.statusViewCacheWidth == width {
		return m.statusViewCacheRendered
	}
	rendered := m.status.view(width)
	m.statusViewCacheSet = true
	m.statusViewCacheKey = m.status
	m.statusViewCacheWidth = width
	m.statusViewCacheRendered = rendered
	return rendered
}
