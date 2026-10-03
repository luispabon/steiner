package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func pickerModel(t *testing.T) (*Model, map[string]occurrenceKey) {
	t.Helper()
	m := newJumpTestModel(t)
	keys := map[string]occurrenceKey{}
	for i, id := range []string{"aaa", "bbb", "ccc"} {
		if i > 0 {
			m.content.AppendLine("filler")
		}
		k := addJumpCard(m, i+1, id, "")
		dd := m.content.delegations[k].dd
		dd.agentID, dd.agentType, dd.toolLabel, dd.status = id, "explore", "explore", "complete"
		dd.startTime, dd.taskPreview = int64(i+1), "task "+id
		keys[id] = k
	}
	addJumpFiller(m, 60)
	m.syncViewport()
	return m, keys
}

func selectedID(m *Model) string {
	k, ok := m.subAgentPicker.SelectedKey()
	if !ok {
		return ""
	}
	return k.CallID
}

func TestSubAgentPickerOpenClose(t *testing.T) {
	cases := []struct {
		name  string
		open  func(m *Model)
		close tea.KeyPressMsg
	}{
		{"alt+a then esc", func(m *Model) { updateModel(t, m, altKey('a')) }, tea.KeyPressMsg{Code: tea.KeyEsc}},
		{"alt+a then alt+a", func(m *Model) { updateModel(t, m, altKey('a')) }, altKey('a')},
		{"slash command then esc", func(m *Model) { m.executeRequestSubAgentPickerAction() }, tea.KeyPressMsg{Code: tea.KeyEsc}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := pickerModel(t)
			tc.open(m)
			if !m.subAgentPicker.IsOpen() {
				t.Fatal("picker not open")
			}
			_ = m.View()
			updateModel(t, m, tc.close)
			if m.subAgentPicker.IsOpen() {
				t.Error("picker still open")
			}
		})
	}
}

func TestSubAgentPickerParseSlashCommand(t *testing.T) {
	if !parseInput("/agents").requestSubAgentPicker {
		t.Error("/agents did not set requestSubAgentPicker")
	}
}

func TestSubAgentPickerNavigationAndJump(t *testing.T) {
	m, keys := pickerModel(t)
	updateModel(t, m, altKey('a'))
	// newest first: ccc, bbb, aaa
	if got := selectedID(m); got != "ccc" {
		t.Fatalf("initial selection = %q, want ccc", got)
	}
	updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if got := selectedID(m); got != "bbb" {
		t.Errorf("after down = %q, want bbb", got)
	}
	updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if got := selectedID(m); got != "ccc" {
		t.Errorf("up past top = %q, want ccc (headings are not selectable)", got)
	}
	updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.subAgentPicker.IsOpen() {
		t.Error("picker still open after enter")
	}
	if m.jumpTarget != keys["bbb"] {
		t.Errorf("jumpTarget = %+v, want %+v", m.jumpTarget, keys["bbb"])
	}
}

func TestSubAgentPickerFilterTyping(t *testing.T) {
	m, _ := pickerModel(t)
	updateModel(t, m, altKey('a'))
	for _, r := range "bb" {
		updateModel(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if m.subAgentPicker.query != "bb" || selectedID(m) != "bbb" {
		t.Errorf("query=%q selected=%q", m.subAgentPicker.query, selectedID(m))
	}
	updateModel(t, m, tea.KeyPressMsg{Code: 'z', Text: "z"})
	if _, ok := m.subAgentPicker.SelectedKey(); ok {
		t.Error("selection on empty result")
	}
	before := m.jumpTarget
	updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.subAgentPicker.IsOpen() || m.jumpTarget != before {
		t.Error("enter with no card selected must do nothing")
	}
	updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if selectedID(m) != "bbb" {
		t.Errorf("after backspace selected=%q", selectedID(m))
	}
}

func TestSubAgentPickerViewBounds(t *testing.T) {
	for _, width := range []int{12, 20, 40, 100} {
		m, _ := pickerModel(t)
		m = updateModelDirect(m, tea.WindowSizeMsg{Width: width, Height: 24})
		updateModel(t, m, altKey('a'))
		view := m.subAgentPicker.View()
		for _, line := range strings.Split(view, "\n") {
			if w := ansi.StringWidth(line); w > m.subAgentPicker.width {
				t.Fatalf("width %d: line width %d exceeds overlay width %d", width, w, m.subAgentPicker.width)
			}
		}
		_ = m.View()
	}
}

func TestSubAgentPickerRefreshMovesFinishedCard(t *testing.T) {
	m, keys := pickerModel(t)
	dd := m.content.delegations[keys["aaa"]].dd
	dd.status = "active"
	m.openSubAgentPicker()
	for selectedID(m) != "aaa" {
		before := m.subAgentPicker.selection
		m.subAgentPicker.selection = m.subAgentPicker.nextSelectable(before, 1)
		if m.subAgentPicker.selection == before {
			t.Fatal("active card row not found")
		}
	}
	if m.subAgentPicker.totalRunning != 1 {
		t.Fatalf("totalRunning = %d, want 1", m.subAgentPicker.totalRunning)
	}

	dd.status = "complete"
	m.refreshSubAgentPicker()

	for _, r := range m.subAgentPicker.rows {
		if r.section && r.heading == "RUNNING" {
			t.Error("RUNNING section still present after the card completed")
		}
	}
	if got := selectedID(m); got != "aaa" {
		t.Errorf("selected = %q, want aaa", got)
	}
	if run, done := m.subAgentPicker.totalRunning, m.subAgentPicker.totalDone; run != 0 || done != 3 {
		t.Errorf("totals = %d running / %d done, want 0 / 3", run, done)
	}
}
