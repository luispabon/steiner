package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// reasoningEffortOption is a single selectable entry in the reasoning effort
// picker: either the provider-default sentinel or one of the model's
// concrete supported effort values. Value, provider-default state, current
// marker, and description are kept as structured fields rather than encoded
// into a rendered label string.
type reasoningEffortOption struct {
	Value             string
	Label             string
	Description       string
	IsProviderDefault bool
	IsCurrent         bool
}

// reasoningPickerOverlay is the second step of the /model flow: choosing a
// reasoning effort for a model that exposes reasoning options. It opens
// after a reasoning-capable model is selected in modelPickerOverlay.
type reasoningPickerOverlay struct {
	OverlayShell
	query        string
	allOptions   []reasoningEffortOption
	candidates   []reasoningEffortOption
	selection    int
	scrollOffset int
	modelName    string
	styles       *theme.Styles
}

func newReasoningPickerOverlay(styles *theme.Styles) reasoningPickerOverlay {
	return reasoningPickerOverlay{styles: styles}
}

// Open builds the effort option list for modelName from caps: a leading
// provider-default option followed by each of caps.SupportedEfforts. current
// marks whichever option matches the caller's current reasoning override. If
// current carries no override (ReasoningOverrideNone or the zero value),
// configuredEffort is used instead: it marks the matching effort option as
// current, or the provider-default option when configuredEffort is empty.
func (m reasoningPickerOverlay) Open(modelName string, caps provider.ReasoningCapabilities, current provider.ReasoningOverride, configuredEffort string) reasoningPickerOverlay {
	m.OverlayShell = m.openShell()
	m.query = ""
	m.modelName = modelName

	defaultDesc := "let the provider decide"
	if strings.TrimSpace(caps.ProviderDefaultEffort) != "" {
		defaultDesc = "default (" + caps.ProviderDefaultEffort + ")"
	}

	hasOverride := current.Kind == provider.ReasoningOverrideProviderDefault || current.Kind == provider.ReasoningOverrideEffort
	isCurrentDefault := current.Kind == provider.ReasoningOverrideProviderDefault
	isCurrentEffort := func(effort string) bool {
		return current.Kind == provider.ReasoningOverrideEffort && current.Effort == effort
	}
	if !hasOverride {
		isCurrentDefault = configuredEffort == ""
		isCurrentEffort = func(effort string) bool { return configuredEffort == effort }
	}

	options := make([]reasoningEffortOption, 0, len(caps.SupportedEfforts)+1)
	options = append(options, reasoningEffortOption{
		Label:             "default",
		Description:       defaultDesc,
		IsProviderDefault: true,
		IsCurrent:         isCurrentDefault,
	})
	for _, effort := range caps.SupportedEfforts {
		options = append(options, reasoningEffortOption{
			Value:     effort,
			Label:     effort,
			IsCurrent: isCurrentEffort(effort),
		})
	}

	m.allOptions = options
	m.candidates = append([]reasoningEffortOption(nil), options...)
	m.selection = 0
	m.scrollOffset = 0
	for i, opt := range m.candidates {
		if !opt.IsCurrent {
			continue
		}
		m.selection = i
		if i >= maxDisplay {
			m.scrollOffset = i - maxDisplay + 1
		}
		break
	}
	return m
}

// Close closes the picker.
func (m reasoningPickerOverlay) Close() reasoningPickerOverlay {
	m.OverlayShell = m.closeShell()
	return m
}

// Update handles key events for search, navigation, and dismissal.
func (m reasoningPickerOverlay) Update(msg tea.Msg) (reasoningPickerOverlay, tea.Cmd) {
	if !m.IsOpen() {
		return m, nil
	}
	switch updateSearchPicker(&m.query, &m.selection, &m.scrollOffset, &m.candidates, m.allOptions, msg, func(query string, entries []reasoningEffortOption) []reasoningEffortOption {
		loweredQuery := strings.ToLower(query)
		filtered := make([]reasoningEffortOption, 0, len(entries))
		for _, entry := range entries {
			if strings.Contains(strings.ToLower(entry.Label), loweredQuery) {
				filtered = append(filtered, entry)
			}
		}
		return filtered
	}) {
	case searchPickerClosed:
		return m.Close(), nil
	case searchPickerHandled, searchPickerIgnored:
		return m, nil
	default:
		return m, nil
	}
}

// SelectedOption returns the currently highlighted option, if any.
func (m reasoningPickerOverlay) SelectedOption() (reasoningEffortOption, bool) {
	if m.selection >= 0 && m.selection < len(m.candidates) {
		return m.candidates[m.selection], true
	}
	return reasoningEffortOption{}, false
}

// View renders the overlay as a positioned overlay string.
func (m reasoningPickerOverlay) View() string {
	if !m.IsOpen() {
		return ""
	}
	return m.render(m.reasoningPickerInnerWidth())
}

func (m reasoningPickerOverlay) render(innerW int) string {
	const selectionPrefix = "> "
	const idlePrefix = "  "

	lines := make([]string, 0, maxDisplay+5)

	title := "reasoning for " + m.modelName
	lines = append(lines, lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Fg)).
		Bold(true).
		Width(innerW).
		Render(title))

	queryDisplay := m.query
	if queryDisplay == "" {
		queryDisplay = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute)).Render("search effort…")
	}
	headerLine := lipgloss.NewStyle().Width(innerW).Render(m.styles.Accent.Render("effort") + " " + queryDisplay)
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BorderSoft)).Render(strings.Repeat("─", innerW))
	lines = append(lines, headerLine, divider)

	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Fg))
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.FgMute))
	currentStyle := m.styles.Accent

	for i := m.scrollOffset; i < min(m.scrollOffset+maxDisplay, len(m.candidates)); i++ {
		opt := m.candidates[i]
		var row string
		if opt.IsCurrent {
			row = currentStyle.Render(opt.Label)
		} else {
			row = nameStyle.Render(opt.Label)
		}
		if desc := strings.TrimSpace(opt.Description); desc != "" {
			row += " " + descStyle.Render(desc)
		}
		if i == m.selection {
			lines = append(lines, m.styles.PaletteItemActive.
				Width(innerW).
				MaxWidth(innerW).
				Render(m.styles.Accent.Render(selectionPrefix)+row))
		} else {
			lines = append(lines, lipgloss.NewStyle().
				Width(innerW).
				MaxWidth(innerW).
				Render(idlePrefix+row))
		}
	}

	body := lipgloss.JoinVertical(lipgloss.Left, lines...)
	// WithBg is required: nested lipgloss renders emit ANSI resets that clear
	// cell backgrounds in transparent terminals; the box Background() does not
	// re-apply after resets inside the body.
	return theme.WithBg(m.styles.PaletteOverlay.Width(innerW+4).Padding(1, 1).Render(body), theme.BgElev)
}

// reasoningOverrideLabel renders a compact sidebar label for a reasoning
// override, e.g. "medium" or "default".
func reasoningOverrideLabel(o provider.ReasoningOverride) string {
	switch o.Kind {
	case provider.ReasoningOverrideEffort:
		return o.Effort
	case provider.ReasoningOverrideProviderDefault:
		return "default"
	default:
		return ""
	}
}

func (m reasoningPickerOverlay) reasoningPickerInnerWidth() int {
	const maxOverlayInner = 90
	inner := m.InnerWidth()
	if inner > maxOverlayInner {
		inner = maxOverlayInner
	}
	if inner < 40 {
		inner = 40
	}
	return inner
}
