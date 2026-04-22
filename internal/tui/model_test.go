package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/output"
)

func TestModelAppliesRuntimeEvents(t *testing.T) {
	m := newModel(Config{Model: "gpt-test"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(1, "hello")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(1, " world")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextBudgetEvent("project_context", 1, 100, 256, false)})

	if got := m.content.String(); !strings.Contains(got, "assistant> hello world") {
		t.Fatalf("content = %q, want assistant stream", got)
	}
	if got := m.status.model; got != "gpt-test" {
		t.Fatalf("status.model = %q, want gpt-test", got)
	}
	if got := m.status.context; got != "ctx 100/256" {
		t.Fatalf("status.context = %q, want ctx 100/256", got)
	}
}

func TestModelSubmitsInputAndTogglesSkills(t *testing.T) {
	var submitted []string
	var toggled []string

	m := newModel(Config{
		SkillNames: []string{"review"},
		OnSubmit: func(value string) {
			submitted = append(submitted, value)
		},
		OnSkillToggle: func(name string, enabled bool) {
			toggled = append(toggled, name)
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("fix the bug")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(submitted) != 1 || submitted[0] != "fix the bug" {
		t.Fatalf("submitted = %#v, want input callback", submitted)
	}

	m.input.SetValue("/skill review")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.enabledSkills["review"] {
		t.Fatal("expected review skill to be enabled")
	}
	if len(toggled) != 1 || toggled[0] != "review" {
		t.Fatalf("toggled = %#v, want review", toggled)
	}
}

func TestModelApprovalModeTransitions(t *testing.T) {
	var approved []bool

	m := newModel(Config{
		OnApproval: func(value bool) {
			approved = append(approved, value)
		},
	}, nil)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "write", "prompt", `{"path":"note.txt"}`)})
	if !m.approval.active {
		t.Fatal("expected approval mode to be active")
	}

	m.input.SetValue("yes")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.approval.active {
		t.Fatal("expected approval mode to clear after decision")
	}
	if len(approved) != 1 || !approved[0] {
		t.Fatalf("approved = %#v, want accepted response", approved)
	}
}

func TestModelResizeAndMouseScroll(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 40, Height: 6})
	for i := 0; i < 20; i++ {
		m.content.AppendLine("line")
	}
	m.syncViewport()
	m.viewport.GotoBottom()
	start := m.viewport.YOffset

	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	if m.viewport.YOffset >= start {
		t.Fatalf("yOffset = %d, want less than %d after wheel up", m.viewport.YOffset, start)
	}
	if m.autoScroll {
		t.Fatal("expected autoScroll to disable after upward scroll")
	}

	m = updateModel(t, m, tea.WindowSizeMsg{Width: 60, Height: 12})
	if m.viewport.Width != 60 {
		t.Fatalf("viewport width = %d, want 60", m.viewport.Width)
	}
	if m.viewport.Height != 10 {
		t.Fatalf("viewport height = %d, want 10", m.viewport.Height)
	}
}

func updateModel(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()

	next, _ := m.Update(msg)
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	return updated
}
