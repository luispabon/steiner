package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/output"
)

func TestModelAppliesRuntimeEvents(t *testing.T) {
	m := newModel(Config{
		Model:         "gpt-test",
		ModelContexts: map[string]int{"gpt-test": 4096},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(1, "hello")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(1, " world")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextTokenBudgetEvent("conversation", 1, 100, 32, 32, 164, 4096, false)})

	if got := m.content.String(m.viewport.Width); !strings.Contains(got, "hello world") {
		t.Fatalf("content = %q, want assistant stream", got)
	}
	if got := m.status.model; got != "gpt-test" {
		t.Fatalf("status.model = %q, want gpt-test", got)
	}
	if got := m.status.context; got != "ctx 100/4096" {
		t.Fatalf("status.context = %q, want ctx 100/4096", got)
	}
	if got := m.sidebar.contextBudget; got != 4096 {
		t.Fatalf("sidebar.contextBudget = %d, want 4096", got)
	}
	if got := m.sidebar.promptUsed; got != 100 {
		t.Fatalf("sidebar.promptUsed = %d, want 100", got)
	}
	if got := m.sidebar.budgetUsed; got != 164 {
		t.Fatalf("sidebar.budgetUsed = %d, want 164", got)
	}
	lines := m.sidebar.lines(38, 50)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"● auto @ 90%",
		"CONTEXT",
		"100 / 4096",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("sidebar = %q, want %q", joined, want)
		}
	}
	if strings.Contains(joined, "Budget") {
		t.Fatalf("sidebar = %q, want no Budget row", joined)
	}
	if strings.Contains(joined, "Prompt") {
		t.Fatalf("sidebar = %q, want no Prompt header", joined)
	}
	if strings.Contains(joined, "Turn:") {
		t.Fatalf("sidebar = %q, want no Turn row", joined)
	}
}

