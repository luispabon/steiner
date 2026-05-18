package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
)

// testController records all actions received by Handle for test verification.
type testController struct {
	mu      sync.Mutex
	actions []interactive.Action
	err     error
}

func (c *testController) Handle(_ context.Context, action interactive.Action) error {
	c.mu.Lock()
	c.actions = append(c.actions, action)
	c.mu.Unlock()
	return c.err
}

func (c *testController) countSubmitPrompt() int {
	return c.countByType(interactive.SubmitPrompt{})
}

func (c *testController) countRequestContextReport() int {
	return c.countByType(interactive.RequestContextReport{})
}

func (c *testController) countRequestConfigReport() int {
	return c.countByType(interactive.RequestConfigReport{})
}

func (c *testController) countInterruptActiveRun() int {
	return c.countByType(interactive.InterruptActiveRun{})
}

func (c *testController) countRequestExit() int {
	return c.countByType(interactive.RequestExit{})
}

func (c *testController) countSetSkillEnabled() int {
	return c.countByType(interactive.SetSkillEnabled{})
}

func (c *testController) submitApprovals() []interactive.SubmitApproval {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []interactive.SubmitApproval
	for _, a := range c.actions {
		if v, ok := a.(interactive.SubmitApproval); ok {
			result = append(result, v)
		}
	}
	return result
}

func (c *testController) countByType(target interactive.Action) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, a := range c.actions {
		switch target.(type) {
		case interactive.SubmitPrompt:
			if _, ok := a.(interactive.SubmitPrompt); ok {
				count++
			}
		case interactive.RequestContextReport:
			if _, ok := a.(interactive.RequestContextReport); ok {
				count++
			}
		case interactive.RequestConfigReport:
			if _, ok := a.(interactive.RequestConfigReport); ok {
				count++
			}
		case interactive.InterruptActiveRun:
			if _, ok := a.(interactive.InterruptActiveRun); ok {
				count++
			}
		case interactive.RequestExit:
			if _, ok := a.(interactive.RequestExit); ok {
				count++
			}
		case interactive.SetSkillEnabled:
			if _, ok := a.(interactive.SetSkillEnabled); ok {
				count++
			}
		case interactive.SubmitApproval:
			if _, ok := a.(interactive.SubmitApproval); ok {
				count++
			}
		}
	}
	return count
}

func TestModelAppliesRuntimeEvents(t *testing.T) {
	m := newModel(Config{
		Model:         "gpt-test",
		ModelContexts: map[string]int{"gpt-test": 4096},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(1, "hello")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(1, " world")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextTokenBudgetEvent("conversation", 1, 100, 4096, 2, 70, 32, 164, "ok", false)})

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
	if got := m.renderContextInfoLine(120); !strings.Contains(got, "prompt_tokens=100") || !strings.Contains(got, "context_window=4096") || !strings.Contains(got, "context_usage_percent=2%") || !strings.Contains(got, "compaction_threshold=70%") || !strings.Contains(got, "estimator_pad_tokens=32") || !strings.Contains(got, "status=ok") {
		t.Fatalf("context info line = %q, want usage diagnostics", got)
	}
	if got := m.renderContextInfoLine(120); strings.Contains(got, "reserve") || strings.Contains(got, "safety") {
		t.Fatalf("context info line = %q, want no reserve/safety wording", got)
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
	ctrl := &testController{}

	m := newModel(Config{
		SkillNames: []string{"review"},
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	if !m.enabledSkills["review"] {
		t.Fatal("expected configured skills to start enabled")
	}

	m.input.SetValue("fix the bug")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if ctrl.countSubmitPrompt() != 1 {
		t.Fatalf("submit count = %d, want 1", ctrl.countSubmitPrompt())
	}

	m.input.SetValue("/skill review")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.enabledSkills["review"] {
		t.Fatal("expected review skill to be disabled")
	}
	if ctrl.countSetSkillEnabled() != 1 {
		t.Fatalf("skill toggle count = %d, want 1", ctrl.countSetSkillEnabled())
	}

	m.input.SetValue("/skill +review")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.enabledSkills["review"] {
		t.Fatal("expected review skill to be enabled")
	}
}

func TestModelModifiedEnterInsertsNewline(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("first line")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter, Alt: true})

	if ctrl.countSubmitPrompt() != 0 {
		t.Fatalf("submit count = %d, want 0 for modified enter", ctrl.countSubmitPrompt())
	}
	if got := m.input.Value(); got != "first line\n" {
		t.Fatalf("input value = %q, want newline inserted", got)
	}
}

func TestModelPlainEnterStillSubmitsPrompt(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("fix the bug")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if ctrl.countSubmitPrompt() != 1 {
		t.Fatalf("submit count = %d, want 1", ctrl.countSubmitPrompt())
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("input value = %q, want reset after submit", got)
	}
}

func TestModelCtrlXTogglesDelegationWhileConversationActive(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewDelegationCompleteEvent("child-1", "complete", 1, 10, "result text")})

	dd := m.content.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil")
	}
	if !dd.collapsed {
		t.Fatal("delegation should start collapsed")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlX})

	if dd.collapsed {
		t.Fatal("delegation should expand on ctrl+x during active conversation")
	}
	rendered := m.content.String(m.viewport.Width)
	if !strings.Contains(rendered, "result text") {
		t.Fatalf("rendered content = %q, want expanded delegation output", rendered)
	}
}

