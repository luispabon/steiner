package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/tui/theme"
)

type oneshotResumePickerOverlay struct {
	OverlayShell
	query        string
	allRuns      []oneshot.ResumableRun
	candidates   []oneshot.ResumableRun
	selection    int
	scrollOffset int
	styles       theme.Styles
}

func newOneshotResumePickerOverlay(styles theme.Styles) oneshotResumePickerOverlay {
	return oneshotResumePickerOverlay{styles: styles}
}

func (o oneshotResumePickerOverlay) withDimensions(width, height int) oneshotResumePickerOverlay {
	o.OverlayShell = o.WithDimensions(width, height)
	return o
}

func (o oneshotResumePickerOverlay) Open(runs []oneshot.ResumableRun) oneshotResumePickerOverlay {
	o.OverlayShell = o.openShell()
	o.query = ""
	o.selection = 0
	o.scrollOffset = 0
	o.allRuns = append([]oneshot.ResumableRun(nil), runs...)
	o.candidates = append([]oneshot.ResumableRun(nil), runs...)
	return o
}

func (o oneshotResumePickerOverlay) Close() oneshotResumePickerOverlay {
	o.OverlayShell = o.closeShell()
	return o
}

func (o oneshotResumePickerOverlay) Update(msg tea.Msg) (oneshotResumePickerOverlay, tea.Cmd) {
	if !o.IsOpen() {
		return o, nil
	}
	switch updateSearchPicker(&o.query, &o.selection, &o.scrollOffset, &o.candidates, o.allRuns, msg, func(query string, runs []oneshot.ResumableRun) []oneshot.ResumableRun {
		return filterSearchPickerEntries(runs, query, func(run oneshot.ResumableRun, loweredQuery string) bool {
			return strings.Contains(strings.ToLower(run.Task), loweredQuery) ||
				strings.Contains(strings.ToLower(run.RunID), loweredQuery) ||
				strings.Contains(strings.ToLower(run.Slug), loweredQuery)
		})
	}) {
	case searchPickerClosed:
		return o.Close(), nil
	case searchPickerHandled, searchPickerIgnored:
		return o, nil
	default:
		return o, nil
	}
}

func (o oneshotResumePickerOverlay) View() string {
	if !o.IsOpen() {
		return ""
	}

	innerWidth := o.InnerWidth()

	prefix := o.styles.Accent.Render("▶")
	queryDisplay := o.query
	if queryDisplay == "" {
		if len(o.candidates) > 0 {
			queryDisplay = o.styles.Accent.Render(o.candidates[o.selection].Task)
		} else {
			queryDisplay = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Render("search runs…")
		}
	}
	headerLine := lipgloss.NewStyle().Width(innerWidth).Render(prefix + " " + queryDisplay)
	divider := o.Divider()

	lines := []string{headerLine, divider}

	for i := o.scrollOffset; i < min(o.scrollOffset+maxDisplay, len(o.candidates)); i++ {
		run := o.candidates[i]
		row := o.formatRunRow(run, innerWidth)
		if i == o.selection {
			lines = append(lines, o.styles.AccentBg.MaxWidth(innerWidth).Render(row))
		} else {
			lines = append(lines, lipgloss.NewStyle().MaxWidth(innerWidth).Render(row))
		}
	}

	if len(o.candidates) > o.scrollOffset+maxDisplay {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Render(fmt.Sprintf("… and %d more", len(o.candidates)-(o.scrollOffset+maxDisplay))))
	}

	footerText := FooterChip("↵") + " select   " + FooterChip("↑↓") + " navigate   " + FooterChip("esc") + " close"
	lines = append(lines, o.Divider(), o.RenderFooter(footerText))

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return o.RenderWithBg(o.styles.PaletteOverlay, body, lipgloss.Color(theme.BgElev))
}

func (o oneshotResumePickerOverlay) formatRunRow(run oneshot.ResumableRun, maxWidth int) string {
	datetime := fmt.Sprintf("[ %s ] ", run.UpdatedAt.Local().Format("2006-01-02 15:04:05"))
	phaseStr := fmt.Sprintf(" (%s)", run.ResumePhase)
	idSuffix := fmt.Sprintf(" [%s]", run.RunID[len(run.RunID)-8:])
	spacer := " "

	prefixWidth := lipgloss.Width(datetime)
	suffixWidth := lipgloss.Width(phaseStr) + lipgloss.Width(spacer) + lipgloss.Width(idSuffix)
	titleMaxWidth := maxWidth - prefixWidth - suffixWidth
	if titleMaxWidth < 10 {
		titleMaxWidth = 10
	}

	task := run.Task
	if len(task) > titleMaxWidth {
		task = task[:titleMaxWidth-1] + "…"
	}

	return datetime + task + phaseStr + spacer + idSuffix
}

func (o oneshotResumePickerOverlay) SelectedRunID() string {
	if o.selection >= 0 && o.selection < len(o.candidates) {
		return o.candidates[o.selection].RunID
	}
	return ""
}
