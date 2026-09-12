package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestConfirmModalDefaultSelectionIsHonoured(t *testing.T) {
	tests := []struct {
		name           string
		defaultAction  confirmModalAction
		expectedButton string
	}{
		{
			name:           "cancel default",
			defaultAction:  confirmModalCancel,
			expectedButton: "No",
		},
		{
			name:           "confirm default",
			defaultAction:  confirmModalConfirm,
			expectedButton: "Yes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := confirmModalSpec{
				Title:         "test",
				Heading:       "Proceed?",
				Body:          "This is a test",
				CancelLabel:   "No",
				ConfirmLabel:  "Yes",
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
		CancelLabel:   "No",
		ConfirmLabel:  "Yes",
		DefaultAction: confirmModalCancel,
	}
	s := openConfirmModal(80, 24, spec)

	// Move right from cancel should go to confirm
	s = s.moveSelection(1)
	if s.selected != confirmModalConfirm {
		t.Errorf("after moveSelection(1) from cancel: selected = %d, want %d", s.selected, confirmModalConfirm)
	}

	// Move right from confirm should wrap to cancel
	s = s.moveSelection(1)
	if s.selected != confirmModalCancel {
		t.Errorf("after moveSelection(1) from confirm: selected = %d, want %d", s.selected, confirmModalCancel)
	}

	// Move left from cancel should wrap to confirm
	s = s.moveSelection(-1)
	if s.selected != confirmModalConfirm {
		t.Errorf("after moveSelection(-1) from cancel: selected = %d, want %d", s.selected, confirmModalConfirm)
	}

	// Move left from confirm should go to cancel
	s = s.moveSelection(-1)
	if s.selected != confirmModalCancel {
		t.Errorf("after moveSelection(-1) from confirm: selected = %d, want %d", s.selected, confirmModalCancel)
	}
}

func TestConfirmModalSelectedActionReturnsCurrentSelection(t *testing.T) {
	spec := confirmModalSpec{
		Title:         "test",
		Heading:       "Proceed?",
		Body:          "This is a test",
		CancelLabel:   "No",
		ConfirmLabel:  "Yes",
		DefaultAction: confirmModalCancel,
	}
	s := openConfirmModal(80, 24, spec)

	if s.selectedAction() != confirmModalCancel {
		t.Errorf("selectedAction() = %d, want %d", s.selectedAction(), confirmModalCancel)
	}

	s = s.moveSelection(1)
	if s.selectedAction() != confirmModalConfirm {
		t.Errorf("after moveSelection, selectedAction() = %d, want %d", s.selectedAction(), confirmModalConfirm)
	}
}

func TestConfirmModalEnterOnCancelSelection(t *testing.T) {
	spec := confirmModalSpec{
		Title:         "test",
		Heading:       "Proceed?",
		Body:          "This is a test",
		CancelLabel:   "No",
		ConfirmLabel:  "Yes",
		DefaultAction: confirmModalCancel,
	}
	s := openConfirmModal(80, 24, spec)

	s, result := s.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result != confirmModalResultCancelled {
		t.Errorf("result = %d, want %d (confirmModalResultCancelled)", result, confirmModalResultCancelled)
	}
	if s.IsOpen() {
		t.Errorf("after enter, modal is still open, want closed")
	}
}

func TestConfirmModalEnterOnConfirmSelection(t *testing.T) {
	spec := confirmModalSpec{
		Title:         "test",
		Heading:       "Proceed?",
		Body:          "This is a test",
		CancelLabel:   "No",
		ConfirmLabel:  "Yes",
		DefaultAction: confirmModalConfirm,
	}
	s := openConfirmModal(80, 24, spec)

	s, result := s.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result != confirmModalResultConfirmed {
		t.Errorf("result = %d, want %d (confirmModalResultConfirmed)", result, confirmModalResultConfirmed)
	}
	if s.IsOpen() {
		t.Errorf("after enter, modal is still open, want closed")
	}
}

func TestConfirmModalEscClosesAndCancels(t *testing.T) {
	spec := confirmModalSpec{
		Title:         "test",
		Heading:       "Proceed?",
		Body:          "This is a test",
		CancelLabel:   "No",
		ConfirmLabel:  "Yes",
		DefaultAction: confirmModalConfirm,
	}
	s := openConfirmModal(80, 24, spec)

	// Even with confirm selected, Esc should cancel
	s, result := s.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if result != confirmModalResultCancelled {
		t.Errorf("result = %d, want %d (confirmModalResultCancelled)", result, confirmModalResultCancelled)
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
		CancelLabel:   "No",
		ConfirmLabel:  "Yes",
		DefaultAction: confirmModalConfirm,
	}
	s := openConfirmModal(80, 24, spec)
	s, result := s.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})

	if s.selected != confirmModalCancel {
		t.Errorf("selected = %d, want %d", s.selected, confirmModalCancel)
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
		CancelLabel:   "No",
		ConfirmLabel:  "Yes",
		DefaultAction: confirmModalConfirm,
	}
	s := openConfirmModal(80, 24, spec)
	s, result := s.handleKey(tea.KeyPressMsg{Code: tea.KeyUp})

	if s.selected != confirmModalCancel {
		t.Errorf("selected = %d, want %d", s.selected, confirmModalCancel)
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
		CancelLabel:   "No",
		ConfirmLabel:  "Yes",
		DefaultAction: confirmModalCancel,
	}
	s := openConfirmModal(80, 24, spec)
	s, result := s.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})

	if s.selected != confirmModalConfirm {
		t.Errorf("selected = %d, want %d", s.selected, confirmModalConfirm)
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
		CancelLabel:   "No",
		ConfirmLabel:  "Yes",
		DefaultAction: confirmModalCancel,
	}
	s := openConfirmModal(80, 24, spec)
	s, result := s.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})

	if s.selected != confirmModalConfirm {
		t.Errorf("selected = %d, want %d", s.selected, confirmModalConfirm)
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
		CancelLabel:   "No",
		ConfirmLabel:  "Yes",
		DefaultAction: confirmModalCancel,
	}
	s := openConfirmModal(80, 24, spec)
	s, result := s.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})

	if s.selected != confirmModalConfirm {
		t.Errorf("selected = %d, want %d", s.selected, confirmModalConfirm)
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
		CancelLabel:   "No",
		ConfirmLabel:  "Yes",
		DefaultAction: confirmModalCancel,
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
		CancelLabel:   "No",
		ConfirmLabel:  "Yes",
		DefaultAction: confirmModalCancel,
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
		CancelLabel:   "No",
		ConfirmLabel:  "Yes",
		DefaultAction: confirmModalCancel,
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
