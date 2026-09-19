package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/session"
)

func TestSessionActionsRefusedWhileBusy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		want string
		run  func(m *Model)
	}{
		{"compact", "cannot compact while a run is in progress", func(m *Model) { m.executeCompactAction(inputAction{}) }},
		{"fork", "cannot fork while a run is in progress", func(m *Model) { m.executeForkSessionAction() }},
		{"session picker open", "cannot switch sessions while a run is in progress", func(m *Model) { m.executeRequestSessionPickerAction() }},
		{"session picker slash", "cannot switch sessions while a run is in progress", func(m *Model) { m.openSessionPickerFromSlashCommand() }},
		{"session picker enter", "cannot switch sessions while a run is in progress", func(m *Model) {
			m.sessionPicker = m.sessionPicker.Open([]session.IndexEntry{{ID: "s1", Title: "t"}})
			m.handleSessionPickerKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			if m.sessionPicker.IsOpen() {
				t.Error("session picker still open after busy refusal")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := &testController{}
			m := newModel(Config{}, nil)
			m.controller = ctrl
			m.content.AppendLine("some conversation")
			m.activity = m.activity.waiting("running", "model")

			tt.run(m)

			if got := m.content.String(m.viewport.Width()); !strings.Contains(got, tt.want) {
				t.Errorf("content = %q, want %q", got, tt.want)
			}
			ctrl.mu.Lock()
			defer ctrl.mu.Unlock()
			if len(ctrl.actions) != 0 {
				t.Errorf("controller received actions %+v while busy", ctrl.actions)
			}
		})
	}
}

func TestCompactAllowedWhenIdle(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}
	m := newModel(Config{}, nil)
	m.controller = ctrl
	m.executeCompactAction(inputAction{})
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	if len(ctrl.actions) != 1 {
		t.Fatalf("actions = %+v, want one compaction action", ctrl.actions)
	}
}
