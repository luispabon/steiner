package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// testController records all actions received by Handle for test verification.
type testController struct {
	mu                        sync.Mutex
	actions                   []interactive.Action
	err                       error
	switchModelErr            error
	workflowHandoffSelections map[string]interactive.WorkflowHandoffModelSelection
}

func (c *testController) Handle(_ context.Context, action interactive.Action) error {
	c.mu.Lock()
	c.actions = append(c.actions, action)
	c.mu.Unlock()
	if _, ok := action.(interactive.SwitchModel); ok && c.switchModelErr != nil {
		return c.switchModelErr
	}
	return c.err
}

func (c *testController) WorkflowHandoffModelSelection(destination string) interactive.WorkflowHandoffModelSelection {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.workflowHandoffSelections == nil {
		return interactive.WorkflowHandoffModelSelection{}
	}
	return c.workflowHandoffSelections[destination]
}

func (c *testController) countSubmitPrompt() int {
	return c.countByType(interactive.SubmitPrompt{})
}

func (c *testController) submitPrompts() []interactive.SubmitPrompt {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []interactive.SubmitPrompt
	for _, a := range c.actions {
		if v, ok := a.(interactive.SubmitPrompt); ok {
			result = append(result, v)
		}
	}
	return result
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

func (c *testController) submitWorkflowHandoffs() []interactive.SubmitWorkflowHandoff {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []interactive.SubmitWorkflowHandoff
	for _, a := range c.actions {
		if v, ok := a.(interactive.SubmitWorkflowHandoff); ok {
			result = append(result, v)
		}
	}
	return result
}

func (c *testController) rotateSessionActions() []interactive.RotateSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []interactive.RotateSession
	for _, a := range c.actions {
		if v, ok := a.(interactive.RotateSession); ok {
			result = append(result, v)
		}
	}
	return result
}

func (c *testController) switchModelActions() []interactive.SwitchModel {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []interactive.SwitchModel
	for _, a := range c.actions {
		if v, ok := a.(interactive.SwitchModel); ok {
			result = append(result, v)
		}
	}
	return result
}

func (c *testController) skillEnabledActions() []interactive.SetSkillEnabled {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []interactive.SetSkillEnabled
	for _, a := range c.actions {
		if v, ok := a.(interactive.SetSkillEnabled); ok {
			result = append(result, v)
		}
	}
	return result
}

func (c *testController) countSteerPrompt() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, a := range c.actions {
		if _, ok := a.(interactive.SteerPrompt); ok {
			count++
		}
	}
	return count
}