func TestModelMouseClickTogglesDelegation(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewDelegationStartedEvent("child-1", "task preview")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.WithAgentScope(output.NewAssistantChunkEvent(1, "transcript body"), "child-1")})

	dd := m.content.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil")
	}
	if !dd.collapsed {
		t.Fatal("delegation should start collapsed")
	}

	m.content.String(m.viewport.Width)
	m.contentTopPad = 0
	clickOffset := m.viewportContentTopOffset()
	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 0, Y: clickOffset})
	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, X: 0, Y: clickOffset})

	if dd.collapsed {
		t.Fatal("delegation should expand on mouse click")
	}

	m.content.String(m.viewport.Width)
	m.contentTopPad = 0
	promptHeaderY := -1
	for i, row := range m.content.delegationRows(dd, m.viewport.Width) {
		if row.kind == delegationRowPromptHeader {
			promptHeaderY = i
			break
		}
	}
	if promptHeaderY < 0 {
		t.Fatal("expected prompt header row")
	}
	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 0, Y: promptHeaderY + clickOffset})
	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, X: 0, Y: promptHeaderY + clickOffset})

	if dd.collapsed {
		t.Fatal("delegation should stay expanded when prompt header toggles")
	}
	if !dd.promptCollapsed {
		t.Fatal("prompt subsection should collapse on prompt header click")
	}

	nonToggleY := -1
	for i, row := range m.content.delegationRows(dd, m.viewport.Width) {
		if row.kind == delegationRowPromptBody || row.kind == delegationRowTranscript || row.kind == delegationRowOutput {
			nonToggleY = i
			break
		}
	}
	if nonToggleY < 0 {
		t.Fatal("expected a non-interactive delegation row to click")
	}

	m.content.String(m.viewport.Width)
	m.contentTopPad = 0
	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 0, Y: nonToggleY + clickOffset})
	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, X: 0, Y: nonToggleY + clickOffset})

	if dd.collapsed {
		t.Fatal("transcript/body click should not collapse delegation")
	}
	if !dd.promptCollapsed {
		t.Fatal("transcript/body click should not toggle prompt subsection")
	}
}

