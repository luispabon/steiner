package tui

import (
	"errors"
	"os"
	"path/filepath"
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

	// Simulate the context report arriving as an event (as interactive.go would emit).
	reportContent := "# Last Request Context\nModel: `test`"
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextReportEvent(reportContent)})

	// Overlay should open immediately with report content, not transcript.
	if !m.contextOverlay.open {
		t.Fatal("contextOverlay.open = false, want overlay open after context report event")
	}
	if !strings.Contains(m.contextOverlay.content, "Last Request Context") {
		t.Fatalf("contextOverlay.content = %q, want report content", m.contextOverlay.content)
	}
	if got := strings.TrimSpace(m.content.String(m.viewport.Width)); got != "" {
		t.Fatalf("content = %q, want no transcript insertion for context report", got)
	}

	// Esc should close the overlay.
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.contextOverlay.open {
		t.Fatal("contextOverlay.open = true, want overlay closed after Esc")
	}
}

func TestModelHandlesConfigCommandLocally(t *testing.T) {
	var submitted []string
	configInspections := 0

	m := newModel(Config{
		OnSubmit: func(value string) {
			submitted = append(submitted, value)
		},
		OnConfigInspect: func() {
			configInspections++
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("/config")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if len(submitted) != 0 {
		t.Fatalf("submitted = %#v, want no provider submission", submitted)
	}
	if configInspections != 1 {
		t.Fatalf("configInspections = %d, want 1", configInspections)
	}
	if got := strings.TrimSpace(m.content.String(m.viewport.Width)); got != "" {
		t.Fatalf("content = %q, want no local echo", got)
	}

	reportContent := "```yaml\nmodel:\n  model: gpt-test\n```"
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewConfigReportEvent(reportContent)})

	if !m.contextOverlay.open {
		t.Fatal("contextOverlay.open = false, want overlay open after config report event")
	}
	if got := m.contextOverlay.title; got != "Config" {
		t.Fatalf("contextOverlay.title = %q, want Config", got)
	}
	if !strings.Contains(m.contextOverlay.content, "model:") {
		t.Fatalf("contextOverlay.content = %q, want yaml content", m.contextOverlay.content)
	}
	if got := strings.TrimSpace(m.content.String(m.viewport.Width)); got != "" {
		t.Fatalf("content = %q, want no transcript insertion for config report", got)
	}
}

func TestModelDisplaysFileEventInTranscript(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	preview := output.FormatFilePreviewWithLimit("snippet.go", `package main
func main() {}
`, 10)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewDisplayFileEvent(output.DisplayFilePayload{
		Path:    "snippet.go",
		Preview: preview,
	})})

	if m.contextOverlay.open {
		t.Fatal("contextOverlay.open = true, want no overlay for display_file")
	}
	content := m.content.String(m.viewport.Width)
	for _, want := range []string{"display file preview", "snippet.go", "package main"} {
		if !strings.Contains(content, want) {
			t.Fatalf("content = %q, want %q", content, want)
		}
	}
	if strings.Contains(m.View(), "file viewer") {
		t.Fatalf("view = %q, want no file viewer overlay", m.View())
	}
}