func (c *testController) steerPrompts() []interactive.SteerPrompt {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []interactive.SteerPrompt
	for _, a := range c.actions {
		if v, ok := a.(interactive.SteerPrompt); ok {
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
		case interactive.SubmitWorkflowHandoff:
			if _, ok := a.(interactive.SubmitWorkflowHandoff); ok {
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

func TestModelRoutesShortContextReportToTranscript(t *testing.T) {
	m := newModel(Config{Model: "gpt-test"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Short single-line context report should go to the transcript, not the overlay.
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextReportEvent("cave_human mode: on")})

	if m.contextOverlay.IsOpen() {
		t.Fatal("contextOverlay.IsOpen() = true, want overlay closed for short context report")
	}
	if got := stripANSI(m.content.String(m.viewport.Width)); !strings.Contains(got, "cave_human mode: on") {
		t.Fatalf("content = %q, want context report text in transcript", got)
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

func TestModelIgnoresScopedContextDiagnosticsForSidebarAndStatus(t *testing.T) {
	m := newModel(Config{
		Model:         "gpt-test",
		ModelContexts: map[string]int{"gpt-test": 4096},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	// Main agent context diagnostics set the baseline.
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextTokenBudgetEvent("conversation", 1, 100, 4096, 2, 70, 32, 164, "ok", false)})
	if got := m.status.context; got != "ctx 100/4096" {
		t.Fatalf("status.context = %q, want ctx 100/4096", got)
	}
	if got := m.sidebar.promptUsed; got != 100 {
		t.Fatalf("sidebar.promptUsed = %d, want 100", got)
	}
	if got := m.sidebar.contextBudget; got != 4096 {
		t.Fatalf("sidebar.contextBudget = %d, want 4096", got)
	}

	// Sub-agent scoped context diagnostics must not overwrite main agent values.
	scoped := output.WithAgentScope(output.NewContextTokenBudgetEvent("conversation", 1, 2000, 8192, 50, 70, 32, 2100, "ok", false), "child-1")
	m = updateModel(t, m, runtimeEventMsg{Event: scoped})
	if got := m.status.context; got != "ctx 100/4096" {
		t.Fatalf("status.context = %q, want ctx 100/4096 after scoped event", got)
	}
	if got := m.sidebar.promptUsed; got != 100 {
		t.Fatalf("sidebar.promptUsed = %d, want 100 after scoped event", got)
	}
	if got := m.sidebar.contextBudget; got != 4096 {
		t.Fatalf("sidebar.contextBudget = %d, want 4096 after scoped event", got)
	}

	// Main agent resumes and updates context fill normally.
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextTokenBudgetEvent("conversation", 2, 150, 4096, 5, 70, 32, 180, "ok", false)})
	if got := m.status.context; got != "ctx 150/4096" {
		t.Fatalf("status.context = %q, want ctx 150/4096 after resume", got)
	}
	if got := m.sidebar.promptUsed; got != 150 {
		t.Fatalf("sidebar.promptUsed = %d, want 150 after resume", got)
	}
}

func TestModelSubmitsInputAndTogglesSkills(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		SkillNames: []string{"review"},
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	if m.enabledSkills["review"] {
		t.Fatal("expected configured skills to start disabled")
	}

	m.input.SetValue("fix the bug")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if ctrl.countSubmitPrompt() != 1 {
		t.Fatalf("submit count = %d, want 1", ctrl.countSubmitPrompt())
	}

	m.input.SetValue("/skill review")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.enabledSkills["review"] {
		t.Fatal("expected review skill to be enabled")
	}
	if ctrl.countSetSkillEnabled() != 1 {
		t.Fatalf("skill toggle count = %d, want 1", ctrl.countSetSkillEnabled())
	}
	if got := m.sidebar.activeSkill; got != "review" {
		t.Fatalf("sidebar.activeSkill = %q, want review", got)
	}

	m.input.SetValue("/skill -review")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.enabledSkills["review"] {
		t.Fatal("expected review skill to be disabled")
	}
	if got := m.sidebar.activeSkill; got != "" {
		t.Fatalf("sidebar.activeSkill = %q, want empty", got)
	}
}

func TestClearResetsActiveSkill(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		SkillNames: []string{"review"},
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("/skill review")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.enabledSkills["review"] {
		t.Fatal("expected review skill to be enabled")
	}
	if got := m.sidebar.activeSkill; got != "review" {
		t.Fatalf("sidebar.activeSkill = %q, want review", got)
	}

	m.input.SetValue("/clear")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.enabledSkills["review"] {
		t.Fatal("expected review skill to be disabled after /clear")
	}
	if got := m.sidebar.activeSkill; got != "" {
		t.Fatalf("sidebar.activeSkill = %q, want empty after /clear", got)
	}
}

func TestSkillExclusivity(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		SkillNames: []string{"review", "test"},
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("/skill test")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.enabledSkills["test"] {
		t.Fatal("expected test skill to be enabled")
	}
	if m.enabledSkills["review"] {
		t.Fatal("expected review skill to stay disabled")
	}
	if got := m.sidebar.activeSkill; got != "test" {
		t.Fatalf("sidebar.activeSkill = %q, want test", got)
	}

	m.input.SetValue("/review")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.enabledSkills["review"] {
		t.Fatal("expected review skill to be enabled")
	}
	if m.enabledSkills["test"] {
		t.Fatal("expected test skill to be disabled when review is invoked")
	}
	if got := m.sidebar.activeSkill; got != "review" {
		t.Fatalf("sidebar.activeSkill = %q, want review", got)
	}

	m.input.SetValue("/skill -review")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.enabledSkills["review"] {
		t.Fatal("expected review skill to be disabled")
	}
	if m.enabledSkills["test"] {
		t.Fatal("expected test skill to remain disabled")
	}
	if got := m.sidebar.activeSkill; got != "" {
		t.Fatalf("sidebar.activeSkill = %q, want empty", got)
	}

	gotActions := ctrl.skillEnabledActions()
	wantActions := []interactive.SetSkillEnabled{
		{Name: "test", Enabled: true},
		{Name: "test", Enabled: false},
		{Name: "review", Enabled: true},
		{Name: "review", Enabled: false},
	}
	if len(gotActions) != len(wantActions) {
		t.Fatalf("skill actions = %#v, want %#v", gotActions, wantActions)
	}
	for i := range wantActions {
		if gotActions[i] != wantActions[i] {
			t.Fatalf("skill actions[%d] = %#v, want %#v", i, gotActions[i], wantActions[i])
		}
	}
}

func TestSidebarSkillSection(t *testing.T) {
	styles := theme.Default().LipGlossStyles()

	sidebar := sidebarState{
		activeSkill: "review",
		styles:      styles,
	}
	joined := strings.Join(sidebar.lines(38, 50), "\n")
	if !strings.Contains(joined, "SKILL") {
		t.Fatalf("sidebar = %q, want skill section", joined)
	}
	if !strings.Contains(joined, "review") {
		t.Fatalf("sidebar = %q, want active skill name", joined)
	}

	empty := sidebarState{styles: styles}
	if joined := strings.Join(empty.lines(38, 50), "\n"); strings.Contains(joined, "SKILL") {
		t.Fatalf("sidebar = %q, want no skill section", joined)
	}
}

func TestBuildSlashOverlayItemsUsesSkillDescriptions(t *testing.T) {
	m := newModel(Config{
		SkillNames:        []string{"review"},
		SkillDescriptions: map[string]string{"review": "Review changes for bugs and regressions."},
	}, nil)

	items := m.buildSlashOverlayItems()

	var reviewItem *slashOverlayItem
	for i := range items {
		if items[i].command == "/review" && items[i].isSkill {
			reviewItem = &items[i]
			break
		}
	}
	if reviewItem == nil {
		t.Fatal("expected /review item in slash overlay")
	}
	if reviewItem.name != "" {
		t.Fatalf("review item name = %q, want empty", reviewItem.name)
	}
	if reviewItem.desc != "Review changes for bugs and regressions." {
		t.Fatalf("review item desc = %q, want skill description", reviewItem.desc)
	}
	if !reviewItem.isSkill {
		t.Fatal("expected review item to be marked as a skill")
	}
}

func TestComposerTokenAtCursor(t *testing.T) {
	t.Run("finds slash token at cursor", func(t *testing.T) {
		token, start, end, ok := composerTokenAtCursor("/con", len([]rune("/con")), '/')
		if !ok {
			t.Fatal("expected slash token to be found")
		}
		if token != "/con" || start != 0 || end != 4 {
			t.Fatalf("got token=%q start=%d end=%d, want /con 0 4", token, start, end)
		}
	})

	t.Run("finds at token after leading text", func(t *testing.T) {
		value := "check @inte"
		token, start, end, ok := composerTokenAtCursor(value, len([]rune(value)), '@')
		if !ok {
			t.Fatal("expected @ token to be found")
		}
		if token != "@inte" {
			t.Fatalf("token = %q, want @inte", token)
		}
		if got := value[start:end]; got != "@inte" {
			t.Fatalf("slice = %q, want @inte", got)
		}
	})

	t.Run("ignores non-matching token", func(t *testing.T) {
		if _, _, _, ok := composerTokenAtCursor("plain text", len([]rune("plain text")), '@'); ok {
			t.Fatal("expected no @ token")
		}
	})
}

func TestModelSlashOverlayTypingUsesComposerText(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})

	if got := m.input.Value(); got != "/co" {
		t.Fatalf("input value = %q, want /co", got)
	}
	if !m.slashOverlay.IsOpen() {
		t.Fatal("expected slash overlay to stay open")
	}
	if got := m.slashOverlay.query; got != "/co" {
		t.Fatalf("slash query = %q, want /co", got)
	}
	foundConfig := false
	foundContext := false
	for _, candidate := range m.slashOverlay.candidates {
		if candidate.command == "/config" {
			foundConfig = true
		}
		if candidate.command == "/context" {
			foundContext = true
		}
	}
	if !foundConfig || !foundContext {
		t.Fatalf("candidates = %#v, want /config and /context present", m.slashOverlay.candidates)
	}
}

func TestModelSlashOverlayEscRemovesActiveToken(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	if m.slashOverlay.IsOpen() {
		t.Fatal("expected slash overlay to close on Esc")
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("input value = %q, want empty after Esc", got)
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
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewDelegationCompleteEvent("child-1", "complete", 1, 10, 0, "result text")})

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

func TestModelMouseClickTargetsResumedToolRowAfterUserGap(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 14})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewUserInputEvent("resumed prompt", "resume")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "bash", "call_1", map[string]any{"command": "pwd"})})

	seg := m.content.segments[1].toolData
	if seg == nil {
		t.Fatal("toolData = nil, want standalone tool call after resumed prompt")
	}
	if !seg.collapsed {
		t.Fatal("standalone tool call should start collapsed")
	}

	rendered := stripANSI(m.content.String(m.viewport.Width))
	lines := strings.Split(rendered, "\n")
	toolRow := -1
	for i, line := range lines {
		if strings.Contains(line, "bash") && strings.Contains(line, "pwd") {
			toolRow = i
			break
		}
	}
	if toolRow < 0 {
		t.Fatalf("rendered content missing tool header: %q", rendered)
	}

	m.contentTopPad = 0
	m.viewport.YOffset = 0
	clickY := toolRow + m.viewportContentTopOffset()
	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 0, Y: clickY})
	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, X: 0, Y: clickY})

	if seg.collapsed {
		t.Fatal("standalone tool call should expand on visible row click after resumed prompt gap")
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
	if !m.contextOverlay.IsOpen() {
		t.Fatal("contextOverlay.IsOpen() = false, want overlay open after context report event")
	}
	if !strings.Contains(m.contextOverlay.content, "Last Request Context") {
		t.Fatalf("contextOverlay.content = %q, want report content", m.contextOverlay.content)
	}
	if got := strings.TrimSpace(m.content.String(m.viewport.Width)); got != "" {
		t.Fatalf("content = %q, want no transcript insertion for context report", got)
	}

	// Esc should close the overlay.
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.contextOverlay.IsOpen() {
		t.Fatal("contextOverlay.IsOpen() = true, want overlay closed after Esc")
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

	if !m.contextOverlay.IsOpen() {
		t.Fatal("contextOverlay.IsOpen() = false, want overlay open after config report event")
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

	if m.contextOverlay.IsOpen() {
		t.Fatal("contextOverlay.IsOpen() = true, want no overlay for display_file")
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
	for _, want := range []string{"compaction", "3 messages summarized into 1"} {
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

func TestModelStatusBarKeepsPrimaryModelDuringOtherRuntimeCalls(t *testing.T) {
	m := newModel(Config{Model: "main-model"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 12})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "main-model", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAPIRequestEvent("other-runtime-model", nil, nil, nil, nil, prompt.ModelTokenBudget{})})

	statusLine := stripANSI(m.status.view(m.viewport.Width))
	if !strings.Contains(statusLine, "model main-model") {
		t.Fatalf("status line = %q, want primary model badge", statusLine)
	}
	if strings.Contains(statusLine, "other-runtime-model") {
		t.Fatalf("status line = %q, want no runtime model override", statusLine)
	}
}

func TestModelTabCompletesModelCommandInPrompt(t *testing.T) {
	m := newModel(Config{
		ModelNames: []string{"deepseek-v4-flash", "qwen3-coder-30b"},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 12})

	m.input.SetValue("/model")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyTab})

	// /model (no args) should be a completion candidate
	if got, want := m.input.Value(), "/model"; got != want {
		t.Fatalf("input after tab = %q, want %q", got, want)
	}
	if got := len(m.completionCandidates); got == 0 {
		t.Fatal("completionCandidates = 0, want cached candidates")
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
	if got := m.content.String(m.viewport.Width); !strings.Contains(strings.ToLower(got), "compaction") {
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

func TestModelPickerEnterSwitchesActiveModel(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		Model:         "small",
		ModelNames:    []string{"small", "large"},
		ModelContexts: map[string]int{"small": 1024, "large": 8192},
		ModelBaseURLs: map[string]string{"large": "http://large.example/v1"},
		Controller:    ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 20})

	m.input.SetValue("/model")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if !m.modelPicker.IsOpen() {
		t.Fatal("modelPicker.IsOpen() = false, want true after /model")
	}
	if m.modelPicker.IsWorkflowHandoff() {
		t.Fatal("modelPicker.IsWorkflowHandoff() = true, want false for command picker")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.modelPicker.IsOpen() {
		t.Fatal("modelPicker.IsOpen() = true, want false after selection")
	}
	if got, want := m.primaryModel, "large"; got != want {
		t.Fatalf("primaryModel = %q, want %q", got, want)
	}
	if got, want := m.sidebar.provider, "http://large.example/v1"; got != want {
		t.Fatalf("sidebar.provider = %q, want %q", got, want)
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

func TestModelThinkingToggleShowsOnlyAfterToggle(t *testing.T) {
	m := newModel(Config{
		ShowThinking: false,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewThinkingChunkEvent(1, "normal reasoning")})

	if got := m.content.String(m.viewport.Width); strings.Contains(got, "normal reasoning") {
		t.Fatalf("content = %q, want no thinking while toggle is off", got)
	}

	m = updateModel(t, m, paletteToggleThinkingMsg{})

	content := m.content.String(m.viewport.Width)
	if !strings.Contains(content, "normal reasoning") {
		t.Fatalf("content = %q, want visible normal thinking after toggle", content)
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

func TestModelApprovalCtrlCInterrupts(t *testing.T) {
	for _, key := range []tea.KeyType{tea.KeyCtrlC, tea.KeyCtrlD} {
		t.Run(key.String(), func(t *testing.T) {
			ctrl := &testController{}
			m := newModel(Config{Controller: ctrl}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
			m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
			m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "bash", "prompt", `{"command":"pwd"}`)})
			if !m.approval.active {
				t.Fatal("approval.active = false, want true")
			}

			m = updateModel(t, m, tea.KeyMsg{Type: key})

			if ctrl.countInterruptActiveRun() != 1 {
				t.Fatalf("interrupt count = %d, want 1", ctrl.countInterruptActiveRun())
			}
			if m.approval.active {
				t.Fatal("approval.active = true, want false after ctrl+c interrupt")
			}
			if !m.interruptPending {
				t.Fatal("interruptPending = false, want true (waiting for run to finish)")
			}
		})
	}
}

func TestModelApprovalStopReasonRestoresComposerFocus(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	if !m.input.Focused() {
		t.Fatal("input.Focused() = false, want true at start")
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "bash", "prompt", `{"command":"pwd"}`)})
	if m.input.Focused() {
		t.Fatal("input.Focused() = true, want false while approval is open")
	}
	if !m.approval.active {
		t.Fatal("approval.active = false, want true before stop")
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewStopReasonEvent(1, "error", nil)})

	if !m.input.Focused() {
		t.Fatal("input.Focused() = false, want true after stop")
	}
	if m.approval.active {
		t.Fatal("approval.active = true, want false after stop")
	}
	if got, want := m.status.mode, "error"; got != want {
		t.Fatalf("status.mode = %q, want %q", got, want)
	}
}

func TestModelApprovalRunFinishedRestoresComposerFocus(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "mutate", "prompt", `{"path":"note.txt"}`)})
	if m.input.Focused() {
		t.Fatal("input.Focused() = true, want false while approval is open")
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunFinishedEvent(1, "cancelled", "", "", nil)})

	if !m.input.Focused() {
		t.Fatal("input.Focused() = false, want true after run finish")
	}
	if m.approval.active {
		t.Fatal("approval.active = true, want false after run finish")
	}
	if got, want := m.status.mode, "cancelled"; got != want {
		t.Fatalf("status.mode = %q, want %q", got, want)
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
	if !updated.exitModal.IsOpen() {
		t.Fatal("exitModal.IsOpen() = false, want modal open")
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
	if !updated.exitModal.IsOpen() {
		t.Fatal("exitModal.IsOpen() = false, want modal open")
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
	if !m.exitModal.IsOpen() {
		t.Fatal("exitModal.IsOpen() = false, want modal open")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	updated := m

	if updated.exitModal.IsOpen() {
		t.Fatal("exitModal.IsOpen() = true, want modal closed")
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
	if !m.exitModal.IsOpen() {
		t.Fatal("exitModal.IsOpen() = false, want modal open")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	updated := m

	if ctrl.countRequestExit() != 1 {
		t.Fatalf("exit request count = %d, want 1", ctrl.countRequestExit())
	}
	if !updated.exitModal.IsOpen() {
		t.Fatal("exitModal.IsOpen() = false, want modal to remain open until runtime quits")
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
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "bash", "call_0", map[string]any{"command": "git diff"})})
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

	// ToolCallFinishedEvent must NOT be suppressed during interrupt.
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallFinishedEvent(1, "bash", "call_0", "{}", nil)})

	// Verify the tool segment for call_0 was updated with completion meta.
	foundCall := false
	for _, seg := range m.content.segments {
		if seg.kind == segmentToolCall && seg.toolData != nil && seg.toolData.callID == "call_0" {
			foundCall = true
			if seg.toolData.meta != "✅" {
				t.Fatalf("tool segment meta = %q, want ✅ after ToolCallFinishedEvent", seg.toolData.meta)
			}
			if seg.toolData.hasError {
				t.Fatal("tool segment hasError = true, want false after successful finish")
			}
			if seg.toolData.body != "{}" {
				t.Fatalf("tool segment body = %q, want %q", seg.toolData.body, "{}")
			}
			break
		}
	}
	if !foundCall {
		t.Fatal("no tool call segment with callID call_0 found after ToolCallFinishedEvent")
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewStopReasonEvent(1, "cancelled", nil)})
	if got, want := m.status.mode, "cancelled"; got != want {
		t.Fatalf("status.mode = %q, want %q after stop", got, want)
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunFinishedEvent(1, "cancelled", "", "", nil)})
	rendered := m.content.String(m.viewport.Width)
	if !strings.Contains(rendered, "cancelled") {
		t.Fatal("expected cancelled stop reason to remain visible")
	}
	if !strings.Contains(rendered, "status") {
		t.Fatal("expected cancelled stop reason to render with the status tag")
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(2, "fresh run")})
	if got := m.content.streamingPhase; got != "answer" {
		t.Fatalf("streamingPhase = %q, want answer after next run resumes", got)
	}
}