func TestModelMouseDragDoesNotToggle(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewDelegationStartedEvent("child-1", "task")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.WithAgentScope(output.NewAssistantChunkEvent(1, "body"), "child-1")})

	dd := m.content.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil")
	}
	if !dd.collapsed {
		t.Fatal("delegation should start collapsed")
	}

	m.content.String(m.viewport.Width)
	m.contentTopPad = 0

	// Press at (0,0), release at (10,0) — different X = drag, should NOT toggle
	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 0, Y: 0})
	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, X: 10, Y: 0})

	if !dd.collapsed {
		t.Fatal("drag (different X) should not toggle collapse")
	}

	// Press at (0,0), release at (0,2) — different Y = drag, should NOT toggle
	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 0, Y: 0})
	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, X: 0, Y: 2})

	if !dd.collapsed {
		t.Fatal("drag (different Y) should not toggle collapse")
	}
}

func TestModelMouseClickTargetsGroupedToolRow(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "bash", "call_1", map[string]any{"command": "pwd"})})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "bash", "call_2", map[string]any{"command": "git status"})})

	group := m.content.segments[0].toolGroupData
	if group == nil {
		t.Fatal("toolGroupData = nil, want grouped tool calls")
	}

	m.content.String(m.viewport.Width)
	m.contentTopPad = 0
	m.viewport.YOffset = 0

	rowForSecond := -1
	dividerRow := -1
	for row := 1; row < m.content.segmentHeights[0]-1; row++ {
		switch idx := m.content.toolCallGroupEntryAtRow(group, row, m.viewport.Width); {
		case idx == 1 && rowForSecond < 0:
			rowForSecond = row
		case idx < 0 && dividerRow < 0:
			dividerRow = row
		}
	}
	if rowForSecond < 0 {
		t.Fatal("did not find row for second grouped tool call")
	}
	if dividerRow < 0 {
		t.Fatal("did not find divider row between grouped tool calls")
	}

	clickOffset := m.viewportContentTopOffset()

	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 0, Y: dividerRow + clickOffset})
	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, X: 0, Y: dividerRow + clickOffset})
	if !group.entries[0].collapsed || !group.entries[1].collapsed {
		t.Fatal("divider click should not toggle any grouped entry")
	}

	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 0, Y: rowForSecond + clickOffset})
	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, X: 0, Y: rowForSecond + clickOffset})

	if !group.entries[0].collapsed {
		t.Fatal("first grouped entry should remain collapsed")
	}
	if group.entries[1].collapsed {
		t.Fatal("second grouped entry should expand on row click")
	}
}

func TestModelMouseClickTargetsStandaloneToolRow(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "bash", "call_1", map[string]any{"command": "pwd"})})

	seg := m.content.segments[0].toolData
	if seg == nil {
		t.Fatal("toolData = nil, want standalone tool call")
	}
	if !seg.collapsed {
		t.Fatal("standalone tool call should start collapsed")
	}

	m.content.String(m.viewport.Width)
	m.contentTopPad = 0
	m.viewport.YOffset = 0

	clickY := m.viewportContentTopOffset()
	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 0, Y: clickY})
	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, X: 0, Y: clickY})

	if seg.collapsed {
		t.Fatal("standalone tool call should expand on visible row click")
	}
}

