package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
)

func TestConfirmModalDefaultSelectionIsHonoured(t *testing.T) {
	tests := []struct {
		name           string
		defaultAction  confirmModalAction
		expectedButton string
	}{
		{
			name:           "left default",
			defaultAction:  confirmModalLeft,
			expectedButton: "No",
		},
		{
			name:           "right default",
			defaultAction:  confirmModalRight,
			expectedButton: "Yes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := confirmModalSpec{
				Title:         "test",
				Heading:       "Proceed?",
				Body:          "This is a test",
				LeftLabel:     "No",
				RightLabel:    "Yes",
				DefaultAction: tt.defaultAction,
			}
			s := openConfirmModal(80, 24, spec)
			if s.selected != tt.defaultAction {
				t.Errorf("selected = %d, want %d", s.selected, tt.defaultAction)
			}
		})
	}
}

func TestConfirmModalMoveSelectionWraps(t *testing.T) {
	spec := confirmModalSpec{
		Title:         "test",
		Heading:       "Proceed?",
		Body:          "This is a test",
		LeftLabel:     "No",
		RightLabel:    "Yes",
		DefaultAction: confirmModalLeft,
	}
	s := openConfirmModal(80, 24, spec)

	// Move right from left should go to right
	s = s.moveSelection(1)
	if s.selected != confirmModalRight {
		t.Errorf("after moveSelection(1) from left: selected = %d, want %d", s.selected, confirmModalRight)
	}

	// Move right from right should wrap to left
	s = s.moveSelection(1)
	if s.selected != confirmModalLeft {
		t.Errorf("after moveSelection(1) from right: selected = %d, want %d", s.selected, confirmModalLeft)
	}

	// Move left from left should wrap to right
	s = s.moveSelection(-1)
	if s.selected != confirmModalRight {
		t.Errorf("after moveSelection(-1) from left: selected = %d, want %d", s.selected, confirmModalRight)
	}

	// Move left from right should go to left
	s = s.moveSelection(-1)
	if s.selected != confirmModalLeft {
		t.Errorf("after moveSelection(-1) from right: selected = %d, want %d", s.selected, confirmModalLeft)
	}
}

func TestConfirmModalSelectedActionReturnsCurrentSelection(t *testing.T) {
	spec := confirmModalSpec{
		Title:         "test",
		Heading:       "Proceed?",
		Body:          "This is a test",
		LeftLabel:     "No",
		RightLabel:    "Yes",
		DefaultAction: confirmModalLeft,
	}
	s := openConfirmModal(80, 24, spec)

	if s.selectedAction() != confirmModalLeft {
		t.Errorf("selectedAction() = %d, want %d", s.selectedAction(), confirmModalLeft)
	}

	s = s.moveSelection(1)
	if s.selectedAction() != confirmModalRight {
		t.Errorf("after moveSelection, selectedAction() = %d, want %d", s.selectedAction(), confirmModalRight)
	}
}

func TestConfirmModalEnterOnLeftSelection(t *testing.T) {
	spec := confirmModalSpec{
		Title:         "test",
		Heading:       "Proceed?",
		Body:          "This is a test",
		LeftLabel:     "No",
		RightLabel:    "Yes",
		DefaultAction: confirmModalLeft,
	}
	s := openConfirmModal(80, 24, spec)

	s, result := s.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result != confirmModalResultChosen {
		t.Errorf("result = %d, want %d (confirmModalResultChosen)", result, confirmModalResultChosen)
	}
	if !s.IsOpen() {
		t.Errorf("after enter, modal is closed, want open")
	}
	if s.selectedAction() != confirmModalLeft {
		t.Errorf("selectedAction() = %d, want %d (confirmModalLeft)", s.selectedAction(), confirmModalLeft)
	}
}

func TestConfirmModalEnterOnRightSelection(t *testing.T) {
	spec := confirmModalSpec{
		Title:         "test",
		Heading:       "Proceed?",
		Body:          "This is a test",
		LeftLabel:     "No",
		RightLabel:    "Yes",
		DefaultAction: confirmModalRight,
	}
	s := openConfirmModal(80, 24, spec)

	s, result := s.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result != confirmModalResultChosen {
		t.Errorf("result = %d, want %d (confirmModalResultChosen)", result, confirmModalResultChosen)
	}
	if !s.IsOpen() {
		t.Errorf("after enter, modal is closed, want open")
	}
	if s.selectedAction() != confirmModalRight {
		t.Errorf("selectedAction() = %d, want %d (confirmModalRight)", s.selectedAction(), confirmModalRight)
	}
}