func TestModelStreamingEnterQueuesSteerPrompt(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(1, "streaming")})
	m.input.SetValue("steer message")

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	// Enter during streaming must not submit a normal prompt.
	if ctrl.countSubmitPrompt() != 0 {
		t.Fatalf("submit count = %d, want 0 while streaming", ctrl.countSubmitPrompt())
	}
	// Enter during streaming must send a SteerPrompt action.
	if ctrl.countSteerPrompt() != 1 {
		t.Fatalf("steer count = %d, want 1", ctrl.countSteerPrompt())
	}
	if got := ctrl.steerPrompts()[0].Text; got != "steer message" {
		t.Fatalf("steer text = %q, want %q", got, "steer message")
	}
	// Input must be cleared after steer.
	if m.input.Value() != "" {
		t.Fatalf("input value = %q, want empty after steer", m.input.Value())
	}
	// steerQueued flag must be set.
	if !m.steerQueued {
		t.Fatal("steerQueued = false, want true after steer sent")
	}
	// A pending steer segment must appear in the content buffer.
	found := false
	for _, seg := range m.content.segments {
		if seg.kind == segmentPendingSteer && seg.text == "steer message" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no segmentPendingSteer found in content buffer after steer")
	}
}

func TestModelStreamingEmptyEnterIsNoop(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(1, "streaming")})
	// Leave input empty.

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if ctrl.countSubmitPrompt() != 0 {
		t.Fatalf("submit count = %d, want 0 for empty enter while streaming", ctrl.countSubmitPrompt())
	}
	if ctrl.countSteerPrompt() != 0 {
		t.Fatalf("steer count = %d, want 0 for empty enter while streaming", ctrl.countSteerPrompt())
	}
	if m.steerQueued {
		t.Fatal("steerQueued = true, want false for empty enter")
	}
}