func TestModelIgnoresByteBudgetForSidebarContextFill(t *testing.T) {
	m := newModel(Config{
		Model:         "gemma4",
		ModelContexts: map[string]int{"gemma4": 65536},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextBudgetEvent("project_context", 1, 2000, 2000, true)})

	if got := m.sidebar.contextBudget; got != 65536 {
		t.Fatalf("sidebar.contextBudget = %d, want 65536", got)
	}
	if got := m.sidebar.promptUsed; got != 0 {
		t.Fatalf("sidebar.promptUsed = %d, want 0", got)
	}
	if got := m.sidebar.budgetUsed; got != 0 {
		t.Fatalf("sidebar.budgetUsed = %d, want 0", got)
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
	if !m.enabledSkills["review"] {
		t.Fatal("expected configured skills to start enabled")
	}

	m.input.SetValue("fix the bug")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(submitted) != 1 || submitted[0] != "fix the bug" {
		t.Fatalf("submitted = %#v, want input callback", submitted)
	}

	m.input.SetValue("/skill review")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.enabledSkills["review"] {
		t.Fatal("expected review skill to be disabled")
	}
	if len(toggled) != 1 || toggled[0] != "review" {
		t.Fatalf("toggled = %#v, want review", toggled)
	}

	m.input.SetValue("/skill +review")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.enabledSkills["review"] {
		t.Fatal("expected review skill to be enabled")
	}
}

func TestModelModifiedEnterInsertsNewline(t *testing.T) {
	var submitted []string

	m := newModel(Config{
		OnSubmit: func(value string) {
			submitted = append(submitted, value)
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("first line")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter, Alt: true})

	if len(submitted) != 0 {
		t.Fatalf("submitted = %#v, want no submission for modified enter", submitted)
	}
	if got := m.input.Value(); got != "first line\n" {
		t.Fatalf("input value = %q, want newline inserted", got)
	}
}

func TestModelPlainEnterStillSubmitsPrompt(t *testing.T) {
	var submitted []string

	m := newModel(Config{
		OnSubmit: func(value string) {
			submitted = append(submitted, value)
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("fix the bug")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if len(submitted) != 1 || submitted[0] != "fix the bug" {
		t.Fatalf("submitted = %#v, want plain enter submission", submitted)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("input value = %q, want reset after submit", got)
	}
}

func TestModelHandlesContextCommandLocally(t *testing.T) {
	var submitted []string
	contextInspections := 0

	m := newModel(Config{
		OnSubmit: func(value string) {
			submitted = append(submitted, value)
		},
		OnContextInspect: func() {
			contextInspections++
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("/context")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if len(submitted) != 0 {
		t.Fatalf("submitted = %#v, want no provider submission", submitted)
	}
	if contextInspections != 1 {
		t.Fatalf("contextInspections = %d, want 1", contextInspections)
	}
	if got := strings.TrimSpace(m.content.String(m.viewport.Width)); got != "" {
		t.Fatalf("content = %q, want no local echo", got)
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

func TestModelApprovalEnterAllowedWhileStreaming(t *testing.T) {
	var approved []bool

	m := newModel(Config{
		OnApproval: func(value bool) {
			approved = append(approved, value)
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(1, "streaming")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "write", "prompt", `{"path":"note.txt"}`)})

	m.input.SetValue("yes")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if len(approved) != 1 || !approved[0] {
		t.Fatalf("approved = %#v, want accepted response while streaming", approved)
	}
	if m.approval.active {
		t.Fatal("expected approval mode to clear after decision")
	}
}

func TestModelEscInterruptsStreaming(t *testing.T) {
	interrupts := 0

	m := newModel(Config{
		OnInterrupt: func() {
			interrupts++
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(1, "streaming")})
	m.input.SetValue("stale")

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	if interrupts != 1 {
		t.Fatalf("interrupts = %d, want 1", interrupts)
	}
	if m.content.streamingPhase != "" {
		t.Fatalf("streamingPhase = %q, want empty", m.content.streamingPhase)
	}
	if m.input.Value() != "" {
		t.Fatalf("input value = %q, want reset", m.input.Value())
	}
	if m.input.Placeholder != "ask steiner — / for commands, @ for files" {
		t.Fatalf("input placeholder = %q, want default", m.input.Placeholder)
	}
	if !strings.Contains(m.content.String(m.viewport.Width), "interrupted") {
		t.Fatal("expected interrupted marker in content")
	}
}

func TestModelCtrlCInterruptsStreamingInsteadOfQuitting(t *testing.T) {
	interrupts := 0

	m := newModel(Config{
		OnInterrupt: func() {
			interrupts++
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(1, "streaming")})

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}

	if interrupts != 1 {
		t.Fatalf("interrupts = %d, want 1", interrupts)
	}
	if cmd != nil {
		t.Fatal("expected no quit command while streaming")
	}
	if updated.content.streamingPhase != "" {
		t.Fatalf("streamingPhase = %q, want empty", updated.content.streamingPhase)
	}
	if updated.input.Value() != "" {
		t.Fatalf("input value = %q, want reset", updated.input.Value())
	}
	if updated.input.Placeholder != "ask steiner — / for commands, @ for files" {
		t.Fatalf("input placeholder = %q, want default", updated.input.Placeholder)
	}
}

func TestModelStreamingBlocksNonApprovalPromptInput(t *testing.T) {
	var submitted []string

	m := newModel(Config{
		OnSubmit: func(value string) {
			submitted = append(submitted, value)
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(1, "streaming")})
	m.input.SetValue("should stay blocked")

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if len(submitted) != 0 {
		t.Fatalf("submitted = %#v, want no submission while streaming", submitted)
	}
	if m.input.Value() != "should stay blocked" {
		t.Fatalf("input value = %q, want unchanged", m.input.Value())
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
	// ContentPane: PaddingLeft(3)+PaddingRight(3) → viewport.Width = 60-6 = 54
	// Layout rows: top_pad(1) + viewport + hDivider(1) + input(3, with focus border) + status(1) → viewport.Height = 12-6 = 6
	if m.viewport.Width != 54 {
		t.Fatalf("viewport width = %d, want 54 after pane chrome", m.viewport.Width)
	}
	if m.viewport.Height != 6 {
		t.Fatalf("viewport height = %d, want 6 after pane chrome", m.viewport.Height)
	}
}

func TestModelListFilesOpensOverlayWithWorkingDir(t *testing.T) {
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.input.SetValue("/ls")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.fileList.open {
		t.Fatal("expected file list overlay to open after /ls")
	}
	if m.fileList.root != "." {
		t.Fatalf("file list root = %q, want .", m.fileList.root)
	}
	if len(m.fileList.entries) == 0 {
		t.Fatal("expected non-empty file list")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.fileList.open {
		t.Fatal("expected file list overlay to close after Esc")
	}
}

func TestModelListFilesOpensWithPath(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.input.SetValue("/ls .")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.fileList.open {
		t.Fatal("expected file list overlay to open after /ls .")
	}
	if len(m.fileList.entries) == 0 {
		t.Fatal("expected non-empty file list for .")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.fileList.open {
		t.Fatal("expected file list overlay to close after Enter")
	}
}

func TestModelFilePickerOverlayInView(t *testing.T) {
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	if !m.filePicker.open {
		t.Fatal("expected file picker to open after @")
	}

	view := m.View()
	// The file picker should appear in the view (not be hidden)
	if !strings.Contains(view, "@") {
		t.Fatal("expected file picker content in View()")
	}
	// The divider, input, and status should still be visible
	if !strings.Contains(view, "─") {
		t.Fatal("expected divider in View()")
	}
	if !strings.Contains(view, "›") {
		t.Fatal("expected input prompt in View()")
	}
}

func TestContentBufferReflowsMarkdownForViewportWidth(t *testing.T) {
	var content contentBuffer
	content.appendMarkdownBlock("## Title\n\nThis is a long markdown paragraph that should wrap differently when the content pane width changes.")

	narrow := content.String(28)
	wide := content.String(72)

	if !strings.Contains(narrow, "Title") {
		t.Fatalf("narrow render = %q, want markdown content", narrow)
	}
	if !strings.Contains(wide, "Title") {
		t.Fatalf("wide render = %q, want markdown content", wide)
	}
	if narrow == wide {
		t.Fatalf("markdown render did not change across widths:\n%s", narrow)
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
