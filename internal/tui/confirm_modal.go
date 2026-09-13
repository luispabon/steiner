package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// confirmModalAction identifies a button position. The component knows only layout;
// callers assign meaning.
type confirmModalAction int

const (
	confirmModalLeft  confirmModalAction = iota // left button, pinned at column 0
	confirmModalRight                           // right button, right-aligned
)

type confirmModalResult int

const (
	confirmModalResultPending   confirmModalResult = iota // modal still open
	confirmModalResultChosen                              // enter: modal still open, read selectedAction(); caller closes
	confirmModalResultDismissed                           // esc: modal closed, nothing chosen
)

// confirmModalSpec configures a confirmModalState.
type confirmModalSpec struct {
	Title         string // overlay shell title, e.g. "confirm"
	Heading       string // accent bold line, e.g. "Proceed?"
	Body          string // muted body text
	LeftLabel     string // left button
	RightLabel    string // right button
	DefaultAction confirmModalAction
}

type confirmModalState struct {
	OverlayShell
	spec     confirmModalSpec
	selected confirmModalAction
}

func openConfirmModal(width, height int, spec confirmModalSpec) confirmModalState {
	shell := OverlayShell{}.WithPreferredWidth(60)
	shell = shell.WithDimensions(width, height).WithTitle(spec.Title).openShell()
	return confirmModalState{
		OverlayShell: shell,
		spec:         spec,
		selected:     spec.DefaultAction,
	}
}

func (s confirmModalState) close() confirmModalState {
	s.OverlayShell = s.closeShell()
	return s
}

func (s confirmModalState) moveSelection(delta int) confirmModalState {
	const actions = 2
	s.selected = confirmModalAction(((int(s.selected)+delta)%actions + actions) % actions)
	return s
}

func (s confirmModalState) selectedAction() confirmModalAction {
	return s.selected
}

func (s confirmModalState) render(styles *theme.Styles) string {
	contentWidth := s.InnerWidth()

	heading := lipgloss.NewStyle().
		Foreground(styles.AccentColor).
		Bold(true).
		Width(contentWidth).
		Render(s.spec.Heading)
	body := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.FgMute)).
		Width(contentWidth).
		Render(s.spec.Body)

	leftButton := renderConfirmModalButton(styles, s.spec.LeftLabel, s.selected == confirmModalLeft)
	rightButton := renderConfirmModalButton(styles, s.spec.RightLabel, s.selected == confirmModalRight)
	buttonRow := strings.Repeat(" ", contentWidth)
	buttonRow = composeOverlayLine(buttonRow, leftButton, contentWidth, 0, lipgloss.Width(leftButton))
	buttonRow = composeOverlayLine(buttonRow, rightButton, contentWidth, contentWidth-lipgloss.Width(rightButton), lipgloss.Width(rightButton))

	divider := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.BorderSoft)).
		Render(strings.Repeat("─", contentWidth))
	footerText := FooterChip("tab/←→") + " move   " + FooterChip("enter") + " confirm   " + FooterChip("esc") + " cancel"
	footer := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.FgMute)).
		Width(contentWidth).
		Render(footerText)

	content := lipgloss.JoinVertical(lipgloss.Left,
		heading,
		"",
		body,
		"",
		buttonRow,
		divider,
		footer,
	)
	return s.RenderWithBg(styles.PaletteOverlay, content, theme.BgElev)
}

func renderConfirmModalButton(styles *theme.Styles, label string, selected bool) string {
	if selected {
		return styles.AccentBg.Padding(0, 2).Render(label)
	}
	return lipgloss.NewStyle().
		Background(lipgloss.Color(theme.BgElev2)).
		Foreground(lipgloss.Color(theme.Fg)).
		Padding(0, 2).
		Render(label)
}

// confirmModals lists every confirm modal on the Model in view priority order.
// Register new confirm modals here.
func (m *Model) confirmModals() []*confirmModalState {
	return []*confirmModalState{&m.worktreeCleanupModal, &m.exitModal, &m.orchestrationConfirm}
}

// openConfirmModalView returns the highest-priority open confirm modal, or nil.
func (m *Model) openConfirmModalView() *confirmModalState {
	for _, s := range m.confirmModals() {
		if s.IsOpen() {
			return s
		}
	}
	return nil
}

func (m *Model) anyConfirmModalOpen() bool {
	return m.openConfirmModalView() != nil
}

// handleKey processes input. On Chosen the modal stays open; the caller must close it.
func (s confirmModalState) handleKey(msg tea.KeyPressMsg) (confirmModalState, confirmModalResult) {
	switch msg.Code {
	case tea.KeyLeft, tea.KeyUp:
		return s.moveSelection(-1), confirmModalResultPending
	case tea.KeyRight, tea.KeyDown, tea.KeyTab:
		return s.moveSelection(1), confirmModalResultPending
	case tea.KeyEnter:
		return s, confirmModalResultChosen
	case tea.KeyEsc:
		return s.close(), confirmModalResultDismissed
	}
	return s, confirmModalResultPending
}