func TestModelSteerReceivedEventAppendUserMessage(t *testing.T) {
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEvent(1, "streaming")})
	m.input.SetValue("my steer")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if !m.steerQueued {
		t.Fatal("steerQueued = false before SteerReceivedEvent")
	}

	// Simulate the agent loop consuming the steer.
	steerReceivedEvent := output.Event{
		Type:    output.EventTypeSteerReceived,
		Payload: output.SteerReceivedEvent{Text: "my steer"},
	}
	m = updateModel(t, m, runtimeEventMsg{Event: steerReceivedEvent})

	if m.steerQueued {
		t.Fatal("steerQueued = true after SteerReceivedEvent, want false")
	}

	var pendingCount, userCount int
	for _, seg := range m.content.segments {
		if seg.text == "my steer" {
			switch seg.kind {
			case segmentPendingSteer:
				pendingCount++
			case segmentUserMarkdown:
				userCount++
			}
		}
	}
	// Pending box must remain unchanged.
	if pendingCount != 1 {
		t.Fatalf("segmentPendingSteer count = %d, want 1 (original queued box must stay)", pendingCount)
	}
	// A new normal user message must appear at the delivery point.
	if userCount != 1 {
		t.Fatalf("segmentUserMarkdown count = %d, want 1 (delivery message must be appended)", userCount)
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

func TestModelIgnoresStructuredMouseLeakRunes(t *testing.T) {
	tests := []string{
		"[<65;174;45M",
		"<65;174;45m",
		"[<65",
		"[<65;174",
		"<65;174",
		"65;174;45M",
	}

	for _, fragment := range tests {
		t.Run(fragment, func(t *testing.T) {
			m := newModel(Config{WorkingDir: "."}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
			m.input.SetValue("seed")

			m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(fragment)})

			if got := m.input.Value(); got != "seed" {
				t.Fatalf("input value = %q, want unchanged", got)
			}
		})
	}
}

func TestModelAllowsNormalRuneInputNearMouseLikeText(t *testing.T) {
	tests := []string{"[", "[abc", "<tag>", "65;foo"}

	for _, text := range tests {
		t.Run(text, func(t *testing.T) {
			m := newModel(Config{}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

			m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})

			if got := m.input.Value(); got != text {
				t.Fatalf("input value = %q, want %q", got, text)
			}
		})
	}
}

