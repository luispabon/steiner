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
	// contextMeasured reports whether a context budget event has reported
	// usage for the current conversation. A configured budget alone leaves
	// it false so the footer shows a placeholder instead of a measured 0%.
	contextMeasured bool
	oneshotPhase    string
	sandboxStatus   string

	subAgentsFinished int
	subAgentsTotal    int
	subAgentsQueued   int
	subAgentsFailed   int
	spinnerFrame      int

	// devLogUnseen and devLogWorst drive the internal error badge.
	devLogUnseen int
	devLogWorst  devLogSeverity
}

// statusSegment is one footer part plus the order in which truncation drops
// it. Lower dropRank values are removed first, so a segment can outlive parts
// that render to its right.
type statusSegment struct {
	text     string
	dropRank int
}

// Truncation drop order for a narrow footer, lowest first. Context drops
// before the mode and sandbox badges, so both badges survive while context is
// shed; the model and oneshot phase outlive every other segment.
const (
	dropApprovalDeny = iota + 1
	dropApprovalMove
	dropApprovalApprove
	dropContext
	dropDevLog
	dropHelp
	dropSidebar
	dropSubAgents
	dropSandbox
	dropMode
	dropModel
	dropPhase
)

//nolint:gocyclo // fan-out by segment is structural, not accidental
func (s statusState) view(width int) string {
	sep := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(" │ ")

	var parts []statusSegment

	// Segment 0: oneshot phase indicator (first, accent-styled)
	if s.oneshotPhase != "" {
		phaseStr := fmt.Sprintf("phase · %s", s.oneshotPhase)
		parts = append(parts, statusSegment{lipgloss.NewStyle().Foreground(s.styles.AccentColor).Render(phaseStr), dropPhase})
	}
	// Segment 1: model (stable left)
	if model := strings.TrimSpace(s.model); model != "" {
		parts = append(parts, statusSegment{lipgloss.NewStyle().Foreground(s.styles.AccentColor).Render(formatModelEffort(model, s.reasoning)), dropModel})
	}

	// Segment 1a: execution mode badge
	if badge := renderModeBadge(s.styles, s.execMode); badge != "" {
		parts = append(parts, statusSegment{badge, dropMode})
	}

	// Segment 1b: ctx (static, infrequently changing)
	parts = append(parts, statusSegment{s.contextSegment(), dropContext})

	// Segment 1c: sandbox status badge
	if badge := renderSandboxBadge(s.styles, s.sandboxStatus); badge != "" {
		parts = append(parts, statusSegment{badge, dropSandbox})
	}

	// Segment 1d: sub-agent chip (dropped only after ctx, help and sidebar)
	if chip := s.subAgentsChip(); chip != "" {
		parts = append(parts, statusSegment{chip, dropSubAgents})
	}

	// Segments 2-3: stable commands and navigation
	parts = append(parts, statusSegment{s.styles.KeyChip.Render("^B") + " sidebar", dropSidebar})
	parts = append(parts, statusSegment{s.styles.KeyChip.Render("Ctrl+F1") + " help", dropHelp})

	// Segment 5: internal error badge (unseen developer error log entries)
	if badge := s.devLogBadge(); badge != "" {
		parts = append(parts, statusSegment{badge, dropDevLog})
	}

	// Segment 6: transient action hints (very end)
	if s.approvalActive {
		parts = append(parts, statusSegment{s.styles.KeyChip.Render("y/a/n") + " approve", dropApprovalApprove})
		parts = append(parts, statusSegment{s.styles.KeyChip.Render("tab") + " move", dropApprovalMove})
		parts = append(parts, statusSegment{s.styles.KeyChip.Render("esc") + " deny", dropApprovalDeny})
	}

	join := func(segs []statusSegment) string {
		texts := make([]string, len(segs))
		for i := range segs {
			texts[i] = segs[i].text
		}
		return strings.Join(texts, sep)
	}

	text := join(parts)
	if width > 0 && lipgloss.Width(text) > width {
		for len(parts) > 1 {
			drop := 0
			for i := 1; i < len(parts); i++ {
				if parts[i].dropRank < parts[drop].dropRank {
					drop = i
				}
			}
			parts = append(parts[:drop], parts[drop+1:]...)
			text = join(parts)
			if lipgloss.Width(text) <= width {
				break
			}
		}
		if lipgloss.Width(text) > width {
			innerWidth := max(1, width-s.styles.StatusBar.GetHorizontalFrameSize())
			text = lipgloss.NewStyle().MaxWidth(innerWidth).Render(text)
		}
	}
	// WithBg is required: lipgloss resets inside the status bar content would
	// clear cell backgrounds in transparent terminals without it.
	if width > 0 {
		return theme.WithBg(s.styles.StatusBar.Width(width).Render(text), s.styles.Palette.ContentBG)
	}
	return theme.WithBg(s.styles.StatusBar.Render(text), s.styles.Palette.ContentBG)
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

// contextSegment renders the context usage indicator. Until a context budget
// event reports usage for this conversation it shows a placeholder, so a
// configured budget alone never claims a measured reading of zero.
func (s statusState) contextSegment() string {
	label := s.styles.FgMute.Render("ctx ")
	if !s.contextMeasured {
		return label + s.styles.FgMute.Render("--")
	}
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
	ctxStr := fmt.Sprintf("%s / %s · %d%%", formatCompactCount(s.promptUsed), formatCompactCount(s.contextBudget), pct)
	return label + lipgloss.NewStyle().Foreground(ctxColor).Render(ctxStr)
}

// subAgentsChip renders the sub-agent progress chip, empty when none were
// dispatched since the last prompt.
func (s statusState) subAgentsChip() string {
	if s.subAgentsTotal == 0 {
		return ""
	}
	count := fmt.Sprintf("%d/%d", s.subAgentsFinished, s.subAgentsTotal)
	if s.subAgentsQueued > 0 {
		count += fmt.Sprintf(" \u00b7 %d queued", s.subAgentsQueued)
	}
	label := s.styles.FgDim.Render("sub-agents ")
	if s.subAgentsFinished >= s.subAgentsTotal {
		icon := s.styles.SuccessStyle.Render("\u2713")
		if s.subAgentsFailed > 0 {
			icon = s.styles.ErrorStyle.Render("\u2717")
		}
		return icon + " " + label + count
	}
	icon := lipgloss.NewStyle().Foreground(s.styles.AccentColor).Render(spinnerFrames[s.spinnerFrame%len(spinnerFrames)])
	if s.subAgentsFailed > 0 {
		count = s.styles.ErrorStyle.Render("\u2717") + " " + count
	}
	return icon + " " + label + count
}
