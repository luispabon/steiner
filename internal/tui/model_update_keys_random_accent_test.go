package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/tui/prefs"
)

func newRandomAccentTestModel(t *testing.T) *Model {
	t.Helper()
	m := newModel(Config{
		Model:             "small",
		CurrentModelAlias: "small",
		ModelNames:        []string{"small", "large"},
		ModelContexts:     map[string]int{"small": 4096},
		AccentPreset:      "amber",
	}, nil)
	return updateModel(t, m, tea.WindowSizeMsg{Width: 110, Height: 40})
}

// pressAltR routes alt+r through the normal key path and returns the model and
// the command the key produced.
func pressAltR(t *testing.T, m *Model) (*Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.handleKeyMsg(altKey('r'))
	updated, ok := next.(*Model)
	if !ok {
		t.Fatalf("handleKeyMsg returned %T, want *Model", next)
	}
	return updated, cmd
}

// TestAltRAppliesRandomAccentAndKeepsComposer proves alt+r feeds the existing
// random accent message path, rebuilds the styles, persists "random" and
// leaves the draft and cursor untouched on every press.
func TestAltRAppliesRandomAccentAndKeepsComposer(t *testing.T) {
	// Not parallel: the persisted preference is read back through prefs, which
	// shares the package test HOME with other tests.
	m := newRandomAccentTestModel(t)
	m.input.SetValue("draft text")
	m.input.SetCursorColumn(3)
	before := m.styles

	for press := 1; press <= 2; press++ {
		var cmd tea.Cmd
		m, cmd = pressAltR(t, m)
		if cmd == nil {
			t.Fatalf("press %d: alt+r returned no command", press)
		}
		msg := cmd()
		accentMsg, ok := msg.(setAccentMsg)
		if !ok {
			t.Fatalf("press %d: alt+r produced %T, want setAccentMsg", press, msg)
		}
		if accentMsg.preset != "random" {
			t.Fatalf("press %d: preset = %q, want random", press, accentMsg.preset)
		}

		m = updateModel(t, m, accentMsg)

		if got := m.accentPreset; got != "random" {
			t.Errorf("press %d: accentPreset = %q, want random", press, got)
		}
		if m.styles == before {
			t.Errorf("press %d: styles were not rebuilt after the accent change", press)
		}
		before = m.styles
		if got := m.input.Value(); got != "draft text" {
			t.Errorf("press %d: draft = %q, want %q", press, got, "draft text")
		}
		if got := m.input.Column(); got != 3 {
			t.Errorf("press %d: cursor column = %d, want 3", press, got)
		}
	}

	loaded, err := prefs.Load()
	if err != nil {
		t.Fatalf("prefs.Load: %v", err)
	}
	if loaded.Accent != "random" {
		t.Errorf("persisted accent = %q, want random", loaded.Accent)
	}
}

// TestAltRIgnoredByHelpOverlayAndApproval ensures the shortcut follows the
// existing routing: help, open overlays and approval consume the key without
// applying an accent.
func TestAltRIgnoredByHelpOverlayAndApproval(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		setup func(m *Model)
	}{
		{name: "help visible", setup: func(m *Model) { m.helpVisible = true }},
		{name: "approval active", setup: func(m *Model) {
			m.approval = approvalState{active: true, tool: "write", mode: "prompt", identity: "call-1"}
		}},
		{name: "model picker open", setup: func(m *Model) {
			m.modelPicker = m.modelPicker.Open(m.modelNames, m.primaryModel)
		}},
		{name: "accent picker open", setup: func(m *Model) {
			m.accentPicker = m.accentPicker.Open(m.accentPreset)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newRandomAccentTestModel(t)
			m.input.SetValue("draft text")
			m.input.SetCursorColumn(3)
			before := m.styles
			tc.setup(m)

			m, cmd := pressAltR(t, m)
			if cmd != nil {
				t.Fatal("alt+r produced a command while blocked")
			}
			if got := m.accentPreset; got != "amber" {
				t.Errorf("accentPreset = %q, want unchanged amber", got)
			}
			if m.styles != before {
				t.Error("styles changed while the shortcut was blocked")
			}
			if got := m.input.Value(); got != "draft text" {
				t.Errorf("draft = %q, want unchanged %q", got, "draft text")
			}
		})
	}
}

func TestHelpListsRandomAccentKeybind(t *testing.T) {
	t.Parallel()
	m := newRandomAccentTestModel(t)
	m.helpVisible = true

	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "alt+r") {
		t.Fatal("help overlay does not list alt+r")
	}
	if !strings.Contains(view, "random accent") {
		t.Fatal("help overlay does not describe alt+r as random accent")
	}
}
