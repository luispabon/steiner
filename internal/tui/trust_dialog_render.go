package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// View implements tea.Model.
func (m *trustDialogModel) View() tea.View {
	var lines []string
	lines = append(lines, m.styles.CardLabel.Render(fmt.Sprintf("Trust project %s?", m.insp.ProjectRoot)))
	lines = append(lines, "")
	lines = append(lines, m.bodyLines()...)
	lines = append(lines, "")
	lines = append(lines, "Once trusted, future changes to this project's config apply without asking.")
	lines = append(lines, "")
	lines = append(lines, m.buttonRow())

	return tea.View{
		Content:   strings.Join(lines, "\n"),
		AltScreen: true,
	}
}

func (m *trustDialogModel) bodyLines() []string {
	switch {
	case m.insp.ConfigPath == "":
		return []string{"This project has no steiner config."}
	case m.insp.ParseError != "":
		msg := fmt.Sprintf("This project's config (%s) could not be parsed: %s", m.insp.ConfigPath, m.insp.ParseError)
		return []string{m.styles.WarningStyle.Render(msg)}
	case len(m.insp.Changes) == 0:
		return []string{fmt.Sprintf("Its config (%s) does not change any settings.", m.insp.ConfigPath)}
	default:
		return m.changesBody()
	}
}

func (m *trustDialogModel) changesBody() []string {
	header := fmt.Sprintf("Its config (%s) overrides your global config:", m.insp.ConfigPath)
	lines := []string{header}

	changeLines := m.renderChangeLines()
	height := m.changeListHeight()
	start := m.scroll
	if start > len(changeLines) {
		start = len(changeLines)
	}
	end := start + height
	if end > len(changeLines) {
		end = len(changeLines)
	}
	lines = append(lines, changeLines[start:end]...)

	if m.hasSecurityChange() {
		lines = append(lines, "", "Lines marked ! are security-relevant.")
	}
	return lines
}

func (m *trustDialogModel) hasSecurityChange() bool {
	for _, c := range m.insp.Changes {
		if c.Security {
			return true
		}
	}
	return false
}

func (m *trustDialogModel) renderChangeLines() []string {
	maxLen := 0
	for _, c := range m.insp.Changes {
		if l := len(c.Path); l > maxLen {
			maxLen = l
		}
	}

	lines := make([]string, 0, len(m.insp.Changes))
	for _, c := range m.insp.Changes {
		prefix := "  "
		if c.Security {
			prefix = "! "
		}
		line := fmt.Sprintf("%s%-*s %s → %s", prefix, maxLen, c.Path, c.Before, c.After)
		if c.Security {
			line = m.styles.WarningStyle.Bold(true).Render(line)
		}
		lines = append(lines, line)
	}
	return lines
}

func (m *trustDialogModel) buttonRow() string {
	buttons := []struct {
		choice TrustChoice
		label  string
	}{
		{TrustAlways, "[a] Always trust"},
		{TrustSession, "[s] This session only"},
		{TrustDeny, "[d] Deny and exit"},
	}

	rendered := make([]string, 0, len(buttons))
	for _, b := range buttons {
		if b.choice == m.selected {
			rendered = append(rendered, m.styles.Accent.Bold(true).Render(b.label))
		} else {
			rendered = append(rendered, b.label)
		}
	}
	return strings.Join(rendered, "   ")
}

// View implements tea.Model.
func (m *noticeDialogModel) View() tea.View {
	width := m.width
	if width < 1 {
		width = 80
	}
	wrapped := ansi.Hardwrap(ansi.Wordwrap(m.message, width, ""), width, true)
	wrapped = strings.TrimRight(wrapped, "\n")

	var lines []string
	lines = append(lines, m.styles.CardLabel.Render(m.title))
	lines = append(lines, "")
	lines = append(lines, wrapped)
	lines = append(lines, "")
	lines = append(lines, "Press Enter to continue")

	return tea.View{
		Content:   strings.Join(lines, "\n"),
		AltScreen: true,
	}
}