func TestModelHandlesContextCommandLocally(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("/context")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if ctrl.countSubmitPrompt() != 0 {
		t.Fatalf("submit count = %d, want 0", ctrl.countSubmitPrompt())
	}
	if ctrl.countRequestContextReport() != 1 {
		t.Fatalf("context report count = %d, want 1", ctrl.countRequestContextReport())
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
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("/config")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if ctrl.countSubmitPrompt() != 0 {
		t.Fatalf("submit count = %d, want 0", ctrl.countSubmitPrompt())
	}
	if ctrl.countRequestConfigReport() != 1 {
		t.Fatalf("config report count = %d, want 1", ctrl.countRequestConfigReport())
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

func TestModelCompactEventsKeepTranscriptCleanAndRestoreIdleState(t *testing.T) {
	m := newModel(Config{
		Model:         "gpt-test",
		ModelContexts: map[string]int{"gpt-test": 4096},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:            "compaction",
		Severity:        "compacting",
		CompactionCount: 3,
		SessionState:    "active",
	})})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewThinkingChunkEvent(1, "thinking during compaction")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(1, "raw compaction summary text")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:              "compaction",
		Severity:          "warning",
		CompactionCount:   3,
		SessionState:      "fresh",
		RestartGuidance:   "restart soon in a fresh session",
		SummaryTitle:      "3 messages summarized into 1",
		CompactedMessages: 3,
		CompactedTurns:    1,
	})})

	if got := m.sidebar.compaction; got != "" {
		t.Fatalf("sidebar.compaction = %q, want cleared after compaction finishes", got)
	}
	if got, want := m.status.mode, "running"; got != want {
		t.Fatalf("status.mode = %q, want %q while run remains active", got, want)
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunFinishedEvent(1, "stop", "", "", nil)})

	content := m.content.String(m.viewport.Width)
	for _, want := range []string{"context compacted", "3 messages summarized into 1"} {
		if !strings.Contains(strings.ToLower(content), want) {
			t.Fatalf("content = %q, want compaction banner with %q", content, want)
		}
	}
	for _, want := range []string{"thinking during compaction", "raw compaction summary text"} {
		if strings.Contains(content, want) {
			t.Fatalf("content = %q, want no leaked compaction chunk %q", content, want)
		}
	}
	for _, unwanted := range []string{"compacting", "summarizing context"} {
		if strings.Contains(strings.ToLower(content), unwanted) {
			t.Fatalf("content = %q, want no stale in-progress compaction banner text %q", content, unwanted)
		}
	}
	if got := m.input.Placeholder; got != "ask steiner — / for commands, @ for files" {
		t.Fatalf("input placeholder = %q, want default after compaction finishes", got)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("input value = %q, want reset after compaction finishes", got)
	}
	if m.status.streaming {
		t.Fatal("status.streaming = true, want false after compaction finishes")
	}
	if m.approval.active {
		t.Fatal("approval.active = true, want false after compaction finishes")
	}
}

func TestModelActivityRowReservesLayoutSpace(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 60, Height: 12})

	if got := m.activityRowHeight(m.viewport.Width); got != 1 {
		t.Fatalf("activity row height = %d, want 1", got)
	}
	if m.viewport.Height != 5 {
		t.Fatalf("viewport height = %d, want 5 after reserved activity row", m.viewport.Height)
	}
}

func TestModelActivityRowShowsSpinnerAfterApiRequestBeforeFirstChunk(t *testing.T) {
	m := newModel(Config{Model: "gpt-test"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAPIRequestEvent("gpt-test", nil, nil, nil, nil, prompt.ModelTokenBudget{})})

	row := m.renderActivityRow(m.viewport.Width)
	for _, want := range []string{"waiting on model", "gpt-test", "⠋"} {
		if !strings.Contains(row, want) {
			t.Fatalf("activity row = %q, want %q", row, want)
		}
	}
}

func TestModelActivityRowShowsToolPhaseLabel(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "bash", "call_1", map[string]any{"command": "pwd"})})

	row := m.renderActivityRow(m.viewport.Width)
	for _, want := range []string{"running tool", "bash", "⠋"} {
		if !strings.Contains(row, want) {
			t.Fatalf("activity row = %q, want %q", row, want)
		}
	}
}

func TestModelActivityRowShowsCompactionSpinner(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:            "compaction",
		Severity:        "compacting",
		CompactionCount: 2,
		Turn:            7,
	})})

	row := m.renderActivityRow(m.viewport.Width)
	for _, want := range []string{"compacting context", "2 compactions", "turn 7", "⠋"} {
		if !strings.Contains(row, want) {
			t.Fatalf("activity row = %q, want %q", row, want)
		}
	}
}

func TestModelApprovalKeepsReservedRowAndDisablesSpinner(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "write", "prompt", `{"path":"note.txt"}`)})

	row := m.renderActivityRow(m.viewport.Width)
	if !strings.Contains(strings.ToLower(row), "approval required") {
		t.Fatalf("activity row = %q, want approval label", row)
	}
	if strings.Contains(row, "⠋") {
		t.Fatalf("activity row = %q, want no spinner for approval", row)
	}
	if m.activity.busy() {
		t.Fatal("activity.busy = true, want false for approval")
	}
}