func TestConfirmModalEscClosesAndDismisses(t *testing.T) {
	spec := confirmModalSpec{
		Title:         "test",
		Heading:       "Proceed?",
		Body:          "This is a test",
		LeftLabel:     "No",
		RightLabel:    "Yes",
		DefaultAction: confirmModalRight,
	}
	s := openConfirmModal(80, 24, spec)

	// Even with right selected, Esc should dismiss
	s, result := s.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if result != confirmModalResultDismissed {
		t.Errorf("result = %d, want %d (confirmModalResultDismissed)", result, confirmModalResultDismissed)
	}
	if s.IsOpen() {
		t.Errorf("after esc, modal is still open, want closed")
	}
}

func TestConfirmModalKeyNavigationLeft(t *testing.T) {
	spec := confirmModalSpec{
		Title:         "test",
		Heading:       "Proceed?",
		Body:          "This is a test",
		LeftLabel:     "No",
		RightLabel:    "Yes",
		DefaultAction: confirmModalRight,
	}
	s := openConfirmModal(80, 24, spec)
	s, result := s.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})

	if s.selected != confirmModalLeft {
		t.Errorf("selected = %d, want %d", s.selected, confirmModalLeft)
	}
	if result != confirmModalResultPending {
		t.Errorf("result = %d, want %d (confirmModalResultPending)", result, confirmModalResultPending)
	}
	if !s.IsOpen() {
		t.Errorf("after navigation key, modal is closed, want open")
	}
}

func TestConfirmModalKeyNavigationUp(t *testing.T) {
	spec := confirmModalSpec{
		Title:         "test",
		Heading:       "Proceed?",
		Body:          "This is a test",
		LeftLabel:     "No",
		RightLabel:    "Yes",
		DefaultAction: confirmModalRight,
	}
	s := openConfirmModal(80, 24, spec)
	s, result := s.handleKey(tea.KeyPressMsg{Code: tea.KeyUp})

	if s.selected != confirmModalLeft {
		t.Errorf("selected = %d, want %d", s.selected, confirmModalLeft)
	}
	if result != confirmModalResultPending {
		t.Errorf("result = %d, want %d (confirmModalResultPending)", result, confirmModalResultPending)
	}
	if !s.IsOpen() {
		t.Errorf("after navigation key, modal is closed, want open")
	}
}

func TestConfirmModalKeyNavigationRight(t *testing.T) {
	spec := confirmModalSpec{
		Title:         "test",
		Heading:       "Proceed?",
		Body:          "This is a test",
		LeftLabel:     "No",
		RightLabel:    "Yes",
		DefaultAction: confirmModalLeft,
	}
	s := openConfirmModal(80, 24, spec)
	s, result := s.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})

	if s.selected != confirmModalRight {
		t.Errorf("selected = %d, want %d", s.selected, confirmModalRight)
	}
	if result != confirmModalResultPending {
		t.Errorf("result = %d, want %d (confirmModalResultPending)", result, confirmModalResultPending)
	}
	if !s.IsOpen() {
		t.Errorf("after navigation key, modal is closed, want open")
	}
}

func TestConfirmModalKeyNavigationDown(t *testing.T) {
	spec := confirmModalSpec{
		Title:         "test",
		Heading:       "Proceed?",
		Body:          "This is a test",
		LeftLabel:     "No",
		RightLabel:    "Yes",
		DefaultAction: confirmModalLeft,
	}
	s := openConfirmModal(80, 24, spec)
	s, result := s.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})

	if s.selected != confirmModalRight {
		t.Errorf("selected = %d, want %d", s.selected, confirmModalRight)
	}
	if result != confirmModalResultPending {
		t.Errorf("result = %d, want %d (confirmModalResultPending)", result, confirmModalResultPending)
	}
	if !s.IsOpen() {
		t.Errorf("after navigation key, modal is closed, want open")
	}
}

func TestConfirmModalKeyNavigationTab(t *testing.T) {
	spec := confirmModalSpec{
		Title:         "test",
		Heading:       "Proceed?",
		Body:          "This is a test",
		LeftLabel:     "No",
		RightLabel:    "Yes",
		DefaultAction: confirmModalLeft,
	}
	s := openConfirmModal(80, 24, spec)
	s, result := s.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})

	if s.selected != confirmModalRight {
		t.Errorf("selected = %d, want %d", s.selected, confirmModalRight)
	}
	if result != confirmModalResultPending {
		t.Errorf("result = %d, want %d (confirmModalResultPending)", result, confirmModalResultPending)
	}
	if !s.IsOpen() {
		t.Errorf("after navigation key, modal is closed, want open")
	}
}

func TestConfirmModalRenderContainsAllContent(t *testing.T) {
	useTrueColor(t)
	spec := confirmModalSpec{
		Title:         "test",
		Heading:       "Switch modes?",
		Body:          "This will change the configuration.",
		LeftLabel:     "No",
		RightLabel:    "Yes",
		DefaultAction: confirmModalLeft,
	}
	s := openConfirmModal(80, 24, spec)

	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	rendered := stripANSI(s.render(m.styles))

	for _, want := range []string{"Switch modes?", "This will change the configuration.", "Yes", "No", "confirm"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered modal = %q, missing %q", rendered, want)
		}
	}
}