func TestModelIgnoresBareBracketMousePrefixAfterWheel(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[[[")})

	if got := m.input.Value(); got != "" {
		t.Fatalf("input value = %q, want empty after wheel leak prefix", got)
	}
}

func TestModelAllowsBareBracketOutsideRecentWheelWindow(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.lastWheelMouseAt = time.Now().Add(-time.Second)

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[")})

	if got := m.input.Value(); got != "[" {
		t.Fatalf("input value = %q, want [", got)
	}
}

func TestModelListFilesOpensOverlayWithWorkingDir(t *testing.T) {
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.input.SetValue("/ls")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.fileList.IsOpen() {
		t.Fatal("expected file list overlay to open after /ls")
	}
	if m.fileList.root != "." {
		t.Fatalf("file list root = %q, want .", m.fileList.root)
	}
	if len(m.fileList.entries) == 0 {
		t.Fatal("expected non-empty file list")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.fileList.IsOpen() {
		t.Fatal("expected file list overlay to close after Esc")
	}
}

func TestModelListFilesOpensWithPath(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.input.SetValue("/ls .")
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.fileList.IsOpen() {
		t.Fatal("expected file list overlay to open after /ls .")
	}
	if len(m.fileList.entries) == 0 {
		t.Fatal("expected non-empty file list for .")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.fileList.IsOpen() {
		t.Fatal("expected file list overlay to close after Enter")
	}
}

func TestModelFilePickerOverlayInView(t *testing.T) {
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	if !m.filePicker.IsOpen() {
		t.Fatal("expected file picker to open after @")
	}

	view := stripANSI(m.View())
	// The file picker should appear in the view (not be hidden)
	if !strings.Contains(view, "@") {
		t.Fatal("expected file picker content in View()")
	}
	// The divider, input, and status should still be visible
	if !strings.Contains(view, "─") {
		t.Fatal("expected divider in View()")
	}
	if !strings.Contains(view, "@") {
		t.Fatal("expected composer text in View()")
	}
	if !strings.Contains(view, "┃") {
		t.Fatal("expected accented composer border in View()")
	}
}

func TestModelRenderInputLinesUsesLocalCursor(t *testing.T) {
	m := newModel(Config{}, nil)
	m.input.SetValue("asdasd")
	m.input.SetCursor(len([]rune("asdasd")))

	lines, placeholder, _ := m.renderInputLines(20)

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
			lines, _ := m.renderTypedInputLines(innerWidth)

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

	lines, _ := m.renderTypedInputLines(innerWidth)

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

func TestModelFilePicker_TabInsertsPath(t *testing.T) {
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	if !m.filePicker.IsOpen() {
		t.Fatal("expected file picker to open")
	}
	if len(m.filePicker.candidates) == 0 {
		t.Skip("no candidates")
	}

	selected := m.filePicker.candidates[m.filePicker.selection]

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.filePicker.IsOpen() {
		t.Fatal("expected file picker to close after Tab")
	}

	val := m.input.Value()
	if !strings.HasPrefix(val, selected+" ") && val != selected {
		t.Fatalf("expected input to start with %q, got %q", selected+" ", val)
	}
}

func TestModelSlashOverlay_TabInsertsCommand(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !m.slashOverlay.IsOpen() {
		t.Fatal("expected slash overlay to open")
	}
	if len(m.slashOverlay.candidates) == 0 {
		t.Skip("no candidates")
	}

	selected := m.slashOverlay.candidates[m.slashOverlay.selection]

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.slashOverlay.IsOpen() {
		t.Fatal("expected slash overlay to close after Tab")
	}

	val := m.input.Value()
	if !strings.HasPrefix(val, selected.command+" ") {
		t.Fatalf("expected input to start with %q, got %q", selected.command+" ", val)
	}
}

func TestModelFilePicker_ReopensAfterSpaceBackspace(t *testing.T) {
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})

	if !m.filePicker.IsOpen() {
		t.Fatal("expected file picker open after @go")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if m.filePicker.IsOpen() {
		t.Fatal("expected file picker to close after space")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	if !m.filePicker.IsOpen() {
		t.Fatal("expected file picker to re-open after backspace")
	}
	if got := m.filePicker.query; got != "go" {
		t.Fatalf("picker query = %q, want go", got)
	}
}

func TestModelSlashOverlay_ReopensAfterSpaceBackspace(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})

	if !m.slashOverlay.IsOpen() {
		t.Fatal("expected slash overlay open after /co")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if m.slashOverlay.IsOpen() {
		t.Fatal("expected slash overlay to close after space")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	if !m.slashOverlay.IsOpen() {
		t.Fatal("expected slash overlay to re-open after backspace")
	}
}

func TestModelFilePicker_ReopensOnLeftArrow(t *testing.T) {
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})

	if !m.filePicker.IsOpen() {
		t.Fatal("expected file picker open after @go")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	if m.filePicker.IsOpen() {
		t.Fatal("expected file picker to close after space")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	if !m.filePicker.IsOpen() {
		t.Fatal("expected file picker to re-open after left arrow")
	}
}

func TestModelFilePicker_NoReopenAfterEsc(t *testing.T) {
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})

	if !m.filePicker.IsOpen() {
		t.Fatal("expected file picker open after @g")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.filePicker.IsOpen() {
		t.Fatal("expected file picker to close on Esc")
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.filePicker.IsOpen() {
		t.Fatal("expected file picker to NOT re-open after Esc removed the token")
	}
}

func TestFrameHeightClamping(t *testing.T) {
	tests := []struct {
		name     string
		height   int
		content  string
		wantMaxH int
	}{
		{"single-line-h5", 5, "one line", 5},
		{"single-line-h8", 8, "one line", 8},
		{"single-line-h10", 10, "one line", 10},
		{"single-line-h15", 15, "one line", 15},
		{"single-line-h24", 24, "one line", 24},
		{"single-line-h40", 40, "one line", 40},
		{"multi-line-h5", 5, "line1\nline2\nline3\nline4\nline5", 5},
		{"multi-line-h8", 8, "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10", 8},
		{"multi-line-h10", 10, "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\nline11\nline12\nline13\nline14\nline15", 10},
		{"multi-line-h15", 15, strings.Repeat("line\n", 30), 15},
		{"multi-line-h24", 24, strings.Repeat("line\n", 50), 24},
		{"multi-line-h40", 40, strings.Repeat("line\n", 100), 40},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(Config{}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: tt.height})

			rendered := m.renderMainColumn(74)
			lines := strings.Split(strings.TrimSpace(ansi.Strip(rendered)), "\n")
			if len(lines) > tt.wantMaxH {
				t.Fatalf("rendered frame height = %d, want <= %d", len(lines), tt.wantMaxH)
			}
		})
	}
}

func TestSelectionSmallHeight(t *testing.T) {
	tests := []struct {
		name            string
		height          int
		wantScreenLines int
	}{
		{"h5", 5, 5},
		{"h8", 8, 8},
		{"h10", 10, 10},
		{"h15", 15, 15},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(Config{}, nil)
			screenLines := new([]string)
			m.screenLines = screenLines

			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: tt.height})

			_ = m.View()

			if len(*screenLines) != tt.wantScreenLines {
				t.Fatalf("screenLines count = %d, want exactly %d entries", len(*screenLines), tt.wantScreenLines)
			}
		})
	}
}

func TestContentTopPadNoOffByOne(t *testing.T) {
	tests := []struct {
		name        string
		height      int
		contentText string
		wantMaxPad  bool
	}{
		{"fits-h5", 5, "one line", true},
		{"fits-h10", 10, "line1\nline2\nline3", true},
		{"overflow-h5", 5, strings.Repeat("line\n", 10), false},
		{"overflow-h8", 8, strings.Repeat("line\n", 20), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(Config{}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: tt.height})

			m.content.AppendLine(tt.contentText)
			m.syncViewport()

			if tt.wantMaxPad {
				if m.contentTopPad < 0 {
					t.Fatalf("contentTopPad = %d, want >= 0 when content fits", m.contentTopPad)
				}
			} else {
				if m.contentTopPad > 0 {
					t.Fatalf("contentTopPad = %d, want 0 when content overflows", m.contentTopPad)
				}
			}
		})
	}
}

func TestModelPlanPickerOpenClose(t *testing.T) {
	t.Run("/implement", func(t *testing.T) {
		tmpDir := t.TempDir()
		origDir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := os.Chdir(origDir); err != nil {
				t.Fatal(err)
			}
		}()
		if err := os.Chdir(tmpDir); err != nil {
			t.Fatal(err)
		}

		m := newModel(Config{}, nil)
		m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

		// Type "/" to open the slash overlay
		m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
		if !m.slashOverlay.IsOpen() {
			t.Fatal("expected slash overlay to open")
		}

		// Type "implement"
		for _, r := range "implement" {
			m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}

		// Type space to trigger plan picker
		m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
		if !m.planPicker.IsOpen() {
			t.Fatal("expected plan picker to open after '/implement '")
		}
		if m.slashOverlay.IsOpen() {
			t.Fatal("expected slash overlay to close after triggering plan picker")
		}
		if got := m.input.Value(); got != "/implement " {
			t.Fatalf("input value = %q, want /implement ", got)
		}

		// Press Esc to close the plan picker
		m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})
		if m.planPicker.IsOpen() {
			t.Fatal("expected plan picker to close on Esc")
		}
		if got := m.input.Value(); got != "/implement " {
			t.Fatalf("input value = %q, want /implement  (unchanged)", got)
		}
	})

	t.Run("/review", func(t *testing.T) {
		tmpDir := t.TempDir()
		origDir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := os.Chdir(origDir); err != nil {
				t.Fatal(err)
			}
		}()
		if err := os.Chdir(tmpDir); err != nil {
			t.Fatal(err)
		}

		m := newModel(Config{}, nil)
		m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

		// Type "/" to open the slash overlay
		m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
		if !m.slashOverlay.IsOpen() {
			t.Fatal("expected slash overlay to open")
		}

		// Type "review"
		for _, r := range "review" {
			m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}

		// Type space to trigger plan picker
		m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
		if !m.planPicker.IsOpen() {
			t.Fatal("expected plan picker to open after '/review '")
		}
		if m.slashOverlay.IsOpen() {
			t.Fatal("expected slash overlay to close after triggering plan picker")
		}
		if got := m.input.Value(); got != "/review " {
			t.Fatalf("input value = %q, want /review ", got)
		}

		// Press Esc to close the plan picker
		m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})
		if m.planPicker.IsOpen() {
			t.Fatal("expected plan picker to close on Esc")
		}
		if got := m.input.Value(); got != "/review " {
			t.Fatalf("input value = %q, want /review  (unchanged)", got)
		}
	})
}