func TestModelInterruptClearsActivityImmediately(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{Controller: ctrl}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAPIRequestEvent("gpt-test", nil, nil, nil, nil, prompt.ModelTokenBudget{})})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	if ctrl.countInterruptActiveRun() != 1 {
		t.Fatalf("interrupt count = %d, want 1", ctrl.countInterruptActiveRun())
	}
	if m.activity.busy() {
		t.Fatal("activity.busy = true, want false after interrupt")
	}
	if m.activity.label != "" || m.activity.detail != "" {
		t.Fatalf("activity = %#v, want cleared", m.activity)
	}
	if got := strings.ToLower(m.renderActivityRow(m.viewport.Width)); strings.Contains(got, "waiting on model") || strings.Contains(got, "running tool") {
		t.Fatalf("activity row = %q, want cleared", got)
	}
}

func TestModelFinishedCompactionDiagnosticDoesNotForceRunningState(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:            "compaction",
		Severity:        "warning",
		CompactionCount: 2,
		SummaryTitle:    "2 messages summarized into 1",
	})})

	if got := m.status.mode; got != "" {
		t.Fatalf("status.mode = %q, want empty for finished compaction diagnostics", got)
	}
	if got := m.sidebar.compaction; got != "" {
		t.Fatalf("sidebar.compaction = %q, want cleared for finished compaction diagnostics", got)
	}
	if got := m.content.String(m.viewport.Width); !strings.Contains(strings.ToLower(got), "context compacted") {
		t.Fatalf("content = %q, want compaction banner", got)
	}
}

func TestModelSessionHealthAfterCompactionDoesNotRearmSidebarSpinner(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:     "compaction",
		Severity: "compacting",
	})})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:            "compaction",
		Severity:        "warning",
		CompactionCount: 1,
		SummaryTitle:    "12 messages summarized into 1",
	})})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextSessionHealthEvent(
		"conversation",
		0,
		1,
		"info",
		"stable",
		"continue, but watch for another compaction",
		"source generation=1 view=full",
	)})

	if got := m.sidebar.compaction; got != "" {
		t.Fatalf("sidebar.compaction = %q, want cleared after finished session health", got)
	}
	lines := strings.Join(m.sidebar.lines(38, 50), "\n")
	if strings.Contains(lines, "compacting…") {
		t.Fatalf("sidebar = %q, want no compacting spinner after finished session health", lines)
	}
}

