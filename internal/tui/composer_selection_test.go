package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestComposerSelectionKeysDoNotSelect pins that the textarea's keyboard
// selection stays unreachable: the composer renders its own lines, so a
// selection would be invisible and the next keystroke would replace it.
func TestComposerSelectionKeysDoNotSelect(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{name: "shift+left", key: tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift}},
		{name: "shift+right", key: tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift}},
		{name: "ctrl+shift+left", key: tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModCtrl | tea.ModShift}},
		{name: "ctrl+shift+right", key: tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl | tea.ModShift}},
		{name: "alt+shift+left", key: tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt | tea.ModShift}},
		{name: "alt+shift+b", key: tea.KeyPressMsg{Code: 'b', Mod: tea.ModAlt | tea.ModShift}},
		{name: "shift+up", key: tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift}},
		{name: "shift+down", key: tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			m = updateModelDirect(m, tea.WindowSizeMsg{Width: 100, Height: 30})
			m.input.SetValue("alpha beta\ngamma delta")
			// Move left into the draft so forward selections have text to cover.
			m = updateModelDirect(m, tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModCtrl})
			before := m.input.Value()

			m = updateModelDirect(m, tc.key)
			if m.input.HasSelection() {
				t.Fatalf("%s started a textarea selection", tc.name)
			}
			m = updateModelDirect(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
			if got, removed := m.input.Value(), len(before)-len(m.input.Value()); removed > 1 {
				t.Errorf("%s then backspace removed %d bytes: %q -> %q", tc.name, removed, before, got)
			}
		})
	}
}