func TestModelWorkflowHandoffOpensModalImmediately(t *testing.T) {
	ctrl := &testController{
		workflowHandoffSelections: map[string]interactive.WorkflowHandoffModelSelection{
			"implement": {
				ModelAlias:  "implement-default",
				SourceLabel: "from handoff default",
			},
		},
	}
	m := newModel(Config{
		Model:      "current-model",
		ModelNames: []string{"current-model", "implement-default"},
		Controller: ctrl,
		SkillNames: []string{"implement", "review"},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffRequestedEvent("implement", ".steiner/plans/step-3", "handoff now")})

	if !m.workflowHandoff.IsOpen() {
		t.Fatal("expected workflow handoff modal to open")
	}
	if got := m.workflowHandoff.acceptLabel(); got != "Accept: Clear + Implement" {
		t.Fatalf("accept label = %q, want %q", got, "Accept: Clear + Implement")
	}
	rendered := ansi.Strip(m.renderWorkflowHandoffModal())
	for _, want := range []string{
		"Continue to implementation?",
		"Model: implement-default (from handoff default)",
		"Planning folder: .steiner/plans/step-3",
		"Accept: Clear + Implement",
		"Dismiss",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered modal = %q, want %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "Next:") || strings.Contains(rendered, "Target:") {
		t.Fatalf("rendered modal = %q, want no Next:/Target: rows", rendered)
	}
	if acceptIdx, dismissIdx := strings.Index(rendered, "Accept: Clear + Implement"), strings.Index(rendered, "Dismiss"); acceptIdx < 0 || dismissIdx < 0 || acceptIdx > dismissIdx {
		t.Fatalf("rendered modal = %q, want accept button before dismiss button", rendered)
	}
	if len(ctrl.submitWorkflowHandoffs()) != 0 {
		t.Fatalf("handoff decisions = %d, want 0 before input", len(ctrl.submitWorkflowHandoffs()))
	}
	if got := ctrl.switchModelActions(); len(got) != 0 {
		t.Fatalf("switch model actions = %#v, want none when modal opens", got)
	}
}

func TestModelWorkflowHandoffRendersReviewCopy(t *testing.T) {
	ctrl := &testController{
		workflowHandoffSelections: map[string]interactive.WorkflowHandoffModelSelection{
			"review": {
				ModelAlias:  "session-model",
				SourceLabel: "current session",
			},
		},
	}
	m := newModel(Config{
		Model:      "session-model",
		ModelNames: []string{"session-model"},
		Controller: ctrl,
		SkillNames: []string{"implement", "review"},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffRequestedEvent("review", ".steiner/plans/step-3", "handoff now")})

	if !m.workflowHandoff.IsOpen() {
		t.Fatal("expected workflow handoff modal to open")
	}
	if got := m.workflowHandoff.acceptLabel(); got != "Accept: Clear + Review" {
		t.Fatalf("accept label = %q, want %q", got, "Accept: Clear + Review")
	}
	rendered := ansi.Strip(m.renderWorkflowHandoffModal())
	for _, want := range []string{
		"Continue to review?",
		"Model: session-model (current session)",
		"Planning folder: .steiner/plans/step-3",
		"Accept: Clear + Review",
		"Dismiss",
		"handoff now",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered modal = %q, want %q", rendered, want)
		}
	}
	if strings.Contains(rendered, "Next:") || strings.Contains(rendered, "Target:") {
		t.Fatalf("rendered modal = %q, want no Next:/Target: rows", rendered)
	}
	if acceptIdx, dismissIdx := strings.Index(rendered, "Accept: Clear + Review"), strings.Index(rendered, "Dismiss"); acceptIdx < 0 || dismissIdx < 0 || acceptIdx > dismissIdx {
		t.Fatalf("rendered modal = %q, want accept button before dismiss button", rendered)
	}
}

func TestModelWorkflowHandoffChangeModelOpensAttachedPickerAndUpdatesSelection(t *testing.T) {
	ctrl := &testController{
		workflowHandoffSelections: map[string]interactive.WorkflowHandoffModelSelection{
			"implement": {
				ModelAlias:  "implement-default",
				SourceLabel: "from handoff default",
			},
		},
	}
	longAlias := "implementation-model-alias-with-clear-visible-suffix"
	m := newModel(Config{
		Model:      "current-model",
		ModelNames: []string{"current-model", "implement-default", longAlias},
		Controller: ctrl,
		SkillNames: []string{"implement", "review"},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffRequestedEvent("implement", ".steiner/plans/step-3", "handoff now")})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if !m.workflowHandoff.IsOpen() {
		t.Fatal("workflowHandoff.IsOpen() = false, want true while picker is open")
	}
	if !m.modelPicker.IsOpen() {
		t.Fatal("modelPicker.IsOpen() = false, want true after Change Model")
	}
	if !m.modelPicker.IsWorkflowHandoff() {
		t.Fatal("modelPicker.IsWorkflowHandoff() = false, want true for handoff picker")
	}

	rendered := ansi.Strip(m.renderWorkflowHandoffModal())
	for _, want := range []string{
		"Select model for implementation",
		"Change Model",
		longAlias,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered modal = %q, want %q", rendered, want)
		}
	}
	acceptIdx := strings.Index(rendered, "Accept: Clear + Implement")
	changeIdx := strings.Index(rendered, "Change Model")
	dismissIdx := strings.Index(rendered, "Dismiss")
	if acceptIdx < 0 || changeIdx < 0 || dismissIdx < 0 {
		t.Fatalf("rendered modal = %q, want Accept, Change Model, Dismiss in order", rendered)
	}
	if acceptIdx >= changeIdx || changeIdx >= dismissIdx {
		t.Fatalf("rendered modal = %q, want Accept, Change Model, Dismiss in order", rendered)
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.modelPicker.IsOpen() {
		t.Fatal("modelPicker.IsOpen() = true, want false after handoff selection")
	}
	if !m.workflowHandoff.IsOpen() {
		t.Fatal("workflowHandoff.IsOpen() = false, want true after returning from picker")
	}
	if got, want := m.workflowHandoff.modelAlias, longAlias; got != want {
		t.Fatalf("workflowHandoff.modelAlias = %q, want %q", got, want)
	}
	if got, want := m.workflowHandoff.modelSource, "selected for handoff"; got != want {
		t.Fatalf("workflowHandoff.modelSource = %q, want %q", got, want)
	}
	if got, want := m.primaryModel, "current-model"; got != want {
		t.Fatalf("primaryModel = %q, want %q", got, want)
	}
	if got := ctrl.switchModelActions(); len(got) != 0 {
		t.Fatalf("switch model actions = %#v, want none while handoff picker updates pending selection", got)
	}

	rendered = ansi.Strip(m.renderWorkflowHandoffModal())
	for _, want := range []string{
		"Model: " + longAlias,
		"(selected for handoff)",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered modal = %q, want updated handoff model line fragment %q", rendered, want)
		}
	}
}