func TestModelSwitchFailureDoesNotUpdateUI(t *testing.T) {
	ctrl := &testController{err: errors.New("model not found")}

	m := newModel(Config{
		Model:         "original",
		ModelContexts: map[string]int{"original": 1024},
		Controller:    ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m.applyModelSelection("original", "")

	m.input.SetValue("/model unknown")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if got, want := m.status.model, "original"; got != want {
		t.Fatalf("status.model = %q, want %q", got, want)
	}
	if got, want := m.sidebar.model, "original"; got != want {
		t.Fatalf("sidebar.model = %q, want %q", got, want)
	}
}

func TestModelSwitchUpdatesProviderHost(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		Model:           "small",
		ModelContexts:   map[string]int{"small": 1024, "large": 8192},
		ModelBaseURLs:   map[string]string{"large": "http://large.example/v1"},
		ProviderBaseURL: "http://small.example/v1",
		Controller:      ctrl,
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
	m.git.Refresh(context.Background())
	m = updateModel(t, m, gitRefreshDoneMsg{})

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
	m.git.Refresh(context.Background())
	m = updateModel(t, m, gitRefreshDoneMsg{})

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
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
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
	submissions := ctrl.submitApprovals()
	if len(submissions) != 1 {
		t.Fatalf("approval count = %d, want 1", len(submissions))
	}
	if got, want := submissions[0].Decision, "allow_once"; got != want {
		t.Fatalf("submission.Decision = %q, want %q", got, want)
	}
	if got, want := submissions[0].Tool, "write"; got != want {
		t.Fatalf("submission.Tool = %q, want %q", got, want)
	}
	if got, want := submissions[0].Mode, "prompt"; got != want {
		t.Fatalf("submission.Mode = %q, want %q", got, want)
	}
}

func TestModelThinkingToggleDoesNotRevealHiddenScaffoldInference(t *testing.T) {
	m := newModel(Config{
		ShowThinking:                  false,
		ShowInternalScaffoldInference: false,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewThinkingChunkEventWithSource(1, "hidden scaffold reasoning", output.ChunkSourceScaffoldInference)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewThinkingChunkEvent(1, "normal reasoning")})

	if got := m.content.String(m.viewport.Width); strings.Contains(got, "hidden scaffold reasoning") || strings.Contains(got, "normal reasoning") {
		t.Fatalf("content = %q, want no thinking while toggle is off", got)
	}

	m = updateModel(t, m, paletteToggleThinkingMsg{})

	content := m.content.String(m.viewport.Width)
	if !strings.Contains(content, "normal reasoning") {
		t.Fatalf("content = %q, want visible normal thinking after toggle", content)
	}
	if strings.Contains(content, "hidden scaffold reasoning") {
		t.Fatalf("content = %q, want scaffold reasoning hidden with debug flag off", content)
	}
}

func TestModelShowsScaffoldInferenceThinkingOnlyWhenDebugEnabled(t *testing.T) {
	m := newModel(Config{
		ShowThinking:                  true,
		ShowInternalScaffoldInference: true,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewThinkingChunkEventWithSource(1, "visible scaffold reasoning", output.ChunkSourceScaffoldInference)})

	if got := m.content.String(m.viewport.Width); !strings.Contains(got, "visible scaffold reasoning") {
		t.Fatalf("content = %q, want scaffold reasoning visible when debug flag enabled", got)
	}
}

func TestContextDiagnosticsHiddenByDefault(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextTokenBudgetEvent("conversation", 1, 100, 4096, 2, 70, 32, 200, "ok", false)})
	m.syncViewport()

	got := m.viewport.View()
	if strings.Contains(got, "context info:") || strings.Contains(got, "prompt_tokens=") {
		t.Fatalf("viewport = %q, want no context diagnostics when debug disabled", got)
	}
}

func TestContextDiagnosticsShownWhenDebugEnabled(t *testing.T) {
	m := newModel(Config{ShowInternalScaffoldInference: true}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextTokenBudgetEvent("conversation", 1, 100, 4096, 2, 70, 32, 200, "ok", false)})
	m.syncViewport()

	got := m.viewport.View()
	if !strings.Contains(got, "prompt_tokens=100") {
		t.Fatalf("viewport = %q, want context diagnostics when debug enabled", got)
	}
}

func TestModelApprovalEnterAllowedWhileStreaming(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(1, "streaming")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "write", "prompt", `{"path":"note.txt"}`)})

	m.input.SetValue("yes")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	submissions := ctrl.submitApprovals()
	if len(submissions) != 1 {
		t.Fatalf("approval count = %d, want 1", len(submissions))
	}
	if got, want := submissions[0].Decision, "allow_once"; got != want {
		t.Fatalf("submission.Decision = %q, want %q", got, want)
	}
	if m.approval.active {
		t.Fatal("expected approval mode to clear after decision")
	}
}

func TestModelApprovalSelectionAndConfirmation(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
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

	submissions := ctrl.submitApprovals()
	if len(submissions) != 1 {
		t.Fatalf("approval count = %d, want 1", len(submissions))
	}
	if got, want := submissions[0].Decision, "always_allow"; got != want {
		t.Fatalf("submission.Decision = %q, want %q", got, want)
	}
	if got, want := submissions[0].Tool, "bash"; got != want {
		t.Fatalf("submission.Tool = %q, want %q", got, want)
	}
	if m.approval.active {
		t.Fatal("expected approval mode to clear after decision")
	}
}

