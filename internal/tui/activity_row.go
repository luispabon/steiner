package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

type activityState struct {
	label    string
	detail   string
	spinning bool
	accent   lipgloss.Color
	spinner  spinner.Model
}

func newActivityState(styles theme.Styles) activityState {
	return activityState{
		accent:  styles.AccentColor,
		spinner: newActivitySpinner(styles.AccentColor),
	}
}

func newActivitySpinner(accent lipgloss.Color) spinner.Model {
	return spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(accent)),
	)
}

func (a activityState) withStyles(styles theme.Styles) activityState {
	a.accent = styles.AccentColor
	a.spinner.Style = lipgloss.NewStyle().Foreground(styles.AccentColor)
	return a
}

func (a activityState) busy() bool {
	return a.spinning
}

func (a activityState) view(width int, styles theme.Styles) string {
	if width < 1 {
		return ""
	}

	var parts []string
	if a.spinning {
		parts = append(parts, a.spinner.View())
	}
	if label := strings.TrimSpace(a.label); label != "" {
		parts = append(parts, styles.FgMute.Render(label))
	}
	if detail := strings.TrimSpace(a.detail); detail != "" {
		parts = append(parts, styles.Accent.Render(detail))
	}

	text := strings.Join(parts, " ")
	if text == "" {
		text = " "
	}
	return theme.WithBg(styles.StatusBar.Width(width).Render(text), lipgloss.Color(theme.BgElev))
}

func (a activityState) advance() activityState {
	if !a.spinning {
		return a
	}
	a.spinner, _ = a.spinner.Update(a.spinner.Tick())
	return a
}

func (a activityState) waiting(label, detail string) activityState {
	if !a.spinning {
		a.spinner = newActivitySpinner(a.accent)
	}
	a.label = label
	a.detail = detail
	a.spinning = true
	return a
}

func (a activityState) static(label, detail string) activityState {
	a.label = label
	a.detail = detail
	a.spinning = false
	return a
}

func (a activityState) clear() activityState {
	a.label = ""
	a.detail = ""
	a.spinning = false
	return a
}

func turnLabel(turn int) string {
	if turn < 1 {
		return ""
	}
	return fmt.Sprintf("turn %d", turn)
}

func compactingLabel(payload output.ContextCompactionEvent) string {
	parts := make([]string, 0, 2)
	if payload.CompactionCount > 0 {
		suffix := ""
		if payload.CompactionCount != 1 {
			suffix = "s"
		}
		parts = append(parts, fmt.Sprintf("%d compaction%s", payload.CompactionCount, suffix))
	}
	if payload.Turn > 0 {
		parts = append(parts, turnLabel(payload.Turn))
	}
	return strings.Join(parts, " · ")
}

func compactedLabel(payload output.ContextCompactionEvent) string {
	parts := make([]string, 0, 2)
	switch {
	case strings.TrimSpace(payload.SummaryTitle) != "":
		parts = append(parts, strings.TrimSpace(payload.SummaryTitle))
	case payload.CompactedMessages > 0:
		parts = append(parts, fmt.Sprintf("%d messages summarized", payload.CompactedMessages))
	case payload.CompactedTurns > 0:
		parts = append(parts, fmt.Sprintf("%d turns summarized", payload.CompactedTurns))
	default:
		parts = append(parts, "context compacted")
	}
	if payload.Turn > 0 {
		parts = append(parts, turnLabel(payload.Turn))
	}
	return strings.Join(parts, " · ")
}

func approvalDetail(payload output.ApprovalEvent) string {
	parts := make([]string, 0, 2)
	if tool := strings.TrimSpace(payload.Tool); tool != "" {
		parts = append(parts, tool)
	}
	if mode := strings.TrimSpace(payload.Mode); mode != "" {
		parts = append(parts, mode)
	}
	return strings.Join(parts, " · ")
}

func approvalResultLabel(eventType string) string {
	switch eventType {
	case output.EventTypeApprovalAccepted:
		return "approval accepted"
	case output.EventTypeApprovalDenied:
		return "approval denied"
	default:
		return "approval resolved"
	}
}

func toolCallDetail(tool string, args map[string]any) string {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return ""
	}
	switch {
	case len(args) == 0:
		return tool
	default:
		return tool
	}
}