func TestModelWorkflowHandoffChangeModelCancelPreservesSelection(t *testing.T) {
	ctrl := &testController{
		workflowHandoffSelections: map[string]interactive.WorkflowHandoffModelSelection{
			"review": {
				ModelAlias:  "review-default",
				SourceLabel: "from handoff default",
			},
		},
	}
	m := newModel(Config{
		Model:      "current-model",
		ModelNames: []string{"current-model", "review-default", "review-alt"},
		Controller: ctrl,
		SkillNames: []string{"implement", "review"},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffRequestedEvent("review", ".steiner/plans/step-3", "")})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	if m.modelPicker.IsOpen() {
		t.Fatal("modelPicker.IsOpen() = true, want false after Esc")
	}
	if !m.workflowHandoff.IsOpen() {
		t.Fatal("workflowHandoff.IsOpen() = false, want true after Esc cancels picker")
	}
	if got, want := m.workflowHandoff.modelAlias, "review-default"; got != want {
		t.Fatalf("workflowHandoff.modelAlias = %q, want %q after cancel", got, want)
	}
	if got, want := m.workflowHandoff.modelSource, "from handoff default"; got != want {
		t.Fatalf("workflowHandoff.modelSource = %q, want %q after cancel", got, want)
	}
	if got := ctrl.submitWorkflowHandoffs(); len(got) != 0 {
		t.Fatalf("handoff decisions = %#v, want none while cancelling picker", got)
	}
}

func TestModelWorkflowHandoffDismissDeclinesAndKeepsTranscript(t *testing.T) {
	ctrl := &testController{
		workflowHandoffSelections: map[string]interactive.WorkflowHandoffModelSelection{
			"review": {
				ModelAlias:  "session-model",
				SourceLabel: "current session",
			},
		},
	}
	m := newModel(Config{
		Model:      "session-model",
		ModelNames: []string{"session-model"},
		Controller: ctrl,
		SkillNames: []string{"implement", "review"},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.content.AppendLine("existing transcript")
	m.syncViewport()

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffRequestedEvent("review", ".steiner/plans/step-3", "")})
	rendered := ansi.Strip(m.renderWorkflowHandoffModal())
	for _, want := range []string{
		"Continue to review?",
		"Planning folder: .steiner/plans/step-3",
		"Accept: Clear + Review",
		"Dismiss",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered modal = %q, want %q", rendered, want)
		}
	}
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	if m.workflowHandoff.IsOpen() {
		t.Fatal("expected workflow handoff modal to close on Esc")
	}
	decisions := ctrl.submitWorkflowHandoffs()
	if len(decisions) != 1 || decisions[0].Decision != "dismiss" {
		t.Fatalf("handoff decisions = %#v, want one dismiss", decisions)
	}
	if got := m.content.String(m.viewport.Width); !strings.Contains(got, "existing transcript") {
		t.Fatalf("content = %q, want transcript retained", got)
	}
	if got := ctrl.submitPrompts(); len(got) != 0 {
		t.Fatalf("submit prompts = %#v, want none", got)
	}
}

func TestModelWorkflowHandoffTerminalEventsCloseModalAndRestoreFocus(t *testing.T) {
	tests := []struct {
		name  string
		event output.Event
	}{
		{
			name:  "stop reason",
			event: output.NewStopReasonEvent(1, "error", nil),
		},
		{
			name:  "run finished",
			event: output.NewRunFinishedEvent(1, "cancelled", "", "", nil),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(Config{}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
			m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffRequestedEvent("review", ".steiner/plans/step-3", "handoff now")})

			if !m.workflowHandoff.IsOpen() {
				t.Fatal("workflowHandoff.IsOpen() = false, want true before terminal event")
			}
			if m.input.Focused() {
				t.Fatal("input.Focused() = true, want false while workflow handoff is open")
			}

			m = updateModel(t, m, runtimeEventMsg{Event: tc.event})

			if m.workflowHandoff.IsOpen() {
				t.Fatal("workflowHandoff.IsOpen() = true, want false after terminal event")
			}
			if !m.input.Focused() {
				t.Fatal("input.Focused() = false, want true after terminal event")
			}
		})
	}
}

func TestModelWorkflowHandoffAcceptClearsAndLaunchesNextWorkflow(t *testing.T) {
	ctrl := &testController{
		workflowHandoffSelections: map[string]interactive.WorkflowHandoffModelSelection{
			"review": {
				ModelAlias:  "review-default",
				SourceLabel: "from handoff default",
			},
		},
	}
	m := newModel(Config{
		Model:         "current-model",
		ModelNames:    []string{"current-model", "review-default"},
		ModelBaseURLs: map[string]string{"review-default": "http://review.example/v1"},
		Controller:    ctrl,
		SkillNames:    []string{"implement", "review"},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.content.AppendLine("old transcript")
	m.syncViewport()

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffRequestedEvent("review", ".steiner/plans/step-3", "handoff now")})
	rendered := ansi.Strip(m.renderWorkflowHandoffModal())
	for _, want := range []string{
		"Continue to review?",
		"Model: review-default (from handoff default)",
		"Planning folder: .steiner/plans/step-3",
		"Accept: Clear + Review",
		"Dismiss",
		"handoff now",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered modal = %q, want %q", rendered, want)
		}
	}
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.workflowHandoff.IsOpen() {
		t.Fatal("expected workflow handoff modal to close on accept")
	}
	decisions := ctrl.submitWorkflowHandoffs()
	if len(decisions) != 1 || decisions[0].Decision != "accept" {
		t.Fatalf("handoff decisions = %#v, want one accept", decisions)
	}
	if got := m.content.String(m.viewport.Width); strings.Contains(got, "old transcript") {
		t.Fatalf("content = %q, want cleared transcript", got)
	}
	if ctrl.countSubmitPrompt() != 0 {
		t.Fatalf("submit count = %d, want 0 before workflow handoff stop", ctrl.countSubmitPrompt())
	}

	var sawSubmit, sawSwitch, sawClear, sawRotate bool
	for _, a := range ctrl.actions {
		switch a.(type) {
		case interactive.SubmitWorkflowHandoff:
			sawSubmit = true
		case interactive.SwitchModel:
			if !sawSubmit {
				t.Fatal("SwitchModel sent before SubmitWorkflowHandoff")
			}
			sawSwitch = true
		case interactive.ClearConversation:
			if !sawSwitch {
				t.Fatal("ClearConversation sent before SwitchModel")
			}
			sawClear = true
		case interactive.RotateSession:
			if !sawClear {
				t.Fatal("RotateSession sent before ClearConversation")
			}
			sawRotate = true
		}
	}
	if !sawSwitch {
		t.Fatal("SwitchModel not found in actions")
	}
	if !sawRotate {
		t.Fatal("RotateSession not found in actions")
	}
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffAcceptedEvent("review", ".steiner/plans/step-3", "handoff now")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallFinishedEvent(1, "workflow_handoff", "call_1", "", nil)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewTurnFinishedEvent(1, 1, "", "", nil)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewStopReasonEvent(1, "workflow_handoff", nil)})

	prompts := ctrl.submitPrompts()
	if len(prompts) != 1 || prompts[0].Text != ".steiner/plans/step-3" {
		t.Fatalf("submit prompts = %#v, want one prompt for target", prompts)
	}
	var sawPrompt bool
	for _, a := range ctrl.actions {
		if _, ok := a.(interactive.SubmitPrompt); ok {
			if !sawRotate {
				t.Fatal("SubmitPrompt sent before RotateSession")
			}
			sawPrompt = true
		}
	}
	if !sawPrompt {
		t.Fatal("SubmitPrompt not found in actions")
	}
	switches := ctrl.switchModelActions()
	if len(switches) != 1 || switches[0].Name != "review-default" {
		t.Fatalf("switch model actions = %#v, want one switch to review-default", switches)
	}
	if got := m.content.String(m.viewport.Width); !strings.Contains(got, "/review .steiner/plans/step-3") {
		t.Fatalf("content = %q, want launched workflow command", got)
	}
	if !m.enabledSkills["review"] {
		t.Fatal("expected review skill enabled after launch")
	}
	if got := m.primaryModel; got != "review-default" {
		t.Fatalf("primaryModel = %q, want review-default after launch", got)
	}
}