func TestModelSwitchUpdatesProviderHost(t *testing.T) {
	m := newModel(Config{
		Model:           "small",
		ModelContexts:   map[string]int{"small": 1024, "large": 8192},
		ProviderBaseURL: "http://small.example/v1",
		OnModelSwitch: func(name string) (string, bool) {
			if name != "large" {
				return "", false
			}
			return "http://large.example/v1", true
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("/model large")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if got, want := m.status.model, "large"; got != want {
		t.Fatalf("status.model = %q, want %q", got, want)
	}
	if got, want := m.sidebar.model, "large"; got != want {
		t.Fatalf("sidebar.model = %q, want %q", got, want)
	}
	if got, want := m.sidebar.provider, "http://large.example/v1"; got != want {
		t.Fatalf("sidebar.provider = %q, want %q", got, want)
	}
	if got, want := m.sidebar.contextBudget, 8192; got != want {
		t.Fatalf("sidebar.contextBudget = %d, want %d", got, want)
	}
}

func TestModelStartupSnapshotPopulatesSidebarModifiedFiles(t *testing.T) {
	repo := initTUITestRepo(t)
	writeRepoFile(t, repo, "tracked.txt", "one\n")
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-m", "init")
	writeRepoFile(t, repo, "scratch.txt", "draft\n")

	m := newModel(Config{WorkingDir: repo}, nil)

	if !m.sidebar.dirty {
		t.Fatal("sidebar.dirty = false, want true")
	}
	if got, want := len(m.sidebar.modifiedFiles), 1; got != want {
		t.Fatalf("len(sidebar.modifiedFiles) = %d, want %d", got, want)
	}
	if got, want := m.sidebar.modifiedFiles[0].Path, "scratch.txt"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if got, want := m.sidebar.modifiedFiles[0].Status, "U"; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
}

func TestModelRefreshesGitSnapshotAfterToolAndTurnFinishedEvents(t *testing.T) {
	repo := initTUITestRepo(t)
	writeRepoFile(t, repo, "tracked.txt", "one\n")
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-m", "init")

	m := newModel(Config{WorkingDir: repo}, nil)
	if m.sidebar.dirty {
		t.Fatal("sidebar.dirty = true, want clean repo")
	}

	writeRepoFile(t, repo, "tracked.txt", "one\ntwo\n")
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallFinishedEvent(1, "write", "call_1", `{"path":"tracked.txt"}`, nil)})

	if !m.sidebar.dirty {
		t.Fatal("sidebar.dirty = false after tool event, want true")
	}
	if got, want := len(m.sidebar.modifiedFiles), 1; got != want {
		t.Fatalf("len(sidebar.modifiedFiles) after tool event = %d, want %d", got, want)
	}
	if got, want := m.sidebar.modifiedFiles[0].Path, "tracked.txt"; got != want {
		t.Fatalf("path after tool event = %q, want %q", got, want)
	}

	writeRepoFile(t, repo, "turn.txt", "draft\n")
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewTurnFinishedEvent(1, 1, "stop", "reply", nil)})

	if got, want := len(m.sidebar.modifiedFiles), 2; got != want {
		t.Fatalf("len(sidebar.modifiedFiles) after turn event = %d, want %d", got, want)
	}
	paths := map[string]string{}
	for _, file := range m.sidebar.modifiedFiles {
		paths[file.Path] = file.Status
	}
	if got, want := paths["tracked.txt"], "M"; got != want {
		t.Fatalf("tracked.txt status = %q, want %q", got, want)
	}
	if got, want := paths["turn.txt"], "U"; got != want {
		t.Fatalf("turn.txt status = %q, want %q", got, want)
	}
}

func TestModelTickConsumesOnlyItsOwnGitError(t *testing.T) {
	m1 := newModel(Config{}, nil)
	m2 := newModel(Config{}, nil)

	errOne := errors.New("first model git error")
	errTwo := errors.New("second model git error")
	m1.git.recordError(errOne)
	m2.git.recordError(errTwo)

	m1 = updateModel(t, m1, tickMsg{})

	if got := m1.git.takeError(); got != nil {
		t.Fatalf("first model pending git error = %v, want nil after its tick", got)
	}
	if got := m2.git.takeError(); !errors.Is(got, errTwo) {
		t.Fatalf("second model pending git error = %v, want %v", got, errTwo)
	}
}