func TestModelApprovalEscDenies(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "write", "prompt", `{"path":"note.txt"}`)})
	m.input.SetValue("stale text")

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	submissions := ctrl.submitApprovals()
	if len(submissions) != 1 {
		t.Fatalf("approval count = %d, want 1", len(submissions))
	}
	if got, want := submissions[0].Decision, "deny"; got != want {
		t.Fatalf("submission.Decision = %q, want %q", got, want)
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("input value = %q, want reset after denial", got)
	}
	if m.approval.active {
		t.Fatal("expected approval mode to clear after esc denial")
	}
}

func TestModelEscInterruptsStreaming(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(1, "streaming")})
	m.input.SetValue("stale")

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	if ctrl.countInterruptActiveRun() != 1 {
		t.Fatalf("interrupt count = %d, want 1", ctrl.countInterruptActiveRun())
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
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	// Idle state: Ctrl+C should open exit modal, not quit.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}

	if ctrl.countRequestExit() != 0 {
		t.Fatalf("exit request count = %d, want 0 before confirmation", ctrl.countRequestExit())
	}
	if ctrl.countInterruptActiveRun() != 0 {
		t.Fatalf("interrupt count = %d, want 0 (idle, not active run)", ctrl.countInterruptActiveRun())
	}
	if !updated.exitModal.open {
		t.Fatal("exitModal.open = false, want modal open")
	}
	if cmd != nil {
		t.Fatal("expected no quit command when opening exit modal")
	}
}

func TestModelIdleCtrlDOpensExitModalInsteadOfQuitting(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}

	if ctrl.countRequestExit() != 0 {
		t.Fatalf("exit request count = %d, want 0 before confirmation", ctrl.countRequestExit())
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
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
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
	if ctrl.countRequestExit() != 0 {
		t.Fatalf("exit request count = %d, want 0", ctrl.countRequestExit())
	}
}

func TestModelExitModalExitRequestsQuit(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.exitModal.open {
		t.Fatal("exitModal.open = false, want modal open")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	updated := m

	if ctrl.countRequestExit() != 1 {
		t.Fatalf("exit request count = %d, want 1", ctrl.countRequestExit())
	}
	if !updated.exitModal.open {
		t.Fatal("exitModal.open = false, want modal to remain open until runtime quits")
	}
}

func TestModelCtrlCInterruptsStreamingInsteadOfQuitting(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(1, "streaming")})

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}

	if ctrl.countInterruptActiveRun() != 1 {
		t.Fatalf("interrupt count = %d, want 1", ctrl.countInterruptActiveRun())
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
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
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

	if ctrl.countInterruptActiveRun() != 1 {
		t.Fatalf("interrupt count = %d, want 1", ctrl.countInterruptActiveRun())
	}
	if got := m.status.mode; got != "" {
		t.Fatalf("status.mode = %q, want cleared after interrupt", got)
	}
}

func TestModelEscInterruptsToolPhase(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "bash", "call_1", map[string]any{"command": "git log --oneline -10"})})

	if got := m.content.streamingPhase; got != "tool" {
		t.Fatalf("streamingPhase = %q, want tool before interrupt", got)
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	if ctrl.countInterruptActiveRun() != 1 {
		t.Fatalf("interrupt count = %d, want 1", ctrl.countInterruptActiveRun())
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
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "bash", "call_1", map[string]any{"command": "git status"})})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalAcceptedEvent(1, "bash", "prompt", `{"command":"git status"}`, "approved")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(1, "still streaming")})

	if ctrl.countInterruptActiveRun() != 1 {
		t.Fatalf("interrupt count = %d, want 1", ctrl.countInterruptActiveRun())
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
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(1, "streaming")})
	m.input.SetValue("should stay blocked")

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if ctrl.countSubmitPrompt() != 0 {
		t.Fatalf("submit count = %d, want 0 while streaming", ctrl.countSubmitPrompt())
	}
	if m.input.Value() != "should stay blocked" {
		t.Fatalf("input value = %q, want unchanged", m.input.Value())
	}
}