func TestModelWorkflowHandoffAcceptSwitchFailureKeepsConversationAndSkipsLaunch(t *testing.T) {
	ctrl := &testController{
		switchModelErr: fmt.Errorf("model switch failed"),
		workflowHandoffSelections: map[string]interactive.WorkflowHandoffModelSelection{
			"review": {
				ModelAlias:  "review-default",
				SourceLabel: "from handoff default",
			},
		},
	}
	m := newModel(Config{
		Model:         "current-model",
		ModelNames:    []string{"current-model", "review-default"},
		ModelBaseURLs: map[string]string{"review-default": "http://review.example/v1"},
		Controller:    ctrl,
		SkillNames:    []string{"implement", "review"},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.content.AppendLine("old transcript")
	m.syncViewport()

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffRequestedEvent("review", ".steiner/plans/step-3", "handoff now")})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	rendered := m.content.String(m.viewport.Width)
	if !m.workflowHandoff.IsOpen() {
		t.Fatal("expected workflow handoff modal to stay open on switch failure")
	}
	if !strings.Contains(rendered, "old transcript") {
		t.Fatalf("content = %q, want original transcript to remain after failed switch", rendered)
	}
	if !strings.Contains(rendered, "model switch failed") {
		t.Fatalf("content = %q, want status error after failed switch", rendered)
	}
	if got := ctrl.submitWorkflowHandoffs(); len(got) != 1 || got[0].Decision != "accept" {
		t.Fatalf("handoff decisions = %#v, want one accept", got)
	}
	if got := ctrl.switchModelActions(); len(got) != 1 || got[0].Name != "review-default" {
		t.Fatalf("switch model actions = %#v, want one switch to review-default", got)
	}
	if got := ctrl.rotateSessionActions(); len(got) != 0 {
		t.Fatalf("rotate session actions = %#v, want none after failed switch", got)
	}
	if got := ctrl.submitPrompts(); len(got) != 0 {
		t.Fatalf("submit prompts = %#v, want none after failed switch", got)
	}
	if m.pendingWorkflowHandoffLaunch != nil {
		t.Fatalf("pending workflow handoff launch = %#v, want nil after failed switch", m.pendingWorkflowHandoffLaunch)
	}
	if m.suppressWorkflowHandoffRun {
		t.Fatal("suppressWorkflowHandoffRun = true, want false after failed switch")
	}
}

func TestModelWorkflowHandoffAcceptWithCurrentSessionModelDoesNotSwitch(t *testing.T) {
	ctrl := &testController{
		workflowHandoffSelections: map[string]interactive.WorkflowHandoffModelSelection{
			"implement": {
				ModelAlias:  "current-model",
				SourceLabel: "current session",
			},
		},
	}
	m := newModel(Config{
		Model:      "current-model",
		ModelNames: []string{"current-model"},
		Controller: ctrl,
		SkillNames: []string{"implement", "review"},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffRequestedEvent("implement", ".steiner/plans/step-4", "")})
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffAcceptedEvent("implement", ".steiner/plans/step-4", "")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallFinishedEvent(1, "workflow_handoff", "call_1", "", nil)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewTurnFinishedEvent(1, 1, "", "", nil)})
	updateModel(t, m, runtimeEventMsg{Event: output.NewStopReasonEvent(1, "workflow_handoff", nil)})

	if got := ctrl.switchModelActions(); len(got) != 0 {
		t.Fatalf("switch model actions = %#v, want none for current session handoff", got)
	}
	prompts := ctrl.submitPrompts()
	if len(prompts) != 1 || prompts[0].Text != ".steiner/plans/step-4" {
		t.Fatalf("submit prompts = %#v, want one prompt for target", prompts)
	}
}

func TestModelTopLevelTerminalEventsPreserveStatusAndContentWithoutPriorBlur(t *testing.T) {
	tests := []struct {
		name        string
		event       output.Event
		wantMode    string
		wantLabel   string
		wantDetail  string
		wantContent string
	}{
		{
			name:        "stop reason",
			event:       output.NewStopReasonEvent(1, "cancelled", nil),
			wantMode:    "cancelled",
			wantLabel:   "stopped",
			wantDetail:  "cancelled",
			wantContent: "cancelled",
		},
		{
			name:        "run finished",
			event:       output.NewRunFinishedEvent(1, "error", "", "", nil),
			wantMode:    "error",
			wantLabel:   "run finished",
			wantDetail:  "error",
			wantContent: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(Config{}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
			m.content.AppendLine("existing transcript")
			m.interruptPending = true

			m = updateModel(t, m, runtimeEventMsg{Event: tc.event})

			if !m.input.Focused() {
				t.Fatal("input.Focused() = false, want true after terminal event")
			}
			if m.interruptPending {
				t.Fatal("interruptPending = true, want false after terminal event")
			}
			if got, want := m.status.mode, tc.wantMode; got != want {
				t.Fatalf("status.mode = %q, want %q", got, want)
			}
			if got, want := m.activity.label, tc.wantLabel; got != want {
				t.Fatalf("activity.label = %q, want %q", got, want)
			}
			if got, want := m.activity.detail, tc.wantDetail; got != want {
				t.Fatalf("activity.detail = %q, want %q", got, want)
			}
			rendered := m.content.String(m.viewport.Width)
			if !strings.Contains(rendered, "existing transcript") {
				t.Fatalf("content = %q, want existing transcript retained", rendered)
			}
			if tc.wantContent != "" && !strings.Contains(rendered, tc.wantContent) {
				t.Fatalf("content = %q, want terminal event content retained", rendered)
			}
		})
	}
}

func TestModelScopedTerminalEventDoesNotChangeMainComposerState(t *testing.T) {
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "bash", "prompt", `{"command":"pwd"}`)})
	if m.input.Focused() {
		t.Fatal("input.Focused() = true, want false while approval is open")
	}
	if !m.approval.active {
		t.Fatal("approval.active = false, want true before scoped event")
	}

	scoped := output.WithAgentScope(output.NewStopReasonEvent(1, "error", nil), "child-1")
	m = updateModel(t, m, runtimeEventMsg{Event: scoped})

	if !m.approval.active {
		t.Fatal("approval.active = false, want true after scoped event")
	}
	if m.input.Focused() {
		t.Fatal("input.Focused() = true, want false after scoped event")
	}
}

func TestMultiLineInputViewHeightNeverExceedsTerminal(t *testing.T) {
	heights := []int{8, 10, 12, 20, 24}
	lineCounts := []int{1, 2, 4, 6, 10, 15}
	for _, h := range heights {
		for _, n := range lineCounts {
			t.Run(fmt.Sprintf("h%d_n%d", h, n), func(t *testing.T) {
				m := newModel(Config{}, nil)
				m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: h})

				// Type n newline-separated lines by sending Alt+Enter for each newline
				val := strings.Repeat("x", 10)
				for i := 1; i < n; i++ {
					val += "\n" + strings.Repeat("x", 10)
				}
				m.input.SetValue(val)
				m.input.CursorEnd()
				// Simulate a keystroke to trigger relayoutInput
				m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
				// Undo the extra 'a' we just typed
				m.input.SetValue(val)
				m.input.CursorEnd()

				view := m.View()
				got := strings.Count(view, "\n") + 1
				if got > h {
					t.Fatalf("View() height = %d, want ≤ %d (terminal height), n=%d input lines", got, h, n)
				}
			})
		}
	}
}
