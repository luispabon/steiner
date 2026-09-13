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

func TestConfirmModalHandleKey(t *testing.T) {
	tests := []struct {
		name          string
		defaultAction confirmModalAction
		key           tea.KeyPressMsg
		wantResult    confirmModalResult
		wantSelected  confirmModalAction
		wantOpen      bool
	}{
		{name: "left moves from right", defaultAction: confirmModalRight, key: tea.KeyPressMsg{Code: tea.KeyLeft}, wantResult: confirmModalResultPending, wantSelected: confirmModalLeft, wantOpen: true},
		{name: "up moves from right", defaultAction: confirmModalRight, key: tea.KeyPressMsg{Code: tea.KeyUp}, wantResult: confirmModalResultPending, wantSelected: confirmModalLeft, wantOpen: true},
		{name: "left wraps from left", defaultAction: confirmModalLeft, key: tea.KeyPressMsg{Code: tea.KeyLeft}, wantResult: confirmModalResultPending, wantSelected: confirmModalRight, wantOpen: true},
		{name: "right moves from left", defaultAction: confirmModalLeft, key: tea.KeyPressMsg{Code: tea.KeyRight}, wantResult: confirmModalResultPending, wantSelected: confirmModalRight, wantOpen: true},
		{name: "down moves from left", defaultAction: confirmModalLeft, key: tea.KeyPressMsg{Code: tea.KeyDown}, wantResult: confirmModalResultPending, wantSelected: confirmModalRight, wantOpen: true},
		{name: "tab moves from left", defaultAction: confirmModalLeft, key: tea.KeyPressMsg{Code: tea.KeyTab}, wantResult: confirmModalResultPending, wantSelected: confirmModalRight, wantOpen: true},
		{name: "tab wraps from right", defaultAction: confirmModalRight, key: tea.KeyPressMsg{Code: tea.KeyTab}, wantResult: confirmModalResultPending, wantSelected: confirmModalLeft, wantOpen: true},
		{name: "enter on left chooses and stays open", defaultAction: confirmModalLeft, key: tea.KeyPressMsg{Code: tea.KeyEnter}, wantResult: confirmModalResultChosen, wantSelected: confirmModalLeft, wantOpen: true},
		{name: "enter on right chooses and stays open", defaultAction: confirmModalRight, key: tea.KeyPressMsg{Code: tea.KeyEnter}, wantResult: confirmModalResultChosen, wantSelected: confirmModalRight, wantOpen: true},
		{name: "esc dismisses and closes", defaultAction: confirmModalRight, key: tea.KeyPressMsg{Code: tea.KeyEsc}, wantResult: confirmModalResultDismissed, wantSelected: confirmModalRight, wantOpen: false},
		{name: "unrelated key is ignored", defaultAction: confirmModalLeft, key: tea.KeyPressMsg{Text: "a"}, wantResult: confirmModalResultPending, wantSelected: confirmModalLeft, wantOpen: true},
		{name: "ctrl+c is not special", defaultAction: confirmModalLeft, key: tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, wantResult: confirmModalResultPending, wantSelected: confirmModalLeft, wantOpen: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openConfirmModal(80, 24, confirmModalSpec{
				Title:         "test",
				Heading:       "Proceed?",
				Body:          "This is a test",
				LeftLabel:     "No",
				RightLabel:    "Yes",
				DefaultAction: tt.defaultAction,
			})

			s, result := s.handleKey(tt.key)
			if result != tt.wantResult {
				t.Errorf("result = %d, want %d", result, tt.wantResult)
			}
			if got := s.selectedAction(); got != tt.wantSelected {
				t.Errorf("selectedAction() = %d, want %d", got, tt.wantSelected)
			}
			if got := s.IsOpen(); got != tt.wantOpen {
				t.Errorf("IsOpen() = %v, want %v", got, tt.wantOpen)
			}
		})
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