func TestModelApprovalModeTransitions(t *testing.T) {
	approved := make(chan ApprovalSubmission, 1)

	m := newModel(Config{
		OnApproval: func(submission ApprovalSubmission) {
			approved <- submission
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
	select {
	case submission := <-approved:
		if got, want := submission.Decision, ApprovalDecisionAllowOnce; got != want {
			t.Fatalf("submission.Decision = %q, want %q", got, want)
		}
		if got, want := submission.Tool, "write"; got != want {
			t.Fatalf("submission.Tool = %q, want %q", got, want)
		}
		if got, want := submission.Mode, "prompt"; got != want {
			t.Fatalf("submission.Mode = %q, want %q", got, want)
		}
	default:
		t.Fatal("expected approval submission")
	}
}

func TestModelApprovalEnterAllowedWhileStreaming(t *testing.T) {
	approved := make(chan ApprovalSubmission, 1)

	m := newModel(Config{
		OnApproval: func(submission ApprovalSubmission) {
			approved <- submission
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(1, "streaming")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "write", "prompt", `{"path":"note.txt"}`)})

	m.input.SetValue("yes")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	select {
	case submission := <-approved:
		if got, want := submission.Decision, ApprovalDecisionAllowOnce; got != want {
			t.Fatalf("submission.Decision = %q, want %q", got, want)
		}
	default:
		t.Fatal("expected approval submission while streaming")
	}
	if m.approval.active {
		t.Fatal("expected approval mode to clear after decision")
	}
}

func TestModelApprovalSelectionAndConfirmation(t *testing.T) {
	approved := make(chan ApprovalSubmission, 1)

	m := newModel(Config{
		OnApproval: func(submission ApprovalSubmission) {
			approved <- submission
		},
	}, nil)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "bash", "prompt", `{"command":"pwd"}`)})

	if got, want := m.approval.selectedAction, 0; got != want {
		t.Fatalf("selectedAction = %d, want %d", got, want)
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if got, want := m.approval.selectedAction, 1; got != want {
		t.Fatalf("selectedAction after tab = %d, want %d", got, want)
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	select {
	case submission := <-approved:
		if got, want := submission.Decision, ApprovalDecisionAlwaysAllow; got != want {
			t.Fatalf("submission.Decision = %q, want %q", got, want)
		}
		if got, want := submission.Tool, "bash"; got != want {
			t.Fatalf("submission.Tool = %q, want %q", got, want)
		}
	default:
		t.Fatal("expected approval submission")
	}
	if m.approval.active {
		t.Fatal("expected approval mode to clear after decision")
	}
}

func TestModelApprovalEscDenies(t *testing.T) {
	approved := make(chan ApprovalSubmission, 1)

	m := newModel(Config{
		OnApproval: func(submission ApprovalSubmission) {
			approved <- submission
		},
	}, nil)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "write", "prompt", `{"path":"note.txt"}`)})
	m.input.SetValue("stale text")

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	select {
	case submission := <-approved:
		if got, want := submission.Decision, ApprovalDecisionDeny; got != want {
			t.Fatalf("submission.Decision = %q, want %q", got, want)
		}
	default:
		t.Fatal("expected denial submission")
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("input value = %q, want reset after denial", got)
	}
	if m.approval.active {
		t.Fatal("expected approval mode to clear after esc denial")
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

func TestModelIdleCtrlCOpensExitModalInsteadOfQuitting(t *testing.T) {
	exitRequests := 0
	interrupts := 0

	m := newModel(Config{
		OnInterrupt: func() {
			interrupts++
		},
		OnExitRequested: func() {
			exitRequests++
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	// Idle state: Ctrl+C should fire OnExitRequested, not quit.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}

	if exitRequests != 0 {
		t.Fatalf("exitRequests = %d, want 0 before confirmation", exitRequests)
	}
	if interrupts != 0 {
		t.Fatalf("interrupts = %d, want 0 (idle, not active run)", interrupts)
	}
	if !updated.exitModal.open {
		t.Fatal("exitModal.open = false, want modal open")
	}
	if cmd != nil {
		t.Fatal("expected no quit command when opening exit modal")
	}
}

func TestModelIdleCtrlDOpensExitModalInsteadOfQuitting(t *testing.T) {
	exitRequests := 0

	m := newModel(Config{
		OnExitRequested: func() {
			exitRequests++
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}

	if exitRequests != 0 {
		t.Fatalf("exitRequests = %d, want 0 before confirmation", exitRequests)
	}
	if !updated.exitModal.open {
		t.Fatal("exitModal.open = false, want modal open")
	}
	if cmd != nil {
		t.Fatal("expected no quit command when opening exit modal")
	}
}

func TestModelIdleCtrlCQuitsWhenNoCallbackSet(t *testing.T) {
	// When OnExitRequested is not wired (e.g. non-interactive mode),
	// idle Ctrl+C falls back to immediate quit.
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd when no OnExitRequested callback")
	}
}

func TestModelExitModalCancelClosesWithoutExiting(t *testing.T) {
	exitRequests := 0

	m := newModel(Config{
		OnExitRequested: func() {
			exitRequests++
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.exitModal.open {
		t.Fatal("exitModal.open = false, want modal open")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	updated := m

	if updated.exitModal.open {
		t.Fatal("exitModal.open = true, want modal closed")
	}
	if exitRequests != 0 {
		t.Fatalf("exitRequests = %d, want 0", exitRequests)
	}
}

func TestModelExitModalExitRequestsQuit(t *testing.T) {
	exitRequests := 0

	m := newModel(Config{
		OnExitRequested: func() {
			exitRequests++
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.exitModal.open {
		t.Fatal("exitModal.open = false, want modal open")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	updated := m

	if exitRequests != 1 {
		t.Fatalf("exitRequests = %d, want 1", exitRequests)
	}
	if !updated.exitModal.open {
		t.Fatal("exitModal.open = false, want modal to remain open until runtime quits")
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

func TestModelEscInterruptsActiveRunWithoutStreamingChunks(t *testing.T) {
	interrupts := 0

	m := newModel(Config{
		OnInterrupt: func() {
			interrupts++
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})

	if m.content.streamingPhase != "" {
		t.Fatalf("streamingPhase = %q, want empty while waiting for model", m.content.streamingPhase)
	}
	if got, want := m.status.mode, "running"; got != want {
		t.Fatalf("status.mode = %q, want %q", got, want)
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	if interrupts != 1 {
		t.Fatalf("interrupts = %d, want 1", interrupts)
	}
	if got := m.status.mode; got != "" {
		t.Fatalf("status.mode = %q, want cleared after interrupt", got)
	}
}

func TestModelEscInterruptsToolPhase(t *testing.T) {
	interrupts := 0

	m := newModel(Config{
		OnInterrupt: func() {
			interrupts++
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "bash", "call_1", map[string]any{"command": "git log --oneline -10"})})

	if got := m.content.streamingPhase; got != "tool" {
		t.Fatalf("streamingPhase = %q, want tool before interrupt", got)
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	if interrupts != 1 {
		t.Fatalf("interrupts = %d, want 1", interrupts)
	}
	if got := m.content.streamingPhase; got != "" {
		t.Fatalf("streamingPhase = %q, want empty after interrupt", got)
	}
	if got := m.input.Placeholder; got != "ask steiner — / for commands, @ for files" {
		t.Fatalf("input placeholder = %q, want default after interrupt", got)
	}
	if m.status.streaming {
		t.Fatal("status.streaming = true, want false after interrupt")
	}
}

func TestModelInterruptSuppressesStaleRunEventsUntilRunFinished(t *testing.T) {
	interrupts := 0

	m := newModel(Config{
		OnInterrupt: func() {
			interrupts++
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "bash", "call_1", map[string]any{"command": "git status"})})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalAcceptedEvent(1, "bash", "prompt", `{"command":"git status"}`, "approved")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(1, "still streaming")})

	if interrupts != 1 {
		t.Fatalf("interrupts = %d, want 1", interrupts)
	}
	if got := m.input.Placeholder; got != "ask steiner — / for commands, @ for files" {
		t.Fatalf("input placeholder = %q, want default while interrupted", got)
	}
	if m.status.streaming {
		t.Fatal("status.streaming = true, want false while interrupted")
	}
	if got := m.content.streamingPhase; got != "" {
		t.Fatalf("streamingPhase = %q, want empty while interrupted", got)
	}
	if strings.Contains(m.content.String(m.viewport.Width), "running tool") {
		t.Fatal("expected stale tool activity to be suppressed after interrupt")
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewStopReasonEvent(1, "cancelled", nil)})
	if got, want := m.status.mode, "cancelled"; got != want {
		t.Fatalf("status.mode = %q, want %q after stop", got, want)
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunFinishedEvent(1, "cancelled", "", "", nil)})
	if !strings.Contains(m.content.String(m.viewport.Width), "status: cancelled") {
		t.Fatal("expected cancelled stop reason to remain visible")
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(2, "fresh run")})
	if got := m.content.streamingPhase; got != "answer" {
		t.Fatalf("streamingPhase = %q, want answer after next run resumes", got)
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

func TestModelAltEnterInsertsNewline(t *testing.T) {
	var submitted []string

	m := newModel(Config{
		OnSubmit: func(value string) {
			submitted = append(submitted, value)
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m.input.SetValue("first line")

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter, Alt: true})

	if got := m.input.Value(); got != "first line\n" {
		t.Fatalf("input value = %q, want newline inserted", got)
	}
	if len(submitted) != 0 {
		t.Fatalf("submitted = %#v, want no submit on modified enter", submitted)
	}
}

func TestModelShiftEnterInsertsNewline(t *testing.T) {
	var submitted []string

	m := newModel(Config{
		OnSubmit: func(value string) {
			submitted = append(submitted, value)
		},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m.input.SetValue("first line")

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyShiftEnter})

	if got := m.input.Value(); got != "first line\n" {
		t.Fatalf("input value = %q, want newline inserted", got)
	}
	if len(submitted) != 0 {
		t.Fatalf("submitted = %#v, want no submit on shift+enter", submitted)
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
	// Layout rows: top_pad(1) + viewport + hDivider(1) + input(3, with padding 1) + status(1) → viewport.Height = 12-6 = 6
	if m.viewport.Width != 54 {
		t.Fatalf("viewport width = %d, want 54 after pane chrome", m.viewport.Width)
	}
	if got := m.input.Width(); got != 56 {
		t.Fatalf("input width = %d, want 56 after rail, padding, and tail fill", got)
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
	if !strings.Contains(view, "ask steiner") {
		t.Fatal("expected composer placeholder in View()")
	}
	if !strings.Contains(view, "┃") {
		t.Fatal("expected accented composer border in View()")
	}
}

func TestModelRenderInputLinesUsesLocalCursor(t *testing.T) {
	m := newModel(Config{}, nil)
	m.input.SetValue("asdasd")
	m.input.SetCursor(len([]rune("asdasd")))

	lines, placeholder := m.renderInputLines(20)

	if placeholder {
		t.Fatal("expected typed input, not placeholder")
	}
	if len(lines) != 1 {
		t.Fatalf("line count = %d, want 1", len(lines))
	}
	if got := lines[0]; got != "asdasd█" {
		t.Fatalf("line = %q, want %q", got, "asdasd█")
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

func initTUITestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.name", "Test User")
	runGit(t, repo, "config", "user.email", "test@example.com")
	return repo
}

func writeRepoFile(t *testing.T, repo, name, content string) {
	t.Helper()
	path := filepath.Join(repo, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
