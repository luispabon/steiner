package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestIsCtrlRequiresCtrlAlone(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		msg  tea.KeyPressMsg
		want bool
	}{
		{name: "ctrl+c", msg: tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, want: true},
		{name: "ctrl+c with caps lock", msg: tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl | tea.ModCapsLock}, want: true},
		{name: "ctrl+c with num lock", msg: tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl | tea.ModNumLock}, want: true},
		{name: "ctrl+shift+c", msg: tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl | tea.ModShift}, want: false},
		{name: "ctrl+alt+c", msg: tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl | tea.ModAlt}, want: false},
		{name: "plain c", msg: tea.KeyPressMsg{Code: 'c'}, want: false},
		{name: "ctrl+d", msg: tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isCtrl(tc.msg, 'c'); got != tc.want {
				t.Errorf("isCtrl(%s, 'c') = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestCtrlShiftCDoesNotQuit pins that a terminal passing ctrl+shift+c through
// (kitty keyboard protocol) does not trigger the ctrl+c quit path.
func TestCtrlShiftCDoesNotQuit(t *testing.T) {
	t.Parallel()
	quits := func(msg tea.KeyPressMsg) bool {
		m := newModel(Config{}, nil)
		m = updateModelDirect(m, tea.WindowSizeMsg{Width: 100, Height: 30})
		_, cmd := m.Update(msg)
		if cmd == nil {
			return false
		}
		_, ok := cmd().(tea.QuitMsg)
		return ok
	}
	if !quits(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}) {
		t.Fatal("ctrl+c no longer quits an idle session without a controller; test premise broken")
	}
	if quits(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl | tea.ModShift}) {
		t.Error("ctrl+shift+c quit the session")
	}
}