func TestModelAltEnterInsertsNewline(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m.input.SetValue("first line")

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter, Alt: true})

	if got := m.input.Value(); got != "first line\n" {
		t.Fatalf("input value = %q, want newline inserted", got)
	}
	if ctrl.countSubmitPrompt() != 0 {
		t.Fatalf("submit count = %d, want 0 on modified enter", ctrl.countSubmitPrompt())
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
	// Layout rows: top_pad(1) + viewport + hDivider(1) + input(3, with padding 1) + activity(1) + status(1) → viewport.Height = 12-7 = 5
	if m.viewport.Width != 54 {
		t.Fatalf("viewport width = %d, want 54 after pane chrome", m.viewport.Width)
	}
	if got := m.input.Width(); got != 99999 {
		t.Fatalf("input width = %d, want 99999 (no internal textarea wrapping)", got)
	}
	if m.viewport.Height != 5 {
		t.Fatalf("viewport height = %d, want 5 after pane chrome", m.viewport.Height)
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

func TestModelCursorInHardwrappedInput(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 40, Height: 10})

	innerWidth := m.inputInnerWidth(40)
	if innerWidth != 36 {
		t.Fatalf("innerWidth = %d, want 36", innerWidth)
	}

	// 100-char line hardwraps into 3 segments at width 36: 36 + 36 + 28
	val := strings.Repeat("x", 100)
	m.input.SetValue(val)

	tests := []struct {
		name    string
		absPos  int
		wantSeg int
		wantCol int
	}{
		{"start-of-line", 0, 0, 0},
		{"mid-first-segment", 18, 0, 18},
		{"boundary-first-to-second", 36, 1, 0},
		{"mid-second-segment", 54, 1, 18},
		{"boundary-second-to-third", 72, 2, 0},
		{"end-of-line", 100, 2, 28},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m.input.SetCursor(tt.absPos)
			lines := m.renderTypedInputLines(innerWidth)

			cursorRow := -1
			cursorCol := -1
			for i, line := range lines {
				if idx := strings.Index(line, "█"); idx >= 0 {
					if cursorRow >= 0 {
						t.Fatal("multiple cursor markers found")
					}
					cursorRow = i
					cursorCol = idx
				}
			}
			if cursorRow < 0 {
				t.Fatal("no cursor marker found")
			}
			if cursorRow != tt.wantSeg {
				t.Fatalf("cursor row = %d, want %d", cursorRow, tt.wantSeg)
			}
			if cursorCol != tt.wantCol {
				t.Fatalf("cursor col = %d, want %d", cursorCol, tt.wantCol)
			}
		})
	}
}

func TestModelCursorInHardwrappedInputWithLeftArrow(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 40, Height: 10})

	innerWidth := m.inputInnerWidth(40)

	// 50-char single-line input wraps into 2 segments: 36 + 14
	val := strings.Repeat("y", 50)
	m.input.SetValue(val)

	// After SetValue cursor is at end; press left arrow 3 times via textarea update
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyLeft})

	lines := m.renderTypedInputLines(innerWidth)

	cursorRow := -1
	cursorCol := -1
	for i, line := range lines {
		if idx := strings.Index(line, "█"); idx >= 0 {
			if cursorRow >= 0 {
				t.Fatal("multiple cursor markers found")
			}
			cursorRow = i
			cursorCol = idx
		}
	}
	if cursorRow < 0 {
		t.Fatal("no cursor marker found")
	}
	// Cursor at position 47: second segment (36-based), offset 11
	if cursorRow != 1 {
		t.Fatalf("cursor row = %d, want 1 (second segment)", cursorRow)
	}
	if cursorCol != 11 {
		t.Fatalf("cursor col = %d, want 11", cursorCol)
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
