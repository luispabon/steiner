package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/interactive"
)

func (c *testController) switchModeActions() []interactive.SwitchMode {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []interactive.SwitchMode
	for _, a := range c.actions {
		if v, ok := a.(interactive.SwitchMode); ok {
			result = append(result, v)
		}
	}
	return result
}

func TestShiftTabTogglesMode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		fromMode string
		want     config.ExecutionMode
	}{
		{"build to plan", "build", config.ExecutionModePlan},
		{"plan to build", "plan", config.ExecutionModeBuild},
		{"unset defaults to plan", "", config.ExecutionModePlan},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := &testController{}
			m := newModel(Config{Controller: ctrl, InitialMode: tc.fromMode}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

			_ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})

			actions := ctrl.switchModeActions()
			if len(actions) != 1 {
				t.Fatalf("SwitchMode action count = %d, want 1", len(actions))
			}
			if actions[0].Mode != tc.want {
				t.Errorf("SwitchMode.Mode = %q, want %q", actions[0].Mode, tc.want)
			}
		})
	}
}

func TestShiftTabPreservesInput(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}
	m := newModel(Config{Controller: ctrl, InitialMode: "build"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.input.SetValue("hello world")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})

	if got := m.input.Value(); got != "hello world" {
		t.Errorf("input value = %q, want hello world", got)
	}
	actions := ctrl.switchModeActions()
	if len(actions) != 1 {
		t.Fatalf("SwitchMode action count = %d, want 1", len(actions))
	}
	if actions[0].Mode != config.ExecutionModePlan {
		t.Errorf("SwitchMode.Mode = %q, want %q", actions[0].Mode, config.ExecutionModePlan)
	}
}

func TestShiftTabSuppressedWhenOverlayOpen(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}
	m := newModel(Config{Controller: ctrl, InitialMode: "build"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.modelPicker = m.modelPicker.Open(m.modelNames, m.primaryModel)

	_ = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})

	if len(ctrl.switchModeActions()) != 0 {
		t.Fatalf("SwitchMode dispatched while overlay open, want no-op")
	}
}

func TestModeSlashCommandParsing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		value       string
		wantToggle  bool
		wantSetMode string
		wantInvalid string
	}{
		{name: "no arg toggles", value: "/mode", wantToggle: true},
		{name: "plan arg sets plan", value: "/mode plan", wantSetMode: "plan"},
		{name: "build arg sets build", value: "/mode build", wantSetMode: "build"},
		{name: "invalid arg reported", value: "/mode bogus", wantInvalid: "bogus"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			action := parseInput(tc.value)
			if action.toggleMode != tc.wantToggle {
				t.Errorf("toggleMode = %v, want %v", action.toggleMode, tc.wantToggle)
			}
			if action.setMode != tc.wantSetMode {
				t.Errorf("setMode = %q, want %q", action.setMode, tc.wantSetMode)
			}
			if action.invalidMode != tc.wantInvalid {
				t.Errorf("invalidMode = %q, want %q", action.invalidMode, tc.wantInvalid)
			}
		})
	}
}

func TestModeSlashCommandDispatch(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}
	m := newModel(Config{Controller: ctrl, InitialMode: "build"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("/mode plan")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	actions := ctrl.switchModeActions()
	if len(actions) != 1 {
		t.Fatalf("SwitchMode action count = %d, want 1", len(actions))
	}
	if actions[0].Mode != config.ExecutionModePlan {
		t.Errorf("SwitchMode.Mode = %q, want %q", actions[0].Mode, config.ExecutionModePlan)
	}
}

func TestModeSlashCommandNoArgClearsInput(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}
	m := newModel(Config{Controller: ctrl, InitialMode: "build"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("/mode")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if got := m.input.Value(); got != "" {
		t.Errorf("input value = %q, want empty after argument-less /mode", got)
	}
	actions := ctrl.switchModeActions()
	if len(actions) != 1 {
		t.Fatalf("SwitchMode action count = %d, want 1", len(actions))
	}
	if actions[0].Mode != config.ExecutionModePlan {
		t.Errorf("SwitchMode.Mode = %q, want %q", actions[0].Mode, config.ExecutionModePlan)
	}
}

func TestModeSlashCommandInvalidArgReportsStatus(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}
	m := newModel(Config{Controller: ctrl, InitialMode: "build"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("/mode bogus")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if len(ctrl.switchModeActions()) != 0 {
		t.Fatalf("SwitchMode dispatched for invalid arg, want no-op")
	}
	if len(m.content.segments) != 1 {
		t.Fatalf("content segments = %d, want 1", len(m.content.segments))
	}
	seg := m.content.segments[0]
	want := "mode \"bogus\" is not valid (use plan: restricted edits, plan artifacts only; or build: normal workspace editing)"
	if seg.kind != segmentStatus {
		t.Fatalf("segment kind = %v, want segmentStatus", seg.kind)
	}
	if seg.text != want {
		t.Fatalf("segment text = %q, want %q", seg.text, want)
	}
}