func TestConfirmModalRenderAtNarrowWidth(t *testing.T) {
	useTrueColor(t)
	spec := confirmModalSpec{
		Title:         "test",
		Heading:       "Proceed?",
		Body:          "This is a test",
		LeftLabel:     "No",
		RightLabel:    "Yes",
		DefaultAction: confirmModalLeft,
	}
	s := openConfirmModal(50, 24, spec)

	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 50, Height: 24})

	rendered := s.render(m.styles)
	if rendered == "" {
		t.Errorf("rendered modal is empty at narrow width")
	}
	if !strings.Contains(stripANSI(rendered), "Proceed?") {
		t.Errorf("rendered modal at narrow width missing heading: %q", stripANSI(rendered))
	}
}

func TestConfirmModalCloseMarksAsClosed(t *testing.T) {
	spec := confirmModalSpec{
		Title:         "test",
		Heading:       "Proceed?",
		Body:          "This is a test",
		LeftLabel:     "No",
		RightLabel:    "Yes",
		DefaultAction: confirmModalLeft,
	}
	s := openConfirmModal(80, 24, spec)
	if !s.IsOpen() {
		t.Errorf("newly opened modal is not open")
	}

	s = s.close()
	if s.IsOpen() {
		t.Errorf("closed modal is still open")
	}
}

func TestConfirmModalUnrelatedKeyReturnsPending(t *testing.T) {
	spec := confirmModalSpec{
		Title:         "test",
		Heading:       "Proceed?",
		Body:          "This is a test",
		LeftLabel:     "No",
		RightLabel:    "Yes",
		DefaultAction: confirmModalLeft,
	}
	s := openConfirmModal(80, 24, spec)

	s, result := s.handleKey(tea.KeyPressMsg{Text: "a"})
	if result != confirmModalResultPending {
		t.Errorf("result = %d, want %d (confirmModalResultPending)", result, confirmModalResultPending)
	}
	if !s.IsOpen() {
		t.Errorf("after unrelated key, modal is closed, want open")
	}
}

func TestConfirmModalsFollowResize(t *testing.T) {
	tests := []struct {
		name string
		open func(m *Model)
	}{
		{
			name: "worktree cleanup",
			open: func(m *Model) {
				m.openWorktreeCleanupModal(m.width, m.height, 1)
			},
		},
		{
			name: "exit",
			open: func(m *Model) {
				m.openExitModal()
			},
		},
		{
			name: "orchestration",
			open: func(m *Model) {
				m.requestOrchestrationLevel(config.OrchestrationLevelLow)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useTrueColor(t)
			ctrl := &conversationTestController{conversation: []agent.Message{{Role: "user"}}}
			m := newModel(Config{Controller: ctrl, SubAgentsEnabled: true, OrchestrationLevel: "standard"}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

			tt.open(m)

			before := m.openConfirmModalView()
			if before == nil {
				t.Fatal("no confirm modal open after opener")
			}
			widthBefore := before.overlayWidth()

			m = updateModel(t, m, tea.WindowSizeMsg{Width: 50, Height: 24})

			after := m.openConfirmModalView()
			if after == nil {
				t.Fatal("no confirm modal open after resize")
			}
			widthAfter := after.overlayWidth()
			if widthAfter == widthBefore {
				t.Fatalf("overlay width unchanged after resize: %d", widthAfter)
			}

			rendered := stripANSI(after.render(m.styles))
			for _, line := range strings.Split(rendered, "\n") {
				if got := lipgloss.Width(line); got != widthAfter {
					t.Errorf("line width = %d, want %d (line %q)", got, widthAfter, line)
				}
			}
		})
	}
}

func TestConfirmModalViewPriority(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.openWorktreeCleanupModal(m.width, m.height, 1)
	m.openExitModal()

	s := m.openConfirmModalView()
	if s == nil {
		t.Fatal("no confirm modal open")
	}
	if s != &m.worktreeCleanupModal {
		t.Fatal("view priority did not select worktreeCleanupModal over exitModal")
	}
}

func TestConfirmModalCtrlCNotSpecial(t *testing.T) {
	spec := confirmModalSpec{
		Title:         "test",
		Heading:       "Proceed?",
		Body:          "This is a test",
		LeftLabel:     "No",
		RightLabel:    "Yes",
		DefaultAction: confirmModalLeft,
	}
	s := openConfirmModal(80, 24, spec)

	s, result := s.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if result != confirmModalResultPending {
		t.Errorf("result = %d, want %d (confirmModalResultPending)", result, confirmModalResultPending)
	}
	if !s.IsOpen() {
		t.Errorf("after ctrl+c, modal is closed, want open")
	}
}
