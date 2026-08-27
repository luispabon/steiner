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

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/notify"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tui/theme"
)

// testController records all actions received by Handle for test verification.
type testController struct {
	mu                        sync.Mutex
	actions                   []interactive.Action
	err                       error
	switchModelErr            error
	workflowHandoffSelections map[string]interactive.WorkflowHandoffModelSelection
	reasoningOverride         provider.ReasoningOverride
}

// CurrentReasoningOverride implements reasoningOverrideProvider for tests.
func (c *testController) CurrentReasoningOverride() provider.ReasoningOverride {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reasoningOverride
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
	t.Parallel()
	m := newModel(Config{
		Model:         "gpt-test",
		ModelContexts: map[string]int{"gpt-test": 4096},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "hello", output.ChunkSourceAssistant)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, " world", output.ChunkSourceAssistant)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextTokenBudgetEvent("conversation", 1, 100, 4096, 2, 70, 32, 164, "ok", false)})

	if got := stripANSI(m.content.String(m.viewport.Width())); !strings.Contains(got, "hello world") {
		t.Fatalf("content = %q, want assistant stream", got)
	}
	if got := m.status.model; got != "gpt-test" {
		t.Fatalf("status.model = %q, want gpt-test", got)
	}
	if got := m.status.reasoning; got != "" {
		t.Fatalf("status.reasoning = %q, want empty for non-reasoning model", got)
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
	joined := stripANSI(strings.Join(lines, "\n"))
	for _, want := range []string{
		"CONTEXT",
		"100 / 4.1k",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("sidebar = %q, want %q", joined, want)
		}
	}
	if strings.Contains(joined, "compacting") {
		t.Fatalf("sidebar = %q, want no compacting dot when idle", joined)
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

func TestModelRoutesShortContextReportToTranscript(t *testing.T) {
	t.Parallel()
	m := newModel(Config{Model: "gpt-test"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Short single-line context report should go to the transcript, not the overlay.
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewOverlayReportEvent("Context Report", "cave_human mode: on")})

	if m.contextOverlay.IsOpen() {
		t.Fatal("contextOverlay.IsOpen() = true, want overlay closed for short context report")
	}
	if got := stripANSI(m.content.String(m.viewport.Width())); !strings.Contains(got, "cave_human mode: on") {
		t.Fatalf("content = %q, want context report text in transcript", got)
	}
}

func TestModelIgnoresByteBudgetForSidebarContextFill(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if ctrl.countSubmitPrompt() != 1 {
		t.Fatalf("submit count = %d, want 1", ctrl.countSubmitPrompt())
	}

	m.input.SetValue("/skill review")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
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
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.enabledSkills["review"] {
		t.Fatal("expected review skill to be disabled")
	}
	if got := m.sidebar.activeSkill; got != "" {
		t.Fatalf("sidebar.activeSkill = %q, want empty", got)
	}
}

func TestClearResetsActiveSkill(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		SkillNames: []string{"review"},
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("/skill review")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.enabledSkills["review"] {
		t.Fatal("expected review skill to be enabled")
	}
	if got := m.sidebar.activeSkill; got != "review" {
		t.Fatalf("sidebar.activeSkill = %q, want review", got)
	}

	m.input.SetValue("/clear")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.enabledSkills["review"] {
		t.Fatal("expected review skill to be disabled after /clear")
	}
	if got := m.sidebar.activeSkill; got != "" {
		t.Fatalf("sidebar.activeSkill = %q, want empty after /clear", got)
	}
}

func TestSkillExclusivity(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		SkillNames: []string{"review", "test"},
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("/skill test")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
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
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
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
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
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
	t.Parallel()
	s := theme.Default().LipGlossStyles()
	styles := &s

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
	t.Parallel()
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
	t.Parallel()
	t.Run("finds slash token at cursor", func(t *testing.T) {
		t.Parallel()
		token, start, end, ok := composerTokenAtCursor("/con", len([]rune("/con")), '/')
		if !ok {
			t.Fatal("expected slash token to be found")
		}
		if token != "/con" || start != 0 || end != 4 {
			t.Fatalf("got token=%q start=%d end=%d, want /con 0 4", token, start, end)
		}
	})

	t.Run("finds at token after leading text", func(t *testing.T) {
		t.Parallel()
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
		t.Parallel()
		if _, _, _, ok := composerTokenAtCursor("plain text", len([]rune("plain text")), '@'); ok {
			t.Fatal("expected no @ token")
		}
	})
}

func TestModelSlashOverlayTypingUsesComposerText(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'c', Text: "c"})
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'o', Text: "o"})

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
	foundCompact := false
	for _, candidate := range m.slashOverlay.candidates {
		if candidate.command == "/config" {
			foundConfig = true
		}
		if candidate.command == "/compact" {
			foundCompact = true
		}
	}
	if !foundConfig || !foundCompact {
		t.Fatalf("candidates = %#v, want /config and /compact present", m.slashOverlay.candidates)
	}
}

func TestModelSlashOverlayEscRemovesActiveToken(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'c', Text: "c"})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})

	if m.slashOverlay.IsOpen() {
		t.Fatal("expected slash overlay to close on Esc")
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("input value = %q, want empty after Esc", got)
	}
}

func TestModelModifiedEnterInsertsNewline(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("first line")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})

	if ctrl.countSubmitPrompt() != 0 {
		t.Fatalf("submit count = %d, want 0 for modified enter", ctrl.countSubmitPrompt())
	}
	if got := m.input.Value(); got != "first line\n" {
		t.Fatalf("input value = %q, want newline inserted", got)
	}
}

func TestModelPlainEnterStillSubmitsPrompt(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("fix the bug")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if ctrl.countSubmitPrompt() != 1 {
		t.Fatalf("submit count = %d, want 1", ctrl.countSubmitPrompt())
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("input value = %q, want reset after submit", got)
	}
}

func TestModelCtrlXTogglesDelegationWhileConversationActive(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewDelegationCompleteEvent(output.DelegationCompleteParams{
		AgentID:       "child-1",
		Status:        "complete",
		TurnCount:     1,
		TokenCount:    10,
		ToolCallCount: 0,
		Output:        "result text",
	})})

	dd := m.content.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil")
	}
	if !dd.collapsed {
		t.Fatal("delegation should start collapsed")
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})

	if dd.collapsed {
		t.Fatal("delegation should expand while conversation is active")
	}
	rendered := m.content.String(m.viewport.Width())
	if !strings.Contains(rendered, "result text") {
		t.Fatalf("rendered content = %q, want expanded delegation output", rendered)
	}
}

func TestModelMouseClickTogglesDelegation(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewDelegationStartedEvent("child-1", "task preview")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, "transcript body", output.ChunkSourceAssistant), "child-1")})

	dd := m.content.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil")
	}
	if !dd.collapsed {
		t.Fatal("delegation should start collapsed")
	}

	m.content.String(m.viewport.Width())
	m.contentTopPad = 0
	m = updateModel(t, m, mouseClickMsg{x: 0, y: m.viewportContentTopOffset()})
	m = updateModel(t, m, mouseReleaseMsg{x: 0, y: m.viewportContentTopOffset()})

	if dd.collapsed {
		t.Fatal("delegation should expand on mouse click")
	}

	m.content.String(m.viewport.Width())
	m.contentTopPad = 0
	promptHeaderY := -1
	for i, row := range m.content.delegationRows(dd, m.viewport.Width()) {
		if row.kind == delegationRowPromptHeader {
			promptHeaderY = i
			break
		}
	}
	if promptHeaderY < 0 {
		t.Fatal("expected prompt header row")
	}
	m = updateModel(t, m, mouseClickMsg{x: 0, y: promptHeaderY})
	m = updateModel(t, m, mouseReleaseMsg{x: 0, y: promptHeaderY})

	if dd.collapsed {
		t.Fatal("delegation should stay expanded when prompt header toggles")
	}
	if !dd.promptCollapsed {
		t.Fatal("prompt subsection should collapse on prompt header click")
	}

	nonToggleY := -1
	for i, row := range m.content.delegationRows(dd, m.viewport.Width()) {
		if row.kind == delegationRowPromptBody || row.kind == delegationRowTranscript || row.kind == delegationRowOutput {
			nonToggleY = i
			break
		}
	}
	if nonToggleY < 0 {
		t.Fatal("expected a non-interactive delegation row to click")
	}

	m.content.String(m.viewport.Width())
	m.contentTopPad = 0
	m = updateModel(t, m, mouseClickMsg{x: 0, y: nonToggleY})
	updateModel(t, m, mouseReleaseMsg{x: 0, y: nonToggleY})

	if dd.collapsed {
		t.Fatal("transcript/body click should not collapse delegation")
	}
	if !dd.promptCollapsed {
		t.Fatal("transcript/body click should not toggle prompt subsection")
	}
}

func TestModelMouseDragDoesNotToggle(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewDelegationStartedEvent("child-1", "task")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.WithAgentScope(output.NewAssistantChunkEventWithSource(1, "body", output.ChunkSourceAssistant), "child-1")})

	dd := m.content.segments[0].delegData
	if dd == nil {
		t.Fatal("delegData = nil")
	}
	if !dd.collapsed {
		t.Fatal("delegation should start collapsed")
	}

	m.content.String(m.viewport.Width())
	m.contentTopPad = 0

	// Press at (0,0), release at (10,0) — different X = drag, should NOT toggle
	m = updateModel(t, m, mouseClickMsg{x: 0, y: 0})
	m = updateModel(t, m, mouseReleaseMsg{x: 10, y: 0})

	if !dd.collapsed {
		t.Fatal("drag (different X) should not toggle collapse")
	}

	// Press at (0,0), release at (0,2) — different Y = drag, should NOT toggle
	m = updateModel(t, m, mouseClickMsg{x: 0, y: 0})
	updateModel(t, m, mouseReleaseMsg{x: 0, y: 2})

	if !dd.collapsed {
		t.Fatal("drag (different Y) should not toggle collapse")
	}
}

func TestModelMouseClickTargetsGroupedToolRow(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "bash", "call_1", map[string]any{"command": "pwd"})})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "bash", "call_2", map[string]any{"command": "git status"})})

	group := m.content.segments[0].toolGroupData
	if group == nil {
		t.Fatal("toolGroupData = nil, want grouped tool calls")
	}

	m.content.String(m.viewport.Width())
	m.contentTopPad = 0
	m.viewport.SetYOffset(0)

	rowForSecond := -1
	dividerRow := -1
	for row := 1; row < m.content.segmentHeights[0]-1; row++ {
		switch idx := m.content.toolCallGroupEntryAtRow(group, row, m.viewport.Width()); {
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

	m = updateModel(t, m, mouseClickMsg{x: 0, y: dividerRow + clickOffset})
	m = updateModel(t, m, mouseReleaseMsg{x: 0, y: dividerRow + clickOffset})
	if !group.entries[0].collapsed || !group.entries[1].collapsed {
		t.Fatal("divider click should not toggle any grouped entry")
	}

	m.lastClickTime = m.lastClickTime.Add(-600 * time.Millisecond)

	m = updateModel(t, m, mouseClickMsg{x: 0, y: rowForSecond + clickOffset})
	updateModel(t, m, mouseReleaseMsg{x: 0, y: rowForSecond + clickOffset})

	if !group.entries[0].collapsed {
		t.Fatal("first grouped entry should remain collapsed")
	}
	if group.entries[1].collapsed {
		t.Fatal("second grouped entry should expand on row click")
	}
}

func TestModelMouseClickTargetsStandaloneToolRow(t *testing.T) {
	t.Parallel()
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

	m.content.String(m.viewport.Width())
	m.contentTopPad = 0
	m.viewport.SetYOffset(0)

	clickY := m.viewportContentTopOffset()
	m = updateModel(t, m, mouseClickMsg{x: 0, y: clickY})
	updateModel(t, m, mouseReleaseMsg{x: 0, y: clickY})

	if seg.collapsed {
		t.Fatal("standalone tool call should expand on visible row click")
	}
}

func TestModelMouseClickTargetsResumedToolRowAfterUserGap(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 14})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewUserInputEvent("resumed prompt", "resume", nil)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "bash", "call_1", map[string]any{"command": "pwd"})})

	seg := m.content.segments[1].toolData
	if seg == nil {
		t.Fatal("toolData = nil, want standalone tool call after resumed prompt")
	}
	if !seg.collapsed {
		t.Fatal("standalone tool call should start collapsed")
	}

	rendered := stripANSI(m.content.String(m.viewport.Width()))
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
	m.viewport.SetYOffset(0)
	clickY := toolRow + m.viewportContentTopOffset()
	m = updateModel(t, m, mouseClickMsg{x: 0, y: clickY})
	updateModel(t, m, mouseReleaseMsg{x: 0, y: clickY})

	if seg.collapsed {
		t.Fatal("standalone tool call should expand on visible row click after resumed prompt gap")
	}
}

func TestModelDoubleClickSelectsURLAndPaths(t *testing.T) {
	t.Parallel()
	const (
		url    = "https://github.com/luispabon/steiner/issues/356"
		path   = "internal/tui/selection.go"
		hidden = ".project_planning/2026-08-05_mcp-walking-skeleton"
	)
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 150, Height: 20})
	m.content.AppendLine("See " + url + " and " + path + " and " + hidden + " for details")
	m.syncViewport()
	m.viewport.SetYOffset(0)

	m.populateScreenLines()
	lines := m.screenLines
	left, _ := m.regionXBounds(regionViewport)

	row, urlCol, pathCol, hiddenCol := -1, -1, -1, -1
	for i, line := range lines {
		if idx := strings.Index(line, url); idx >= 0 {
			row = i
			urlCol = idx
		}
		if idx := strings.Index(line, path); idx >= 0 {
			pathCol = idx
		}
		if idx := strings.Index(line, hidden); idx >= 0 {
			hiddenCol = idx
		}
	}
	if row < 0 || urlCol < 0 || pathCol < 0 || hiddenCol < 0 {
		t.Fatalf("rendered content missing URL, path, or hidden path:\n%s", strings.Join(lines, "\n"))
	}

	// Double-click inside the URL: two consecutive clicks at the same
	// coordinates land well inside nextClickCount's 500ms / ±1 cell window.
	m = updateModel(t, m, mouseClickMsg{x: urlCol + 2, y: row})
	m = updateModel(t, m, mouseClickMsg{x: urlCol + 2, y: row})

	// The viewport selection is content-anchored: the line converts back
	// through the scroll offset and top pad, and the column is relative to the
	// content area (frame column minus the region's left bound).
	contentRow := m.contentLineAtScreenY(row)
	start, end := m.selection.canonical()
	if start.line != contentRow || end.line != contentRow || start.col != urlCol-left || end.col != urlCol+len(url)-left {
		t.Fatalf("URL selection = (%d,%d)-(%d,%d), want (%d,%d)-(%d,%d)",
			start.line, start.col, end.line, end.col, contentRow, urlCol-left, contentRow, urlCol+len(url)-left)
	}
	if got := m.extractViewportText(); got != url {
		t.Fatalf("extractViewportText = %q, want %q", got, url)
	}

	// Double-click inside the bare relative path.
	m = updateModel(t, m, mouseClickMsg{x: pathCol + 2, y: row})
	m = updateModel(t, m, mouseClickMsg{x: pathCol + 2, y: row})

	start, end = m.selection.canonical()
	if start.line != contentRow || end.line != contentRow || start.col != pathCol-left || end.col != pathCol+len(path)-left {
		t.Fatalf("path selection = (%d,%d)-(%d,%d), want (%d,%d)-(%d,%d)",
			start.line, start.col, end.line, end.col, contentRow, pathCol-left, contentRow, pathCol+len(path)-left)
	}
	if got := m.extractViewportText(); got != path {
		t.Fatalf("extractViewportText = %q, want %q", got, path)
	}

	// Double-click inside the hidden folder path.
	m = updateModel(t, m, mouseClickMsg{x: hiddenCol + 2, y: row})
	m = updateModel(t, m, mouseClickMsg{x: hiddenCol + 2, y: row})

	start, end = m.selection.canonical()
	if start.line != contentRow || end.line != contentRow || start.col != hiddenCol-left || end.col != hiddenCol+len(hidden)-left {
		t.Fatalf("hidden path selection = (%d,%d)-(%d,%d), want (%d,%d)-(%d,%d)",
			start.line, start.col, end.line, end.col, contentRow, hiddenCol-left, contentRow, hiddenCol+len(hidden)-left)
	}
	if got := m.extractViewportText(); got != hidden {
		t.Fatalf("extractViewportText = %q, want %q", got, hidden)
	}
}

func TestModelHandlesContextKeybindLocally(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m = updateModel(t, m, tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})

	if ctrl.countSubmitPrompt() != 0 {
		t.Fatalf("submit count = %d, want 0", ctrl.countSubmitPrompt())
	}
	if ctrl.countRequestContextReport() != 1 {
		t.Fatalf("context report count = %d, want 1", ctrl.countRequestContextReport())
	}
	if got := strings.TrimSpace(stripANSI(m.content.String(m.viewport.Width()))); got != "" {
		t.Fatalf("content = %q, want no local echo", got)
	}

	// Simulate the context report arriving as an event (as interactive.go would emit).
	reportContent := "# Last Request Context\nModel: `test`"
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewOverlayReportEvent("Context Report", reportContent)})

	// Overlay should open immediately with report content, not transcript.
	if !m.contextOverlay.IsOpen() {
		t.Fatal("contextOverlay.IsOpen() = false, want overlay open after context report event")
	}
	if !strings.Contains(m.contextOverlay.content, "Last Request Context") {
		t.Fatalf("contextOverlay.content = %q, want report content", m.contextOverlay.content)
	}
	if got := strings.TrimSpace(stripANSI(m.content.String(m.viewport.Width()))); got != "" {
		t.Fatalf("content = %q, want no transcript insertion for context report", got)
	}

	// Esc should close the overlay.
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.contextOverlay.IsOpen() {
		t.Fatal("contextOverlay.IsOpen() = true, want overlay closed after Esc")
	}
}

func TestModelHandlesConfigCommandLocally(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("/config")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if ctrl.countSubmitPrompt() != 0 {
		t.Fatalf("submit count = %d, want 0", ctrl.countSubmitPrompt())
	}
	if ctrl.countRequestConfigReport() != 1 {
		t.Fatalf("config report count = %d, want 1", ctrl.countRequestConfigReport())
	}
	if got := strings.TrimSpace(stripANSI(m.content.String(m.viewport.Width()))); got != "" {
		t.Fatalf("content = %q, want no local echo", got)
	}

	reportContent := "```yaml\nmodel:\n  model: gpt-test\n```"
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewOverlayReportEvent("Config", reportContent)})

	if !m.contextOverlay.IsOpen() {
		t.Fatal("contextOverlay.IsOpen() = false, want overlay open after config report event")
	}
	if got := m.contextOverlay.title; got != "Config" {
		t.Fatalf("contextOverlay.title = %q, want Config", got)
	}
	if !strings.Contains(m.contextOverlay.content, "model:") {
		t.Fatalf("contextOverlay.content = %q, want yaml content", m.contextOverlay.content)
	}
	if got := strings.TrimSpace(stripANSI(m.content.String(m.viewport.Width()))); got != "" {
		t.Fatalf("content = %q, want no transcript insertion for config report", got)
	}
}

func TestModelDisplaysFileEventInTranscript(t *testing.T) {
	t.Parallel()
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
	content := stripANSI(m.content.String(m.viewport.Width()))
	for _, want := range []string{"display file preview", "snippet.go", "package main"} {
		if !strings.Contains(content, want) {
			t.Fatalf("content = %q, want %q", content, want)
		}
	}
	if strings.Contains(m.View().Content, "file viewer") {
		t.Fatalf("view = %q, want no file viewer overlay", m.View().Content)
	}
}

func TestModelCompactEventsKeepTranscriptCleanAndRestoreIdleState(t *testing.T) {
	t.Parallel()
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
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewThinkingChunkEventWithSource(1, "thinking during compaction", output.ChunkSourceAssistant)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "raw compaction summary text", output.ChunkSourceAssistant)})
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

	if got := m.sidebar.compaction.SidebarLabel(); got != "" {
		t.Fatalf("sidebar.compaction = %q, want cleared after compaction finishes", got)
	}
	if got, want := m.status.mode, "running"; got != want {
		t.Fatalf("status.mode = %q, want %q while run remains active", got, want)
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunFinishedEvent(1, "stop", "", "", nil)})

	content := m.content.String(m.viewport.Width())
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
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 60, Height: 12})

	if got := m.activityRowHeight(m.viewport.Width()); got != 1 {
		t.Fatalf("activity row height = %d, want 1", got)
	}
	if m.viewport.Height() != 5 {
		t.Fatalf("viewport height = %d, want 5 after reserved activity row", m.viewport.Height())
	}
}

func TestModelActivityRowShowsSpinnerAfterApiRequestBeforeFirstChunk(t *testing.T) {
	t.Parallel()
	m := newModel(Config{Model: "gpt-test"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAPIRequestEvent("gpt-test", nil, nil, nil, nil, prompt.ModelTokenBudget{})})

	row := m.renderActivityRow(m.viewport.Width())
	for _, want := range []string{"waiting", "⠋"} {
		if !strings.Contains(row, want) {
			t.Fatalf("activity row = %q, want %q", row, want)
		}
	}
	if strings.Contains(row, "gpt-test") {
		t.Fatalf("activity row = %q, want no model detail", row)
	}
}

func TestModelStatusBarKeepsPrimaryModelDuringOtherRuntimeCalls(t *testing.T) {
	t.Parallel()
	m := newModel(Config{Model: "main-model"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 12})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "main-model", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAPIRequestEvent("other-runtime-model", nil, nil, nil, nil, prompt.ModelTokenBudget{})})

	statusLine := stripANSI(m.status.view(m.viewport.Width()))
	if !strings.Contains(statusLine, "model main-model") {
		t.Fatalf("status line = %q, want primary model badge", statusLine)
	}
	if strings.Contains(statusLine, "other-runtime-model") {
		t.Fatalf("status line = %q, want no runtime model override", statusLine)
	}
}

func TestModelTabCompletesModelCommandInPrompt(t *testing.T) {
	t.Parallel()
	m := newModel(Config{
		ModelNames: []string{"deepseek-v4-flash", "qwen3-coder-30b"},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 12})

	m.input.SetValue("/model")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})

	// /model (no args) should be a completion candidate
	if got, want := m.input.Value(), "/model"; got != want {
		t.Fatalf("input after tab = %q, want %q", got, want)
	}
	if got := len(m.completionCandidates); got == 0 {
		t.Fatal("completionCandidates = 0, want cached candidates")
	}
}

func TestModelActivityRowShowsToolPhaseLabel(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "bash", "call_1", map[string]any{"command": "pwd"})})

	row := m.renderActivityRow(m.viewport.Width())
	for _, want := range []string{"running tool", "bash", "⠋"} {
		if !strings.Contains(row, want) {
			t.Fatalf("activity row = %q, want %q", row, want)
		}
	}
}

func TestModelActivityRowShowsCompactionSpinner(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:            "compaction",
		Severity:        "compacting",
		CompactionCount: 2,
		Turn:            7,
	})})

	row := m.renderActivityRow(m.viewport.Width())
	for _, want := range []string{"compacting context", "2 compactions", "turn 7", "⠋"} {
		if !strings.Contains(row, want) {
			t.Fatalf("activity row = %q, want %q", row, want)
		}
	}
}

func TestModelApprovalKeepsReservedRowAndDisablesSpinner(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "write", "", "prompt", `{"path":"note.txt"}`, "path", "", "")})

	row := m.renderActivityRow(m.viewport.Width())
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
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{Controller: ctrl}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAPIRequestEvent("gpt-test", nil, nil, nil, nil, prompt.ModelTokenBudget{})})

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})

	if ctrl.countInterruptActiveRun() != 1 {
		t.Fatalf("interrupt count = %d, want 1", ctrl.countInterruptActiveRun())
	}
	if m.activity.busy() {
		t.Fatal("activity.busy = true, want false after interrupt")
	}
	if m.activity.label != "" || m.activity.detail != "" {
		t.Fatalf("activity = %#v, want cleared", m.activity)
	}
	if got := strings.ToLower(m.renderActivityRow(m.viewport.Width())); strings.Contains(got, "waiting") || strings.Contains(got, "running tool") {
		t.Fatalf("activity row = %q, want cleared", got)
	}
}

func TestModelFinishedCompactionDiagnosticDoesNotForceRunningState(t *testing.T) {
	t.Parallel()
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
	if got := m.sidebar.compaction.SidebarLabel(); got != "" {
		t.Fatalf("sidebar.compaction = %q, want cleared for finished compaction diagnostics", got)
	}
	if got := m.content.String(m.viewport.Width()); !strings.Contains(strings.ToLower(got), "compaction") {
		t.Fatalf("content = %q, want compaction banner", got)
	}
}

func TestModelSessionHealthAfterCompactionDoesNotRearmSidebarSpinner(t *testing.T) {
	t.Parallel()
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

	if got := m.sidebar.compaction.SidebarLabel(); got != "" {
		t.Fatalf("sidebar.compaction = %q, want cleared after finished session health", got)
	}
	lines := strings.Join(m.sidebar.lines(38, 50), "\n")
	if strings.Contains(lines, "compacting…") {
		t.Fatalf("sidebar = %q, want no compacting spinner after finished session health", lines)
	}
}

func TestModelSwitchFailureDoesNotUpdateUI(t *testing.T) {
	t.Parallel()
	ctrl := &testController{err: errors.New("model not found")}

	m := newModel(Config{
		Model:         "original",
		ModelContexts: map[string]int{"original": 1024},
		Controller:    ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m.applyModelSelection("original", "")

	m.input.SetValue("/model unknown")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if got, want := m.status.model, "original"; got != want {
		t.Fatalf("status.model = %q, want %q", got, want)
	}
	if got, want := m.sidebar.model, "original"; got != want {
		t.Fatalf("sidebar.model = %q, want %q", got, want)
	}
}

func TestModelPhaseTransitionUpdatesModelDisplay(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Model:         "default-model",
		ModelContexts: map[string]int{"default-model": 4096, "plan-model": 2048},
		Controller:    ctrl,
		ModelReasoningEfforts: map[string]string{
			"default-model": "low",
			"plan-model":    "medium",
		},
		ModelReasoningCapabilities: map[string]provider.ReasoningCapabilities{
			"default-model": {SupportedEfforts: []string{"low", "high"}},
			"plan-model":    {SupportedEfforts: []string{"low", "medium", "high"}},
		},
	}, nil)
	m.reasoningLabels = newReasoningLabels(
		map[string]string{"default-model": "low", "plan-model": "medium"},
		map[string]provider.ReasoningCapabilities{
			"default-model": {SupportedEfforts: []string{"low", "high"}},
			"plan-model":    {SupportedEfforts: []string{"low", "medium", "high"}},
		},
	)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})

	// Simulate a oneshot phase transition that changes the model.
	event := runtimeEventMsg{Event: output.NewPhaseTransitionEvent(
		"run-1",      // runID
		"",           // from
		"plan",       // to (phase name)
		"starting",   // status
		"plan-model", // model
		"session-1",  // sessionID
	)}
	m = updateModel(t, m, event)

	if got, want := m.primaryModel, "plan-model"; got != want {
		t.Fatalf("primaryModel = %q, want %q", got, want)
	}
	if got, want := m.status.reasoning, "medium"; got != want {
		t.Fatalf("status.reasoning = %q, want %q", got, want)
	}
	if got, want := m.status.model, "plan-model"; got != want {
		t.Fatalf("status.model = %q, want %q", got, want)
	}
	if got, want := m.sidebar.model, "plan-model"; got != want {
		t.Fatalf("sidebar.model = %q, want %q", got, want)
	}
	if got, want := m.sidebar.contextBudget, 2048; got != want {
		t.Fatalf("sidebar.contextBudget = %d, want %d", got, want)
	}
	if got, want := m.sidebar.reasoning, "medium"; got != want {
		t.Fatalf("sidebar.reasoning = %q, want %q", got, want)
	}
	if got, want := m.sidebar.promptUsed, 0; got != want {
		t.Fatalf("sidebar.promptUsed = %d, want %d", got, want)
	}
	if got, want := m.sidebar.budgetUsed, 0; got != want {
		t.Fatalf("sidebar.budgetUsed = %d, want %d", got, want)
	}
	if got, want := m.status.context, "ctx 0/2048"; got != want {
		t.Fatalf("status.context = %q, want %q", got, want)
	}
}

func TestModelSwitchUpdatesProviderHost(t *testing.T) {
	t.Parallel()
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
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

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
	t.Parallel()
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
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if !m.modelPicker.IsOpen() {
		t.Fatal("modelPicker.IsOpen() = false, want true after /model")
	}
	if m.modelPicker.IsWorkflowHandoff() {
		t.Fatal("modelPicker.IsWorkflowHandoff() = true, want false for command picker")
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

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

func TestModelOverlayKeyRoutingPreservesPriorityAndCmdBehavior(t *testing.T) {
	t.Parallel()
	type checkFunc func(*testing.T, *Model, bool, tea.Cmd)

	tests := []struct {
		name  string
		setup func(*testing.T) *Model
		key   tea.KeyPressMsg
		check checkFunc
	}{
		{
			name: "no overlay",
			setup: func(t *testing.T) *Model {
				m := newModel(Config{Model: "small", ModelNames: []string{"small", "large"}}, nil)
				m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
				m.input.SetValue("keep me")
				m.input.CursorEnd()
				return m
			},
			key: tea.KeyPressMsg{Code: tea.KeyEsc},
			check: func(t *testing.T, got *Model, handled bool, cmd tea.Cmd) {
				if handled {
					t.Fatal("handled = true, want false with no open overlays")
				}
				if cmd != nil {
					t.Fatal("cmd = non-nil, want nil with no open overlays")
				}
				if got := got.input.Value(); got != "keep me" {
					t.Fatalf("input value = %q, want unchanged when no overlay is open", got)
				}
			},
		},
		{
			name: "workflow handoff model picker priority",
			setup: func(t *testing.T) *Model {
				m := newModel(Config{Model: "small", ModelNames: []string{"small", "large"}}, nil)
				m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
				m.input.SetValue("/model")
				m.input.CursorEnd()
				m.workflowHandoff = openWorkflowHandoffModal(100, 30, output.WorkflowHandoffEvent{Next: "review", Target: ".steiner/plans/step-3"}, interactive.WorkflowHandoffModelSelection{ModelAlias: "small", SourceLabel: "current session"})
				m.modelPicker = m.modelPicker.OpenForWorkflowHandoff("Select model", []string{"small", "large"}, "small")
				m.modelPicker.width = 100
				m.modelPicker.height = 30
				return m
			},
			key: tea.KeyPressMsg{Code: tea.KeyEsc},
			check: func(t *testing.T, got *Model, handled bool, cmd tea.Cmd) {
				if !handled {
					t.Fatal("handled = false, want true for workflow handoff model picker")
				}
				if cmd != nil {
					t.Fatal("cmd = non-nil, want nil when closing workflow handoff model picker")
				}
				if got.modelPicker.IsOpen() {
					t.Fatal("modelPicker.IsOpen() = true, want false after Esc")
				}
				if !got.workflowHandoff.IsOpen() {
					t.Fatal("workflowHandoff.IsOpen() = false, want true when model picker gets Esc first")
				}
				if got := got.input.Value(); got != "/model" {
					t.Fatalf("input value = %q, want /model unchanged", got)
				}
			},
		},
		{
			name: "workflow handoff modal",
			setup: func(t *testing.T) *Model {
				m := newModel(Config{Model: "small"}, nil)
				m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
				m.workflowHandoff = openWorkflowHandoffModal(100, 30, output.WorkflowHandoffEvent{Next: "review", Target: ".steiner/plans/step-3"}, interactive.WorkflowHandoffModelSelection{ModelAlias: "small", SourceLabel: "current session"})
				return m
			},
			key: tea.KeyPressMsg{Code: tea.KeyEsc},
			check: func(t *testing.T, got *Model, handled bool, cmd tea.Cmd) {
				if !handled {
					t.Fatal("handled = false, want true for workflow handoff modal")
				}
				if cmd != nil {
					t.Fatal("cmd = non-nil, want nil when dismissing workflow handoff modal")
				}
				if got.workflowHandoff.IsOpen() {
					t.Fatal("workflowHandoff.IsOpen() = true, want false after Esc")
				}
			},
		},
		{
			name: "exit modal",
			setup: func(t *testing.T) *Model {
				m := newModel(Config{}, nil)
				m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
				m.exitModal = openExitModal(100, 30)
				return m
			},
			key: tea.KeyPressMsg{Code: tea.KeyEnter},
			check: func(t *testing.T, got *Model, handled bool, cmd tea.Cmd) {
				if !handled {
					t.Fatal("handled = false, want true for exit modal")
				}
				if cmd == nil {
					t.Fatal("cmd = nil, want confirm command for exit modal enter")
				}
				if !got.exitModal.IsOpen() {
					t.Fatal("exitModal.IsOpen() = false, want modal to remain open until runtime quits")
				}
			},
		},
		{
			name: "slash overlay",
			setup: func(t *testing.T) *Model {
				m := newModel(Config{}, nil)
				m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
				m.input.SetValue("/co")
				m.input.CursorEnd()
				m.slashOverlay = m.slashOverlay.Open(m.buildSlashOverlayItems())
				m.slashOverlay.width = 100
				m.slashOverlay.height = 30
				return m
			},
			key: tea.KeyPressMsg{Code: tea.KeyEsc},
			check: func(t *testing.T, got *Model, handled bool, cmd tea.Cmd) {
				if !handled {
					t.Fatal("handled = false, want true for slash overlay")
				}
				if cmd != nil {
					t.Fatal("cmd = non-nil, want nil when closing slash overlay")
				}
				if got.slashOverlay.IsOpen() {
					t.Fatal("slashOverlay.IsOpen() = true, want false after Esc")
				}
				if got := got.input.Value(); got != "" {
					t.Fatalf("input value = %q, want empty after removing slash token", got)
				}
			},
		},
		{
			name: "file list",
			setup: func(t *testing.T) *Model {
				root := t.TempDir()
				writeRepoFile(t, root, "file.txt", "content\n")
				m := newModel(Config{}, nil)
				m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
				m.fileList = newFileListOverlay(m.styles).Open(root)
				m.fileList.width = 100
				m.fileList.height = 30
				return m
			},
			key: tea.KeyPressMsg{Code: tea.KeyEsc},
			check: func(t *testing.T, got *Model, handled bool, cmd tea.Cmd) {
				if !handled {
					t.Fatal("handled = false, want true for file list")
				}
				if cmd != nil {
					t.Fatal("cmd = non-nil, want nil when closing file list")
				}
				if got.fileList.IsOpen() {
					t.Fatal("fileList.IsOpen() = true, want false after Esc")
				}
			},
		},
		{
			name: "context overlay",
			setup: func(t *testing.T) *Model {
				m := newModel(Config{}, nil)
				m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
				m.contextOverlay = openContextOverlay("Context Report", strings.Repeat("line\n", 40), 100, 30, m.styles, m.content.glamourStyleSheet)
				return m
			},
			key: tea.KeyPressMsg{Code: tea.KeyEsc},
			check: func(t *testing.T, got *Model, handled bool, cmd tea.Cmd) {
				if !handled {
					t.Fatal("handled = false, want true for context overlay")
				}
				if cmd != nil {
					t.Fatal("cmd = non-nil, want nil when closing context overlay")
				}
				if got.contextOverlay.IsOpen() {
					t.Fatal("contextOverlay.IsOpen() = true, want false after Esc")
				}
			},
		},
		{
			name: "file picker",
			setup: func(t *testing.T) *Model {
				m := newModel(Config{WorkingDir: "."}, nil)
				m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
				m.input.SetValue("@go")
				m.input.CursorEnd()
				m.filePicker = m.filePicker.Open(".")
				m.filePicker.width = 100
				m.filePicker.height = 30
				return m
			},
			key: tea.KeyPressMsg{Code: tea.KeyEsc},
			check: func(t *testing.T, got *Model, handled bool, cmd tea.Cmd) {
				if !handled {
					t.Fatal("handled = false, want true for file picker")
				}
				if cmd != nil {
					t.Fatal("cmd = non-nil, want nil when closing file picker")
				}
				if got.filePicker.IsOpen() {
					t.Fatal("filePicker.IsOpen() = true, want false after Esc")
				}
				if got := got.input.Value(); got != "" {
					t.Fatalf("input value = %q, want empty after removing @ token", got)
				}
			},
		},
		{
			name: "session picker",
			setup: func(t *testing.T) *Model {
				m := newModel(Config{}, nil)
				m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
				m.sessionPicker = m.sessionPicker.Open([]session.IndexEntry{{ID: "session-12345678", Title: "session", Model: "small"}})
				m.sessionPicker.width = 100
				m.sessionPicker.height = 30
				return m
			},
			key: tea.KeyPressMsg{Code: tea.KeyEsc},
			check: func(t *testing.T, got *Model, handled bool, cmd tea.Cmd) {
				if !handled {
					t.Fatal("handled = false, want true for session picker")
				}
				if cmd != nil {
					t.Fatal("cmd = non-nil, want nil when closing session picker")
				}
				if got.sessionPicker.IsOpen() {
					t.Fatal("sessionPicker.IsOpen() = true, want false after Esc")
				}
			},
		},
		{
			name: "oneshot resume picker",
			setup: func(t *testing.T) *Model {
				m := newModel(Config{}, nil)
				m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
				m.oneshotResumePicker = m.oneshotResumePicker.Open([]oneshot.ResumableRun{{RunID: "run-12345678", Task: "task", ResumePhase: "draft"}})
				m.oneshotResumePicker.width = 100
				m.oneshotResumePicker.height = 30
				return m
			},
			key: tea.KeyPressMsg{Code: tea.KeyEsc},
			check: func(t *testing.T, got *Model, handled bool, cmd tea.Cmd) {
				if !handled {
					t.Fatal("handled = false, want true for oneshot resume picker")
				}
				if cmd != nil {
					t.Fatal("cmd = non-nil, want nil when closing oneshot resume picker")
				}
				if got.oneshotResumePicker.IsOpen() {
					t.Fatal("oneshotResumePicker.IsOpen() = true, want false after Esc")
				}
			},
		},
		{
			name: "plan picker",
			setup: func(t *testing.T) *Model {
				m := newModel(Config{}, nil)
				m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
				m.planPicker = m.planPicker.Open("/implement")
				m.planPicker.width = 100
				m.planPicker.height = 30
				return m
			},
			key: tea.KeyPressMsg{Code: tea.KeyEsc},
			check: func(t *testing.T, got *Model, handled bool, cmd tea.Cmd) {
				if !handled {
					t.Fatal("handled = false, want true for plan picker")
				}
				if cmd != nil {
					t.Fatal("cmd = non-nil, want nil when closing plan picker")
				}
				if got.planPicker.IsOpen() {
					t.Fatal("planPicker.IsOpen() = true, want false after Esc")
				}
			},
		},
		{
			name: "model picker",
			setup: func(t *testing.T) *Model {
				m := newModel(Config{Model: "small", ModelNames: []string{"small", "large"}}, nil)
				m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
				m.input.SetValue("/model")
				m.input.CursorEnd()
				m.modelPicker = m.modelPicker.Open(m.modelNames, m.primaryModel)
				m.modelPicker.width = 100
				m.modelPicker.height = 30
				return m
			},
			key: tea.KeyPressMsg{Code: tea.KeyEsc},
			check: func(t *testing.T, got *Model, handled bool, cmd tea.Cmd) {
				if !handled {
					t.Fatal("handled = false, want true for model picker")
				}
				if cmd != nil {
					t.Fatal("cmd = non-nil, want nil when closing model picker")
				}
				if got.modelPicker.IsOpen() {
					t.Fatal("modelPicker.IsOpen() = true, want false after Esc")
				}
				if got := got.input.Value(); got != "" {
					t.Fatalf("input value = %q, want reset after closing model picker", got)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := tc.setup(t)
			handled, next, cmd := m.handleOverlayKeyMsg(tc.key)
			got, ok := next.(*Model)
			if !ok {
				t.Fatalf("next model type = %T, want tui.Model", next)
			}
			tc.check(t, got, handled, cmd)
		})
	}
}

func TestModelStartupSnapshotPopulatesSidebarModifiedFiles(t *testing.T) {
	t.Parallel()
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

func TestModelRefreshesGitSnapshotAfterToolAndModelCallFinishedEvents(t *testing.T) {
	t.Parallel()
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
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewModelCallFinishedEvent(output.ModelCallFinishedParams{Turn: 1, FinishReason: "stop", ToolCalls: 1})})
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
	t.Parallel()
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

// approvalCoordinatorController routes ApprovalHeadIdentity queries to a real
// ApprovalCoordinator so the model exercises FIFO tray gating.
type approvalCoordinatorController struct {
	*testController
	coordinator *interactive.ApprovalCoordinator
}

func (c *approvalCoordinatorController) ApprovalHeadIdentity() string {
	return c.coordinator.HeadIdentity()
}

func TestModelApprovalFIFOIdentityAcrossReorderAndSubmit(t *testing.T) {
	t.Parallel()
	coord := &interactive.ApprovalCoordinator{}
	ctrl := &approvalCoordinatorController{testController: &testController{}, coordinator: coord}
	m := newModel(Config{Controller: ctrl}, nil)

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "read", "call-A", nil)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "write", "call-B", nil)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "read", "call-C", nil)})

	bCh := coord.Begin("call-B", "write", "", "path", "")
	aCh := coord.Begin("call-A", "read", "", "path", "")
	cCh := coord.Begin("call-C", "read", "", "path", "")
	if got, want := coord.HeadIdentity(), "call-B"; got != want {
		t.Fatalf("coordinator head = %q, want %q", got, want)
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "read", "call-A", "prompt", "{}", "path", "", "")})
	if m.approval.active {
		t.Fatal("approval.active = true after non-head call-A request")
	}
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "read", "call-C", "prompt", "{}", "path", "", "")})
	if m.approval.active {
		t.Fatal("approval.active = true after non-head call-C request")
	}
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "write", "call-B", "prompt", "{}", "path", "", "")})
	if !m.approval.active {
		t.Fatal("approval.active = false for coordinator head call-B request")
	}
	if got, want := m.approval.identity, "call-B"; got != want {
		t.Fatalf("approval.identity = %q, want %q", got, want)
	}
	if got, want := m.approval.tool, "write"; got != want {
		t.Fatalf("approval.tool = %q, want %q", got, want)
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	submissions := ctrl.submitApprovals()
	if len(submissions) != 1 {
		t.Fatalf("approval count = %d, want 1", len(submissions))
	}
	if got, want := submissions[0].Identity, "call-B"; got != want {
		t.Fatalf("submission.Identity = %q, want %q", got, want)
	}
	if got, want := submissions[0].Tool, "write"; got != want {
		t.Fatalf("submission.Tool = %q, want %q", got, want)
	}
	if got, want := submissions[0].Decision, "allow_once"; got != want {
		t.Fatalf("submission.Decision = %q, want %q", got, want)
	}
	if got, want := submissions[0].Mode, "prompt"; got != want {
		t.Fatalf("submission.Mode = %q, want %q", got, want)
	}

	coord.Submit(interactive.SubmitApproval{Identity: "call-B", Tool: "write", Mode: "prompt", Decision: "allow_once"})
	if got, want := coord.HeadIdentity(), "call-A"; got != want {
		t.Fatalf("coordinator head after call-B = %q, want %q", got, want)
	}
	if got := <-bCh; got.Identity != "call-B" {
		t.Fatalf("call-B channel received identity %q, want %q", got.Identity, "call-B")
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalAcceptedEvent(1, "write", "call-B", "prompt", "{}", "ok", "path", "", "")})
	if !m.approval.active {
		t.Fatal("approval.active = false after call-B acceptance")
	}
	if got, want := m.approval.identity, "call-A"; got != want {
		t.Fatalf("approval.identity after call-B = %q, want %q", got, want)
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	submissions = ctrl.submitApprovals()
	if len(submissions) != 2 {
		t.Fatalf("approval count = %d, want 2", len(submissions))
	}
	if got, want := submissions[1].Identity, "call-A"; got != want {
		t.Fatalf("second submission.Identity = %q, want %q", got, want)
	}

	coord.Submit(interactive.SubmitApproval{Identity: "call-A", Tool: "read", Mode: "prompt", Decision: "allow_once"})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalAcceptedEvent(1, "read", "call-A", "prompt", "{}", "ok", "path", "", "")})
	if !m.approval.active {
		t.Fatal("approval.active = false after call-A acceptance")
	}
	if got, want := m.approval.identity, "call-C"; got != want {
		t.Fatalf("approval.identity after call-A = %q, want %q", got, want)
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	submissions = ctrl.submitApprovals()
	if len(submissions) != 3 {
		t.Fatalf("approval count = %d, want 3", len(submissions))
	}
	if got, want := submissions[2].Identity, "call-C"; got != want {
		t.Fatalf("third submission.Identity = %q, want %q", got, want)
	}

	coord.Submit(interactive.SubmitApproval{Identity: "call-C", Tool: "read", Mode: "prompt", Decision: "allow_once"})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalAcceptedEvent(1, "read", "call-C", "prompt", "{}", "ok", "path", "", "")})
	if m.approval.active {
		t.Fatal("approval.active = true after final call-C acceptance")
	}
	_ = aCh
	_ = cCh
}

func TestModelApprovalFIFOSkipsCancelledMiddleRequest(t *testing.T) {
	t.Parallel()
	coord := &interactive.ApprovalCoordinator{}
	ctrl := &approvalCoordinatorController{testController: &testController{}, coordinator: coord}
	m := newModel(Config{Controller: ctrl}, nil)

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "read", "call-A", nil)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "write", "call-B", nil)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "read", "call-C", nil)})

	bCh := coord.Begin("call-B", "write", "", "path", "")
	aCh := coord.Begin("call-A", "read", "", "path", "")
	coord.Begin("call-C", "read", "", "path", "")
	if got, want := coord.HeadIdentity(), "call-B"; got != want {
		t.Fatalf("coordinator head = %q, want %q", got, want)
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "read", "call-A", "prompt", "{}", "path", "", "")})
	if m.approval.active {
		t.Fatal("approval.active = true after non-head call-A request")
	}
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "read", "call-C", "prompt", "{}", "path", "", "")})
	if m.approval.active {
		t.Fatal("approval.active = true after non-head call-C request")
	}
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "write", "call-B", "prompt", "{}", "path", "", "")})
	if !m.approval.active {
		t.Fatal("approval.active = false for coordinator head call-B request")
	}
	if got, want := m.approval.identity, "call-B"; got != want {
		t.Fatalf("approval.identity = %q, want %q", got, want)
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	submissions := ctrl.submitApprovals()
	if len(submissions) != 1 {
		t.Fatalf("approval count = %d, want 1", len(submissions))
	}
	if got, want := submissions[0].Identity, "call-B"; got != want {
		t.Fatalf("submission.Identity = %q, want %q", got, want)
	}

	coord.Submit(interactive.SubmitApproval{Identity: "call-B", Tool: "write", Mode: "prompt", Decision: "allow_once"})
	if got, want := coord.HeadIdentity(), "call-A"; got != want {
		t.Fatalf("coordinator head after call-B = %q, want %q", got, want)
	}
	coord.Finish(aCh)
	if got, want := coord.HeadIdentity(), "call-C"; got != want {
		t.Fatalf("coordinator head after call-A cancellation = %q, want %q", got, want)
	}
	if got := <-bCh; got.Identity != "call-B" {
		t.Fatalf("call-B channel received identity %q, want %q", got.Identity, "call-B")
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalAcceptedEvent(1, "write", "call-B", "prompt", "{}", "ok", "path", "", "")})
	if !m.approval.active {
		t.Fatal("approval.active = false after call-B acceptance")
	}
	if got, want := m.approval.identity, "call-C"; got != want {
		t.Fatalf("approval.identity after cancelled call-A = %q, want %q", got, want)
	}
	select {
	case got := <-aCh:
		t.Fatalf("cancelled approval received submission for %q", got.Identity)
	default:
	}

	updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	submissions = ctrl.submitApprovals()
	if len(submissions) != 2 {
		t.Fatalf("approval count = %d, want 2", len(submissions))
	}
	if got, want := submissions[1].Identity, "call-C"; got != want {
		t.Fatalf("second submission.Identity = %q, want %q", got, want)
	}
}

func TestModelApprovalModeTransitions(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "write", "", "prompt", `{"path":"note.txt"}`, "path", "", "")})
	if !m.approval.active {
		t.Fatal("expected approval mode to be active")
	}

	m.input.SetValue("yes")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
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

func TestModelApprovalMCPRendersServerToolAndSessionButton(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "mcp__fixture__echo", "", "MCP tool call", `{"message":"hi"}`, "mcp", "fixture", "echo")})

	if got, want := m.approval.kind, "mcp"; got != want {
		t.Fatalf("approval.kind = %q, want %q", got, want)
	}
	if got, want := m.approval.server, "fixture"; got != want {
		t.Fatalf("approval.server = %q, want %q", got, want)
	}
	if got, want := m.approval.mcpToolName, "echo"; got != want {
		t.Fatalf("approval.mcpToolName = %q, want %q", got, want)
	}

	rendered := m.content.String(m.viewport.Width())
	for _, want := range []string{"fixture → echo", "Allowed for session", "Message:", "hi"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("content = %q, missing %q", rendered, want)
		}
	}
}

func TestModelThinkingToggleShowsOnlyAfterToggle(t *testing.T) {
	t.Parallel()
	m := newModel(Config{
		ShowThinking: false,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewThinkingChunkEventWithSource(1, "normal reasoning", output.ChunkSourceAssistant)})

	if got := m.content.String(m.viewport.Width()); strings.Contains(got, "normal reasoning") {
		t.Fatalf("content = %q, want no thinking while toggle is off", got)
	}

	m = updateModel(t, m, toggleThinkingMsg{})

	content := m.content.String(m.viewport.Width())
	if !strings.Contains(content, "normal reasoning") {
		t.Fatalf("content = %q, want visible normal thinking after toggle", content)
	}
}

func TestContextDiagnosticsHiddenByDefault(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextTokenBudgetEvent("conversation", 1, 100, 4096, 2, 70, 32, 200, "ok", false)})
	m.syncViewport()

	got := m.visibleViewportContent()
	if strings.Contains(got, "context info:") || strings.Contains(got, "prompt_tokens=") {
		t.Fatalf("viewport = %q, want no context diagnostics when debug disabled", got)
	}
}

func TestModelApprovalEnterAllowedWhileStreaming(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "streaming", output.ChunkSourceAssistant)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "write", "", "prompt", `{"path":"note.txt"}`, "path", "", "")})

	m.input.SetValue("yes")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

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
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "bash", "", "prompt", `{"command":"pwd"}`, "path", "", "")})

	if got, want := m.approval.selectedAction, 0; got != want {
		t.Fatalf("selectedAction = %d, want %d", got, want)
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if got, want := m.approval.selectedAction, 1; got != want {
		t.Fatalf("selectedAction after tab = %d, want %d", got, want)
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

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
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "write", "", "prompt", `{"path":"note.txt"}`, "path", "", "")})
	m.input.SetValue("stale text")

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})

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
	t.Parallel()
	for _, key := range []tea.KeyPressMsg{{Code: 'c', Mod: tea.ModCtrl}, {Code: 'd', Mod: tea.ModCtrl}} {
		t.Run(key.Keystroke(), func(t *testing.T) {
			t.Parallel()
			ctrl := &testController{}
			m := newModel(Config{Controller: ctrl}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
			m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
			m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "bash", "", "prompt", `{"command":"pwd"}`, "path", "", "")})
			if !m.approval.active {
				t.Fatal("approval.active = false, want true")
			}

			m = updateModel(t, m, key)

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
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	if !m.input.Focused() {
		t.Fatal("input.Focused() = false, want true at start")
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "bash", "", "prompt", `{"command":"pwd"}`, "path", "", "")})
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
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "mutate", "", "prompt", `{"path":"note.txt"}`, "path", "", "")})
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
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "streaming", output.ChunkSourceAssistant)})
	m.input.SetValue("stale")

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})

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
	if !strings.Contains(m.content.String(m.viewport.Width()), "interrupted") {
		t.Fatal("expected interrupted marker in content")
	}
}

func TestModelEscClosesHelpDuringActiveConversation(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "streaming", output.ChunkSourceAssistant)})

	// Open help via ? key during active conversation.
	m = updateModel(t, m, tea.KeyPressMsg{Code: '?', Text: "?"})
	if !m.helpVisible {
		t.Fatal("helpVisible = false, want true after ? key")
	}

	// ESC should close help, not interrupt.
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})

	if m.helpVisible {
		t.Fatal("helpVisible = true, want false after ESC")
	}
	if ctrl.countInterruptActiveRun() != 0 {
		t.Fatalf("interrupt count = %d, want 0 (help should close, not interrupt)", ctrl.countInterruptActiveRun())
	}
	// Streaming should still be active (help closed, agent not interrupted).
	if m.content.streamingPhase == "" {
		t.Fatal("streamingPhase empty, want still streaming after help closed")
	}
}

func TestModelIdleCtrlCOpensExitModalInsteadOfQuitting(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	// Idle state: Ctrl+C should open exit modal, not quit.
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	updated, ok := next.(*Model)
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
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	updated, ok := next.(*Model)
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
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd when no OnExitRequested callback")
	}
}

func TestModelExitModalCancelClosesWithoutExiting(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !m.exitModal.IsOpen() {
		t.Fatal("exitModal.IsOpen() = false, want modal open")
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	updated := m

	if updated.exitModal.IsOpen() {
		t.Fatal("exitModal.IsOpen() = true, want modal closed")
	}
	if ctrl.countRequestExit() != 0 {
		t.Fatalf("exit request count = %d, want 0", ctrl.countRequestExit())
	}
}

func TestModelExitModalExitRequestsQuit(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !m.exitModal.IsOpen() {
		t.Fatal("exitModal.IsOpen() = false, want modal open")
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	updated := m

	if ctrl.countRequestExit() != 1 {
		t.Fatalf("exit request count = %d, want 1", ctrl.countRequestExit())
	}
	if !updated.exitModal.IsOpen() {
		t.Fatal("exitModal.IsOpen() = false, want modal to remain open until runtime quits")
	}
}

func TestModelCtrlCInterruptsStreamingInsteadOfQuitting(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "streaming", output.ChunkSourceAssistant)})

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	updated, ok := next.(*Model)
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
	t.Parallel()
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

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})

	if ctrl.countInterruptActiveRun() != 1 {
		t.Fatalf("interrupt count = %d, want 1", ctrl.countInterruptActiveRun())
	}
	if got := m.status.mode; got != "" {
		t.Fatalf("status.mode = %q, want cleared after interrupt", got)
	}
}

func TestModelEscInterruptsToolPhase(t *testing.T) {
	t.Parallel()
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

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})

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
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "bash", "call_0", map[string]any{"command": "git diff"})})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallStartedEvent(1, "bash", "call_1", map[string]any{"command": "git status"})})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalAcceptedEvent(1, "bash", "", "prompt", `{"command":"git status"}`, "approved", "path", "", "")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "still streaming", output.ChunkSourceAssistant)})

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
	if strings.Contains(m.content.String(m.viewport.Width()), "running tool") {
		t.Fatal("expected stale tool activity to be suppressed after interrupt")
	}

	// ToolCallFinishedEvent must NOT be suppressed during interrupt.
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallFinishedEvent(1, "bash", "call_0", "{}", nil)})

	// Verify the tool segment for call_0 was updated with completion meta.
	foundCall := false
	for _, seg := range m.content.segments {
		if seg.kind == segmentToolCall && seg.toolData != nil && seg.toolData.callID == "call_0" {
			foundCall = true
			if seg.toolData.meta != "✓" {
				t.Fatalf("tool segment meta = %q, want ✓ after ToolCallFinishedEvent", seg.toolData.meta)
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
	rendered := m.content.String(m.viewport.Width())
	if !strings.Contains(rendered, "cancelled") {
		t.Fatal("expected cancelled stop reason to remain visible")
	}
	if !strings.Contains(rendered, "status") {
		t.Fatal("expected cancelled stop reason to render with the status tag")
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(2, "fresh run", output.ChunkSourceAssistant)})
	if got := m.content.streamingPhase; got != "answer" {
		t.Fatalf("streamingPhase = %q, want answer after next run resumes", got)
	}
}

func TestModelStreamingEnterQueuesSteerPrompt(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "streaming", output.ChunkSourceAssistant)})
	m.input.SetValue("steer message")

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

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

func TestModelStreamingEnterRendersSteerImmediately(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "streaming", output.ChunkSourceAssistant)})
	m.input.SetValue("queued steer text")

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	// The viewport content must immediately show the queued steer box.
	viewportContent := m.visibleViewportContent()
	if !strings.Contains(viewportContent, "queued steer text") {
		t.Fatalf("viewport content does not contain queued steer text immediately after Enter.\nViewport:\n%s", viewportContent)
	}
}

func TestModelStreamingEmptyEnterIsNoop(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "streaming", output.ChunkSourceAssistant)})
	// Leave input empty.

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

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
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "streaming", output.ChunkSourceAssistant)})
	m.input.SetValue("my steer")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

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
	t.Parallel()
	ctrl := &testController{}

	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m.input.SetValue("first line")

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})

	if got := m.input.Value(); got != "first line\n" {
		t.Fatalf("input value = %q, want newline inserted", got)
	}
	if ctrl.countSubmitPrompt() != 0 {
		t.Fatalf("submit count = %d, want 0 on modified enter", ctrl.countSubmitPrompt())
	}
}

func TestModelResizeAndMouseScroll(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 40, Height: 6})
	for i := 0; i < 20; i++ {
		m.content.AppendLine("line")
	}
	m.syncViewport()
	m.viewport.GotoBottom()
	start := m.viewport.YOffset()

	m = updateModel(t, m, mouseWheelMsg{direction: "up"})
	if m.viewport.YOffset() >= start {
		t.Fatalf("yOffset = %d, want less than %d after wheel up", m.viewport.YOffset(), start)
	}
	if m.autoScroll {
		t.Fatal("expected autoScroll to disable after upward scroll")
	}

	m = updateModel(t, m, tea.WindowSizeMsg{Width: 60, Height: 12})
	// ContentPane: PaddingLeft(3)+PaddingRight(3) → viewport.Width = 60-6 = 54
	// Layout rows: top_pad(1) + viewport + hDivider(1) + input(3, with padding 1) + activity(1) + status(1) → viewport.Height = 12-7 = 5
	if m.viewport.Width() != 54 {
		t.Fatalf("viewport width = %d, want 54 after pane chrome", m.viewport.Width())
	}
	if got := m.input.Width(); got != 56 {
		t.Fatalf("input width = %d, want 56 (inner composer width at 60-col terminal)", got)
	}
	if m.viewport.Height() != 5 {
		t.Fatalf("viewport height = %d, want 5 after pane chrome", m.viewport.Height())
	}
}

func TestModelOnMouseDispatchesWheelEvents(t *testing.T) {
	t.Parallel()
	upCmd := classifyMouse(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}))
	if upCmd == nil {
		t.Fatal("upCmd = nil, want wheel dispatch command")
	}
	upMsg, ok := upCmd().(mouseWheelMsg)
	if !ok {
		t.Fatalf("upCmd() type = %T, want mouseWheelMsg", upCmd())
	}
	if upMsg.direction != "up" {
		t.Fatalf("up direction = %q, want up", upMsg.direction)
	}

	downCmd := classifyMouse(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown}))
	if downCmd == nil {
		t.Fatal("downCmd = nil, want wheel dispatch command")
	}
	downMsg, ok := downCmd().(mouseWheelMsg)
	if !ok {
		t.Fatalf("downCmd() type = %T, want mouseWheelMsg", downCmd())
	}
	if downMsg.direction != "down" {
		t.Fatalf("down direction = %q, want down", downMsg.direction)
	}
}

func TestModelOnMouseIgnoresHoverMotion(t *testing.T) {
	t.Parallel()
	if cmd := classifyMouse(tea.MouseMotionMsg(tea.Mouse{X: 4, Y: 2})); cmd != nil {
		t.Fatalf("hover motion cmd = %v, want nil", cmd)
	}
}

func TestModelIgnoresStructuredMouseLeakRunes(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
			m := newModel(Config{WorkingDir: "."}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
			m.input.SetValue("seed")

			m = updateModel(t, m, tea.KeyPressMsg{Code: rune(fragment[0]), Text: fragment})

			if got := m.input.Value(); got != "seed" {
				t.Fatalf("input value = %q, want unchanged", got)
			}
		})
	}
}

func TestModelAllowsNormalRuneInputNearMouseLikeText(t *testing.T) {
	t.Parallel()
	tests := []string{"[", "[abc", "<tag>", "65;foo"}

	for _, text := range tests {
		t.Run(text, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

			m = updateModel(t, m, tea.KeyPressMsg{Code: rune(text[0]), Text: text})

			if got := m.input.Value(); got != text {
				t.Fatalf("input value = %q, want %q", got, text)
			}
		})
	}
}

func TestModelIgnoresBareBracketMousePrefixAfterWheel(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, mouseWheelMsg{direction: "up"})
	m = updateModel(t, m, tea.KeyPressMsg{Text: "[[["})

	if got := m.input.Value(); got != "" {
		t.Fatalf("input value = %q, want empty after wheel leak prefix", got)
	}
}

func TestModelAllowsBareBracketOutsideRecentWheelWindow(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.lastWheelMouseAt = time.Now().Add(-time.Second)

	m = updateModel(t, m, tea.KeyPressMsg{Code: '[', Text: "["})

	if got := m.input.Value(); got != "[" {
		t.Fatalf("input value = %q, want [", got)
	}
}

func TestModelListFilesOpensOverlayWithWorkingDir(t *testing.T) {
	t.Parallel()
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.input.SetValue("/ls")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.fileList.IsOpen() {
		t.Fatal("expected file list overlay to open after /ls")
	}
	if m.fileList.root != "." {
		t.Fatalf("file list root = %q, want .", m.fileList.root)
	}
	if len(m.fileList.entries) == 0 {
		t.Fatal("expected non-empty file list")
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.fileList.IsOpen() {
		t.Fatal("expected file list overlay to close after Esc")
	}
}

func TestModelListFilesOpensWithPath(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.input.SetValue("/ls .")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.fileList.IsOpen() {
		t.Fatal("expected file list overlay to open after /ls .")
	}
	if len(m.fileList.entries) == 0 {
		t.Fatal("expected non-empty file list for .")
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.fileList.IsOpen() {
		t.Fatal("expected file list overlay to close after Enter")
	}
}

func TestModelFilePickerOverlayInView(t *testing.T) {
	t.Parallel()
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyPressMsg{Code: '@', Text: "@"})
	if !m.filePicker.IsOpen() {
		t.Fatal("expected file picker to open after @")
	}

	view := stripANSI(m.View().Content)
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
	t.Parallel()
	m := newModel(Config{}, nil)
	m.input.SetValue("asdasd")
	m.input.SetCursorColumn(len([]rune("asdasd")))

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
	t.Parallel()
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
			// Not parallel: subtests share and mutate m.input's cursor
			// position, which is unsafe to do concurrently.
			m.input.SetCursorColumn(tt.absPos)
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
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 40, Height: 10})

	innerWidth := m.inputInnerWidth(40)

	// 50-char single-line input wraps into 2 segments: 36 + 14
	val := strings.Repeat("y", 50)
	m.input.SetValue(val)

	// After SetValue cursor is at end; press left arrow 3 times via textarea update
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})

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
	t.Parallel()
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

func updateModel(t *testing.T, m *Model, msg tea.Msg) *Model {
	t.Helper()

	next, _ := m.Update(msg)
	updated, ok := next.(*Model)
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

// fakeNotifier records Notify calls for test assertions.
type fakeNotifier struct {
	mu     sync.Mutex
	calls  []notify.Notification
	avail  bool
	reason string
}

func (f *fakeNotifier) Notify(_ context.Context, n notify.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, n)
	return nil
}

func (f *fakeNotifier) Availability() (bool, string) {
	return f.avail, f.reason
}

func (f *fakeNotifier) snapshot() []notify.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]notify.Notification, len(f.calls))
	copy(out, f.calls)
	return out
}

func TestNotifyApprovalEventFiresNotification(t *testing.T) {
	t.Parallel()
	fn := &fakeNotifier{avail: true}
	m := newModel(Config{WorkingDir: "/home/user/myproject", Notifier: fn}, nil)
	m.sidebar.branch = "main"

	_ = m.applyEvent(output.NewApprovalRequestedEvent(1, "bash", "", "approve", "some preview", "path", "", ""))

	time.Sleep(20 * time.Millisecond)

	calls := fn.snapshot()
	if len(calls) != 1 {
		t.Fatalf("got %d Notify calls, want 1", len(calls))
	}
	if calls[0].Project != "myproject" {
		t.Errorf("Project = %q, want %q", calls[0].Project, "myproject")
	}
	if calls[0].Branch != "main" {
		t.Errorf("Branch = %q, want %q", calls[0].Branch, "main")
	}
	if !strings.Contains(calls[0].Reason, "bash") {
		t.Errorf("Reason = %q, want it to contain %q", calls[0].Reason, "bash")
	}
}

func TestNotifyWorkflowHandoffFiresNotification(t *testing.T) {
	t.Parallel()
	fn := &fakeNotifier{avail: true}
	m := newModel(Config{WorkingDir: "/home/user/myproject", Notifier: fn}, nil)
	m.sidebar.branch = "main"

	_ = m.applyEvent(output.NewWorkflowHandoffRequestedEvent("next", "target", "message", ""))

	time.Sleep(20 * time.Millisecond)

	calls := fn.snapshot()
	if len(calls) != 1 {
		t.Fatalf("got %d Notify calls, want 1", len(calls))
	}
	if calls[0].Reason != "workflow handoff requested" {
		t.Errorf("Reason = %q, want %q", calls[0].Reason, "workflow handoff requested")
	}
}

func TestNotifyNilNotifierIsSafe(t *testing.T) {
	t.Parallel()
	m := newModel(Config{WorkingDir: "/home/user/myproject"}, nil)
	// must not panic
	_ = m.applyEvent(output.NewApprovalRequestedEvent(1, "bash", "", "approve", "preview", "path", "", ""))
}

func TestNotifyUnavailableEmitsStartupWarning(t *testing.T) {
	t.Parallel()
	fn := &fakeNotifier{avail: false, reason: "no daemon found"}
	m := newModel(Config{WorkingDir: "/home/user/myproject", Notifier: fn}, nil)

	found := false
	for _, seg := range m.content.segments {
		if strings.Contains(seg.text, "desktop notifications: no daemon found") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected startup warning in content buffer, none found")
	}
}

func TestModelFilePicker_TabInsertsPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fixture.txt"), []byte("fixture"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	m := newModel(Config{WorkingDir: dir}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyPressMsg{Code: '@', Text: "@"})
	if !m.filePicker.IsOpen() {
		t.Fatal("expected file picker to open")
	}
	if len(m.filePicker.candidates) == 0 {
		t.Fatal("expected file picker candidates from fixture directory")
	}

	selected := m.filePicker.candidates[m.filePicker.selection]

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.filePicker.IsOpen() {
		t.Fatal("expected file picker to close after Tab")
	}

	val := m.input.Value()
	if !strings.HasPrefix(val, selected+" ") && val != selected {
		t.Fatalf("expected input to start with %q, got %q", selected+" ", val)
	}
}

func TestModelSlashOverlay_TabInsertsCommand(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
	if !m.slashOverlay.IsOpen() {
		t.Fatal("expected slash overlay to open")
	}
	if len(m.slashOverlay.candidates) == 0 {
		t.Skip("no candidates")
	}

	selected := m.slashOverlay.candidates[m.slashOverlay.selection]

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.slashOverlay.IsOpen() {
		t.Fatal("expected slash overlay to close after Tab")
	}

	val := m.input.Value()
	if !strings.HasPrefix(val, selected.command+" ") {
		t.Fatalf("expected input to start with %q, got %q", selected.command+" ", val)
	}
}

func TestModelSlashOverlay_TypedAccentSpaceOpensPicker(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{name: "rune space", key: tea.KeyPressMsg{Code: ' ', Text: " "}},
		{name: "key space", key: tea.KeyPressMsg{Code: tea.KeySpace}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

			m = updateModel(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
			for _, r := range "accent" {
				m = updateModel(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			if !m.slashOverlay.IsOpen() {
				t.Fatal("expected slash overlay to stay open after /accent")
			}

			m = updateModel(t, m, tt.key)
			if !m.accentPicker.IsOpen() {
				t.Fatal("expected accent picker to open after '/accent '")
			}
			if m.slashOverlay.IsOpen() {
				t.Fatal("expected slash overlay to close after triggering accent picker")
			}
			if got := m.input.Value(); got != "/accent " {
				t.Fatalf("input value = %q, want /accent ", got)
			}
		})
	}
}

func TestModelSlashOverlay_SelectAccentOpensPicker(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{name: "Tab", key: tea.KeyPressMsg{Code: tea.KeyTab}},
		{name: "Enter", key: tea.KeyPressMsg{Code: tea.KeyEnter}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

			m = updateModel(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
			for _, r := range "accent" {
				m = updateModel(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			selected := m.slashOverlay.SelectedItem()
			if selected == nil || selected.command != "/accent" {
				t.Fatalf("selected slash item = %#v, want /accent", selected)
			}

			m = updateModel(t, m, tt.key)
			if !m.accentPicker.IsOpen() {
				t.Fatal("expected accent picker to open after selecting /accent")
			}
			if m.slashOverlay.IsOpen() {
				t.Fatal("expected slash overlay to close after selecting /accent")
			}
			if got := m.input.Value(); got != "/accent " {
				t.Fatalf("input value = %q, want /accent ", got)
			}
		})
	}
}

func TestModelFilePicker_ReopensAfterSpaceBackspace(t *testing.T) {
	t.Parallel()
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyPressMsg{Code: '@', Text: "@"})
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'g', Text: "g"})
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'o', Text: "o"})

	if !m.filePicker.IsOpen() {
		t.Fatal("expected file picker open after @go")
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: ' ', Text: " "})
	if m.filePicker.IsOpen() {
		t.Fatal("expected file picker to close after space")
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if !m.filePicker.IsOpen() {
		t.Fatal("expected file picker to re-open after backspace")
	}
	if got := m.filePicker.query; got != "go" {
		t.Fatalf("picker query = %q, want go", got)
	}
}

func TestModelSlashOverlay_ReopensAfterSpaceBackspace(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'c', Text: "c"})
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'o', Text: "o"})

	if !m.slashOverlay.IsOpen() {
		t.Fatal("expected slash overlay open after /co")
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: ' ', Text: " "})
	if m.slashOverlay.IsOpen() {
		t.Fatal("expected slash overlay to close after space")
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if !m.slashOverlay.IsOpen() {
		t.Fatal("expected slash overlay to re-open after backspace")
	}
}

func TestModelFilePicker_ReopensOnLeftArrow(t *testing.T) {
	t.Parallel()
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyPressMsg{Code: '@', Text: "@"})
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'g', Text: "g"})
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'o', Text: "o"})

	if !m.filePicker.IsOpen() {
		t.Fatal("expected file picker open after @go")
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: ' ', Text: " "})
	if m.filePicker.IsOpen() {
		t.Fatal("expected file picker to close after space")
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	if !m.filePicker.IsOpen() {
		t.Fatal("expected file picker to re-open after left arrow")
	}
}

func TestModelFilePicker_NoReopenAfterEsc(t *testing.T) {
	t.Parallel()
	m := newModel(Config{WorkingDir: "."}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, tea.KeyPressMsg{Code: '@', Text: "@"})
	m = updateModel(t, m, tea.KeyPressMsg{Code: 'g', Text: "g"})

	if !m.filePicker.IsOpen() {
		t.Fatal("expected file picker open after @g")
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.filePicker.IsOpen() {
		t.Fatal("expected file picker to close on Esc")
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.filePicker.IsOpen() {
		t.Fatal("expected file picker to NOT re-open after Esc removed the token")
	}
}

func TestFrameHeightClamping(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
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
	t.Parallel()
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
			t.Parallel()
			m := newModel(Config{}, nil)

			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: tt.height})

			// Activate selection so screenLines is populated during View().
			m.selection.active = true

			_ = m.View().Content

			if len(m.screenLines) != tt.wantScreenLines {
				t.Fatalf("screenLines count = %d, want exactly %d entries", len(m.screenLines), tt.wantScreenLines)
			}
		})
	}
}

func TestContentTopPadNoOffByOne(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
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
		m = updateModel(t, m, tea.KeyPressMsg{Code: '/', Text: "/"})
		if !m.slashOverlay.IsOpen() {
			t.Fatal("expected slash overlay to open")
		}

		// Type "implement"
		for _, r := range "implement" {
			m = updateModel(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
		}

		// Type space to trigger plan picker
		m = updateModel(t, m, tea.KeyPressMsg{Code: ' ', Text: " "})
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
		m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
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
		m = updateModel(t, m, tea.KeyPressMsg{Code: '/', Text: string('/')})
		if !m.slashOverlay.IsOpen() {
			t.Fatal("expected slash overlay to open")
		}

		// Type "review"
		for _, r := range "review" {
			m = updateModel(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
		}

		// Type space to trigger plan picker
		m = updateModel(t, m, tea.KeyPressMsg{Code: ' ', Text: string(' ')})
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
		m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})
		if m.planPicker.IsOpen() {
			t.Fatal("expected plan picker to close on Esc")
		}
		if got := m.input.Value(); got != "/review " {
			t.Fatalf("input value = %q, want /review  (unchanged)", got)
		}
	})
}

func TestModelWorkflowHandoffOpensModalImmediately(t *testing.T) {
	t.Parallel()
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

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffRequestedEvent("implement", ".steiner/plans/step-3", "handoff now", "")})

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
	t.Parallel()
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

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffRequestedEvent("review", ".steiner/plans/step-3", "handoff now", "")})

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
	t.Parallel()
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

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffRequestedEvent("implement", ".steiner/plans/step-3", "handoff now", "")})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

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

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

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
	t.Parallel()
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

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffRequestedEvent("review", ".steiner/plans/step-3", "", "")})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})

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
	t.Parallel()
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

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffRequestedEvent("review", ".steiner/plans/step-3", "", "")})
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
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})

	if m.workflowHandoff.IsOpen() {
		t.Fatal("expected workflow handoff modal to close on Esc")
	}
	decisions := ctrl.submitWorkflowHandoffs()
	if len(decisions) != 1 || decisions[0].Decision != "dismiss" {
		t.Fatalf("handoff decisions = %#v, want one dismiss", decisions)
	}
	if got := m.content.String(m.viewport.Width()); !strings.Contains(got, "existing transcript") {
		t.Fatalf("content = %q, want transcript retained", got)
	}
	if got := ctrl.submitPrompts(); len(got) != 0 {
		t.Fatalf("submit prompts = %#v, want none", got)
	}
}

func TestModelWorkflowHandoffTerminalEventsCloseModalAndRestoreFocus(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
			m := newModel(Config{}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
			m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffRequestedEvent("review", ".steiner/plans/step-3", "handoff now", "")})

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
	t.Parallel()
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

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffRequestedEvent("review", ".steiner/plans/step-3", "handoff now", "")})
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
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.workflowHandoff.IsOpen() {
		t.Fatal("expected workflow handoff modal to close on accept")
	}
	decisions := ctrl.submitWorkflowHandoffs()
	if len(decisions) != 1 || decisions[0].Decision != "accept" {
		t.Fatalf("handoff decisions = %#v, want one accept", decisions)
	}
	if got := m.content.String(m.viewport.Width()); strings.Contains(got, "old transcript") {
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
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewModelCallFinishedEvent(output.ModelCallFinishedParams{Turn: 1, ToolCalls: 1})})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewStopReasonEvent(1, "workflow_handoff", nil)})

	prompts := ctrl.submitPrompts()
	if len(prompts) != 1 || prompts[0].Text != "/review .steiner/plans/step-3" {
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
	if got := stripANSI(m.content.String(m.viewport.Width())); !strings.Contains(got, "/review .steiner/plans/step-3") {
		t.Fatalf("content = %q, want launched workflow command", got)
	}
	if !m.enabledSkills["review"] {
		t.Fatal("expected review skill enabled after launch")
	}
	if got := m.primaryModel; got != "review-default" {
		t.Fatalf("primaryModel = %q, want review-default after launch", got)
	}
}

func TestModelWorkflowHandoffAcceptLaunchesLiteralPromptForBuildTarget(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}
	m := newModel(Config{
		Model:      "current-model",
		ModelNames: []string{"current-model"},
		Controller: ctrl,
		SkillNames: []string{},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.content.AppendLine("old transcript")
	m.syncViewport()

	submission := "Implement the plan at .steiner/plans/step-9/plan.md. It is the complete record of what was agreed — read it before making any changes."
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffRequestedEvent("build", ".steiner/plans/step-9", "handoff now", submission)})
	rendered := ansi.Strip(m.renderWorkflowHandoffModal())
	for _, want := range []string{
		"Continue to the next workflow?",
		"Planning folder: .steiner/plans/step-9",
		"Accept",
		"Dismiss",
		"handoff now",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered modal = %q, want %q", rendered, want)
		}
	}
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.workflowHandoff.IsOpen() {
		t.Fatal("expected workflow handoff modal to close on accept")
	}
	decisions := ctrl.submitWorkflowHandoffs()
	if len(decisions) != 1 || decisions[0].Decision != "accept" {
		t.Fatalf("handoff decisions = %#v, want one accept", decisions)
	}
	if got := m.content.String(m.viewport.Width()); strings.Contains(got, "old transcript") {
		t.Fatalf("content = %q, want cleared transcript", got)
	}
	if ctrl.countSubmitPrompt() != 0 {
		t.Fatalf("submit count = %d, want 0 before workflow handoff stop", ctrl.countSubmitPrompt())
	}

	var sawSubmit, sawClear, sawRotate bool
	for _, a := range ctrl.actions {
		switch a.(type) {
		case interactive.SubmitWorkflowHandoff:
			sawSubmit = true
		case interactive.ClearConversation:
			if !sawSubmit {
				t.Fatal("ClearConversation sent before SubmitWorkflowHandoff")
			}
			sawClear = true
		case interactive.RotateSession:
			if !sawClear {
				t.Fatal("RotateSession sent before ClearConversation")
			}
			sawRotate = true
		}
	}
	if !sawClear {
		t.Fatal("ClearConversation not found in actions")
	}
	if !sawRotate {
		t.Fatal("RotateSession not found in actions")
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffAcceptedEvent("build", ".steiner/plans/step-9", "handoff now")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallFinishedEvent(1, "workflow_handoff", "call_1", "", nil)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewModelCallFinishedEvent(output.ModelCallFinishedParams{Turn: 1, ToolCalls: 1})})
	updateModel(t, m, runtimeEventMsg{Event: output.NewStopReasonEvent(1, "workflow_handoff", nil)})

	prompts := ctrl.submitPrompts()
	if len(prompts) != 1 || prompts[0].Text != submission {
		t.Fatalf("submit prompts = %#v, want one prompt with literal submission text %q", prompts, submission)
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
}

func TestModelWorkflowHandoffAcceptSwitchFailureKeepsConversationAndSkipsLaunch(t *testing.T) {
	t.Parallel()
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

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffRequestedEvent("review", ".steiner/plans/step-3", "handoff now", "")})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	rendered := m.content.String(m.viewport.Width())
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
	t.Parallel()
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

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffRequestedEvent("implement", ".steiner/plans/step-4", "", "")})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewWorkflowHandoffAcceptedEvent("implement", ".steiner/plans/step-4", "")})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewToolCallFinishedEvent(1, "workflow_handoff", "call_1", "", nil)})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewModelCallFinishedEvent(output.ModelCallFinishedParams{Turn: 1, ToolCalls: 1})})
	updateModel(t, m, runtimeEventMsg{Event: output.NewStopReasonEvent(1, "workflow_handoff", nil)})

	if got := ctrl.switchModelActions(); len(got) != 0 {
		t.Fatalf("switch model actions = %#v, want none for current session handoff", got)
	}
	prompts := ctrl.submitPrompts()
	if len(prompts) != 1 || prompts[0].Text != "/implement .steiner/plans/step-4" {
		t.Fatalf("submit prompts = %#v, want one prompt for target", prompts)
	}
}

func TestModelTopLevelTerminalEventsPreserveStatusAndContentWithoutPriorBlur(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
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
			rendered := m.content.String(m.viewport.Width())
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
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewApprovalRequestedEvent(1, "bash", "", "prompt", `{"command":"pwd"}`, "path", "", "")})
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
	t.Parallel()
	heights := []int{8, 10, 12, 20, 24}
	lineCounts := []int{1, 2, 4, 6, 10, 15}
	for _, h := range heights {
		for _, n := range lineCounts {
			t.Run(fmt.Sprintf("h%d_n%d", h, n), func(t *testing.T) {
				t.Parallel()
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
				m = updateModel(t, m, tea.KeyPressMsg{Code: 'a', Text: "a"})
				// Undo the extra 'a' we just typed
				m.input.SetValue(val)
				m.input.CursorEnd()

				view := m.View().Content
				got := strings.Count(view, "\n") + 1
				if got > h {
					t.Fatalf("View() height = %d, want ≤ %d (terminal height), n=%d input lines", got, h, n)
				}
			})
		}
	}
}

func TestContentStringCacheInvalidationOnDirtySegment(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Add content and populate cache.
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "test content", output.ChunkSourceAssistant)})
	firstCall := m.content.String(80)

	// Mark a segment dirty and verify cache is invalidated.
	if len(m.content.segments) > 0 {
		m.content.segments[0].renderDirty = true
		secondCall := m.content.String(80)
		if firstCall == secondCall {
			t.Fatal("content.String cache should invalidate when segment is dirty")
		}
	}
}

func TestContentStringCacheInvalidationOnWidthChange(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Add long content that wraps differently at different widths.
	longContent := strings.Repeat("word ", 100)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, longContent, output.ChunkSourceAssistant)})
	result1 := m.content.String(40)
	width1 := m.content.stringCacheWidth

	// Call at different width.
	result2 := m.content.String(80)
	width2 := m.content.stringCacheWidth

	// Verify cache key changed and results differ.
	if width1 == width2 {
		t.Fatal("stringCacheWidth should track width changes")
	}
	if result1 == result2 {
		t.Fatal("content.String output should differ at different widths")
	}
}

func TestHiddenThinkingSegmentCleared(t *testing.T) {
	// Verify that renderDirty is cleared on hidden thinking segments
	// (the fix for Phase 4 cache being defeated by hidden thinking blocks).
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Add a thinking block segment.
	m.content.segments = append(m.content.segments, contentSegment{
		kind:        segmentThinkingBlock,
		text:        "thinking content",
		renderDirty: true,
		thinkData: &thinkingBlockData{
			body: "thinking content",
		},
	})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "regular content", output.ChunkSourceAssistant)})

	// With thinking hidden, skipHiddenSegment should clear renderDirty.
	m.content.showThinking = false
	m.content.String(80)

	// Verify renderDirty was cleared on the hidden thinking segment.
	if m.content.segments[0].renderDirty {
		t.Fatal("renderDirty should be cleared on hidden thinking segments")
	}
}

func TestContentStringCacheInvalidationOnActiveDelegation(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, "test", output.ChunkSourceAssistant)})
	m.content.streaming = false // Stop streaming so cache behavior is measurable.
	cache1 := m.content.String(80)

	// Create actual delegation segment with active status.
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewDelegationStartedEvent("agent_1", "test task")})
	cache2 := m.content.String(80)

	// With active delegation, checkBufferDirty returns true (forces rebuild).
	if !m.content.checkBufferDirty(80) {
		t.Fatal("checkBufferDirty should return true when active delegation exists")
	}
	if cache1 == cache2 {
		t.Fatal("cache should invalidate when delegation becomes active")
	}
}

func TestScrollbarCacheInvalidationOnScroll(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// Add enough content to require scrollbar.
	longContent := strings.Repeat("line of content\n", 100)
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewAssistantChunkEventWithSource(1, longContent, output.ChunkSourceAssistant)})

	// Prepare viewport content.
	m.syncViewport()

	// Get scrollbar at offset 0.
	m.viewport.SetYOffset(0)
	key1 := m.scrollbarCacheKey
	scroll1 := m.renderScrollbar()

	// Scroll to different position and verify cache key is different.
	m.viewport.SetYOffset(10)
	key2 := m.scrollbarCacheKey
	scroll2 := m.renderScrollbar()

	if key1 == key2 {
		t.Fatal("scrollbar cache key should differ when YOffset changes")
	}
	if scroll1 == scroll2 {
		t.Fatal("scrollbar rendering should differ after scroll")
	}
}

func TestStripTrailingResetSupportsLipglossV2ShortReset(t *testing.T) {
	t.Parallel()
	input := "styled\x1b[m"
	if got := stripTrailingReset(input); got != "styled" {
		t.Fatalf("stripTrailingReset(%q) = %q, want %q", input, got, "styled")
	}
}

func TestPasteGate_CapableModel_AllowsPaste(t *testing.T) {
	t.Parallel()
	vc := agent.NewVisionCapabilities(false)
	vc.SetDerived("gpt-4", agent.VisionCapable)

	m := newModel(Config{
		Model:              "gpt-4",
		ModelNames:         []string{"gpt-4"},
		Controller:         &testController{},
		VisionCapabilities: vc,
	}, nil)
	m.primaryModel = "gpt-4"

	msg := tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl}
	_, _, cmd := m.handleNavigationKeyMsg(msg)
	if cmd == nil {
		t.Fatal("cmd is nil, want pasteImageCmd")
	}
}

func TestPasteGate_UnknownModel_AllowsPaste(t *testing.T) {
	t.Parallel()
	vc := agent.NewVisionCapabilities(false)

	m := newModel(Config{
		Model:              "unknown-model",
		ModelNames:         []string{"unknown-model"},
		Controller:         &testController{},
		VisionCapabilities: vc,
	}, nil)
	m.primaryModel = "unknown-model"

	msg := tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl}
	_, _, cmd := m.handleNavigationKeyMsg(msg)
	if cmd == nil {
		t.Fatal("cmd is nil, want pasteImageCmd")
	}
}

func TestPasteGate_IncapableModelWithSubAgent_AllowsPaste(t *testing.T) {
	t.Parallel()
	vc := agent.NewVisionCapabilities(true)
	vc.LatchIncapable("deepseek")

	m := newModel(Config{
		Model:              "deepseek",
		ModelNames:         []string{"deepseek"},
		Controller:         &testController{},
		VisionCapabilities: vc,
	}, nil)
	m.primaryModel = "deepseek"

	msg := tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl}
	_, _, cmd := m.handleNavigationKeyMsg(msg)
	if cmd == nil {
		t.Fatal("cmd is nil, want pasteImageCmd")
	}
}

func TestPasteGate_IncapableModelNoSubAgent_BlocksPaste(t *testing.T) {
	t.Parallel()
	vc := agent.NewVisionCapabilities(false)
	vc.LatchIncapable("deepseek")

	m := newModel(Config{
		Model:              "deepseek",
		ModelNames:         []string{"deepseek"},
		Controller:         &testController{},
		VisionCapabilities: vc,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.primaryModel = "deepseek"

	msg := tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl}
	_, nextModel, cmd := m.handleNavigationKeyMsg(msg)
	if cmd != nil {
		t.Fatal("cmd is not nil, want nil (blocked)")
	}
	next := nextModel.(*Model)
	content := stripANSI(next.content.String(80))
	if !strings.Contains(content, "can't view images") {
		t.Fatalf("content = %q, want message about images not supported", content)
	}
	if !strings.Contains(content, "vision") {
		t.Fatalf("content = %q, want mention of vision sub-agent", content)
	}
}

func TestPasteGate_DisabledCapabilities_AllowsPaste(t *testing.T) {
	t.Parallel()
	m := newModel(Config{
		Model:              "test-model",
		ModelNames:         []string{"test-model"},
		Controller:         &testController{},
		VisionCapabilities: nil,
	}, nil)
	m.primaryModel = "test-model"

	msg := tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl}
	_, _, cmd := m.handleNavigationKeyMsg(msg)
	if cmd == nil {
		t.Fatal("cmd is nil, want pasteImageCmd (gate disabled)")
	}
}

func TestModelReasoningResolvedMsg_UpdatesCapabilitiesEffortsAndLabels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		initialCaps      map[string]provider.ReasoningCapabilities
		initialEfforts   map[string]string
		primaryModel     string // backend model ID, set via Config.Model
		modelAlias       string // config alias, set via Config.CurrentModelAlias; keys reasoningLabels
		resolvedCaps     map[string]provider.ReasoningCapabilities
		resolvedEfforts  map[string]string
		wantCapabilities map[string]provider.ReasoningCapabilities
		wantEfforts      map[string]string
		wantSidebarLabel string
	}{
		{
			// primaryModel (backend ID) deliberately differs from modelAlias
			// to catch indexing reasoningLabels by the wrong field.
			name:           "empty to populated",
			initialCaps:    map[string]provider.ReasoningCapabilities{},
			initialEfforts: map[string]string{},
			primaryModel:   "gpt-4-0613",
			modelAlias:     "gpt4",
			resolvedCaps: map[string]provider.ReasoningCapabilities{
				"gpt4": {
					SupportedEfforts:      []string{"low", "medium", "high"},
					ProviderDefaultEffort: "medium",
				},
			},
			resolvedEfforts: map[string]string{
				"gpt4": "high",
			},
			wantCapabilities: map[string]provider.ReasoningCapabilities{
				"gpt4": {
					SupportedEfforts:      []string{"low", "medium", "high"},
					ProviderDefaultEffort: "medium",
				},
			},
			wantEfforts: map[string]string{
				"gpt4": "high",
			},
			wantSidebarLabel: "high",
		},
		{
			name: "override existing",
			initialCaps: map[string]provider.ReasoningCapabilities{
				"gpt4": {
					SupportedEfforts: []string{"low", "high"},
				},
			},
			initialEfforts: map[string]string{
				"gpt4": "low",
			},
			primaryModel: "gpt-4-0613",
			modelAlias:   "gpt4",
			resolvedCaps: map[string]provider.ReasoningCapabilities{
				"gpt4": {
					SupportedEfforts:      []string{"low", "medium", "high"},
					ProviderDefaultEffort: "medium",
				},
			},
			resolvedEfforts: map[string]string{
				"gpt4": "medium",
			},
			wantCapabilities: map[string]provider.ReasoningCapabilities{
				"gpt4": {
					SupportedEfforts:      []string{"low", "medium", "high"},
					ProviderDefaultEffort: "medium",
				},
			},
			wantEfforts: map[string]string{
				"gpt4": "medium",
			},
			wantSidebarLabel: "medium",
		},
		{
			name: "default (no explicit effort)",
			initialCaps: map[string]provider.ReasoningCapabilities{
				"claude": {
					SupportedEfforts: []string{"low", "high"},
				},
			},
			initialEfforts: map[string]string{},
			primaryModel:   "claude-3-opus-20240229",
			modelAlias:     "claude",
			resolvedCaps: map[string]provider.ReasoningCapabilities{
				"claude": {
					SupportedEfforts:      []string{"low", "medium", "high"},
					ProviderDefaultEffort: "medium",
				},
			},
			resolvedEfforts: map[string]string{},
			wantCapabilities: map[string]provider.ReasoningCapabilities{
				"claude": {
					SupportedEfforts:      []string{"low", "medium", "high"},
					ProviderDefaultEffort: "medium",
				},
			},
			wantEfforts:      map[string]string{},
			wantSidebarLabel: "default",
		},
		{
			name:           "no reasoning capability",
			initialCaps:    map[string]provider.ReasoningCapabilities{},
			initialEfforts: map[string]string{},
			primaryModel:   "llama-3-70b",
			modelAlias:     "llama",
			resolvedCaps: map[string]provider.ReasoningCapabilities{
				"llama": {
					SupportedEfforts: []string{},
				},
			},
			resolvedEfforts: map[string]string{},
			wantCapabilities: map[string]provider.ReasoningCapabilities{
				"llama": {
					SupportedEfforts: []string{},
				},
			},
			wantEfforts:      map[string]string{},
			wantSidebarLabel: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newModel(Config{
				Model:                      tt.primaryModel,
				ModelNames:                 []string{tt.modelAlias},
				CurrentModelAlias:          tt.modelAlias,
				ModelReasoningCapabilities: tt.initialCaps,
				ModelReasoningEfforts:      tt.initialEfforts,
				Controller:                 &testController{},
			}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

			msg := modelReasoningResolvedMsg{
				capabilities: tt.resolvedCaps,
				efforts:      tt.resolvedEfforts,
			}
			m = updateModel(t, m, msg)

			// Check capabilities were updated
			if len(m.modelReasoningCapabilities) != len(tt.wantCapabilities) {
				t.Fatalf("len(modelReasoningCapabilities) = %d, want %d",
					len(m.modelReasoningCapabilities), len(tt.wantCapabilities))
			}
			for name, want := range tt.wantCapabilities {
				got, ok := m.modelReasoningCapabilities[name]
				if !ok {
					t.Fatalf("modelReasoningCapabilities missing %s", name)
				}
				if got.ProviderDefaultEffort != want.ProviderDefaultEffort {
					t.Fatalf("modelReasoningCapabilities[%s].ProviderDefaultEffort = %q, want %q",
						name, got.ProviderDefaultEffort, want.ProviderDefaultEffort)
				}
				if len(got.SupportedEfforts) != len(want.SupportedEfforts) {
					t.Fatalf("len(modelReasoningCapabilities[%s].SupportedEfforts) = %d, want %d",
						name, len(got.SupportedEfforts), len(want.SupportedEfforts))
				}
			}

			// Check efforts were updated
			if len(m.modelReasoningEfforts) != len(tt.wantEfforts) {
				t.Fatalf("len(modelReasoningEfforts) = %d, want %d",
					len(m.modelReasoningEfforts), len(tt.wantEfforts))
			}
			for name, want := range tt.wantEfforts {
				got, ok := m.modelReasoningEfforts[name]
				if !ok {
					t.Fatalf("modelReasoningEfforts missing %s", name)
				}
				if got != want {
					t.Fatalf("modelReasoningEfforts[%s] = %q, want %q", name, got, want)
				}
			}

			// Check reasoning labels were rebuilt
			if got, want := m.reasoningLabels[tt.modelAlias], tt.wantSidebarLabel; got != want {
				t.Fatalf("reasoningLabels[%s] = %q, want %q", tt.modelAlias, got, want)
			}

			// Check sidebar reasoning label was updated
			if got, want := m.sidebar.reasoning, tt.wantSidebarLabel; got != want {
				t.Fatalf("sidebar.reasoning = %q, want %q", got, want)
			}

			if got, want := m.status.reasoning, tt.wantSidebarLabel; got != want {
				t.Fatalf("status.reasoning = %q, want %q", got, want)
			}
			badge := stripANSI(renderModelBadge(m.styles, m.status.model, m.status.reasoning))
			if !strings.Contains(badge, m.status.model+"/"+tt.wantSidebarLabel) && tt.wantSidebarLabel != "" {
				t.Fatalf("status model badge = %q, want effort suffix %q", badge, "/"+tt.wantSidebarLabel)
			}
		})
	}
}

func TestPasteMsgRelayoutsInput(t *testing.T) {
	t.Parallel()
	m := newModel(Config{
		Model:         "test-model",
		ModelContexts: map[string]int{"test-model": 4096},
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	heightBefore := m.viewport.Height()

	multiline := "line1\nline2\nline3\nline4\nline5"
	m = updateModel(t, m, tea.PasteMsg{Content: multiline})

	if m.input.Value() != multiline {
		t.Fatalf("input.Value() = %q, want %q", m.input.Value(), multiline)
	}

	heightAfter := m.viewport.Height()
	if heightAfter >= heightBefore {
		t.Fatalf("viewport height should shrink after paste: before=%d after=%d", heightBefore, heightAfter)
	}
}

// ---------------------------------------------------------------------------
// Content-anchored viewport selection and drag auto-scroll
// ---------------------------------------------------------------------------

// selectionViewportModel returns a layout-consistent model with a scrollable
// viewport of 30 content lines (contentTopPad 0, so content lines and viewport
// lines agree) scrolled to the given offset. The model is laid out at height 17
// so the real viewport height is 10 and clampToRegion's viewport y bound
// (inputStartRow-3) agrees with the viewport bottom row.
func selectionViewportModel(t *testing.T, yOffset int) *Model {
	t.Helper()
	m := buildTestModel(100, 17, false, false)
	m.layout()
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d", i)
	}
	m.setViewportContent(strings.Join(lines, "\n"))
	m.contentTopPad = 0
	m.viewport.SetYOffset(yOffset)
	m.activeRegion = regionViewport
	return m
}

func TestSelectionFollowsScroll(t *testing.T) {
	t.Parallel()
	m := buildTestModel(100, 30, false, false)
	m.viewport.SetHeight(8)
	content := []string{"line 00", "line 01", "line 02", "line 03", "line 04", "line 05", "line 06", "line 07", "line 08", "line 09"}
	m.setViewportContent(strings.Join(append([]string{"", "", ""}, content...), "\n"))
	m.contentTopPad = 3
	m.activeRegion = regionViewport
	m.selection = selectionState{start: selectionPoint{2, 0}, end: selectionPoint{5, 7}, active: true}

	// At YOffset 1 content line c sits at frame row c + 3.
	m.viewport.SetYOffset(1)
	screen := m.screenSelection()
	if screen.start.line != 5 || screen.end.line != 8 {
		t.Errorf("at YOffset 1: screenSelection rows = %d-%d; want 5-8", screen.start.line, screen.end.line)
	}
	if got := m.extractViewportText(); got != "line 02\nline 03\nline 04\nline 05" {
		t.Errorf("at YOffset 1: extractViewportText = %q", got)
	}

	// Scrolling down keeps the same content selected; only the projected rows move.
	m.viewport.SetYOffset(3)
	screen = m.screenSelection()
	if screen.start.line != 3 || screen.end.line != 6 {
		t.Errorf("at YOffset 3: screenSelection rows = %d-%d; want 3-6", screen.start.line, screen.end.line)
	}
	if got := m.extractViewportText(); got != "line 02\nline 03\nline 04\nline 05" {
		t.Errorf("at YOffset 3: extractViewportText = %q", got)
	}
}

func TestDragEdgeAutoScrollUp(t *testing.T) {
	t.Parallel()
	m := selectionViewportModel(t, 10)
	m = updateModel(t, m, mouseClickMsg{x: 5, y: 5})
	if got := m.selection.start.line; got != 14 {
		t.Fatalf("selection start line = %d; want 14", got)
	}

	// Drag to the top edge: arm the auto-scroll tick.
	m = updateModel(t, m, mouseMotionMsg{x: 5, y: 0})
	if m.dragScrollDir != -1 {
		t.Fatalf("dragScrollDir = %d; want -1", m.dragScrollDir)
	}
	if !m.dragScrollTicking {
		t.Fatal("dragScrollTicking = false; want true")
	}
	if got := m.viewport.YOffset(); got != 10 {
		t.Fatalf("YOffset before tick = %d; want 10", got)
	}

	// Each tick scrolls up one line and extends the selection upward.
	next, cmd := m.handleDragAutoScrollTick(dragAutoScrollTickMsg{epoch: m.dragScrollEpoch})
	m = next.(*Model)
	if cmd == nil {
		t.Fatal("tick returned nil cmd; want re-arm")
	}
	if got := m.viewport.YOffset(); got != 9 {
		t.Errorf("YOffset after tick = %d; want 9", got)
	}
	if got := m.selection.end.line; got != 8 {
		t.Errorf("selection end line = %d; want 8", got)
	}
	if got := m.selection.start.line; got != 14 {
		t.Errorf("selection start line moved; want 14, got %d", got)
	}
}

func TestDragEdgeAutoScrollDown(t *testing.T) {
	t.Parallel()
	m := selectionViewportModel(t, 5)
	m = updateModel(t, m, mouseClickMsg{x: 5, y: 5})
	if got := m.selection.start.line; got != 9 {
		t.Fatalf("selection start line = %d; want 9", got)
	}

	// Drag past the bottom edge: arm the auto-scroll tick.
	m = updateModel(t, m, mouseMotionMsg{x: 5, y: 15})
	if m.dragScrollDir != 1 {
		t.Fatalf("dragScrollDir = %d; want 1", m.dragScrollDir)
	}
	if !m.dragScrollTicking {
		t.Fatal("dragScrollTicking = false; want true")
	}

	next, cmd := m.handleDragAutoScrollTick(dragAutoScrollTickMsg{epoch: m.dragScrollEpoch})
	m = next.(*Model)
	if cmd == nil {
		t.Fatal("tick returned nil cmd; want re-arm")
	}
	if got := m.viewport.YOffset(); got != 6 {
		t.Errorf("YOffset after tick = %d; want 6", got)
	}
	// The drag is clamped to the viewport bottom row (10), so one scroll down
	// puts the selection end at content line 10 + YOffset - 1 = 15.
	if got := m.selection.end.line; got != 15 {
		t.Errorf("selection end line = %d; want 15", got)
	}
	if m.autoScroll {
		t.Error("autoScroll = true; want false after drag auto-scroll")
	}
}

func TestDragAutoScrollTickStopsOnRelease(t *testing.T) {
	t.Parallel()

	// tickModel returns a model with scrollable viewport content at YOffset 5
	// and a matching drag epoch, so a tick that wrongly scrolls is detectable
	// and the epoch guard does not short-circuit the branch under test.
	tickModel := func() *Model {
		m := buildTestModel(100, 30, false, false)
		m.viewport.SetHeight(10)
		m.setViewportContent(strings.Repeat("line\n", 29) + "line")
		m.viewport.SetYOffset(5)
		m.dragScrollEpoch = 1
		return m
	}

	// No press in flight: the tick is a no-op, stops ticking, and does not scroll.
	m := tickModel()
	m.mousePressX = -1
	m.dragScrollDir = -1
	m.dragScrollTicking = true
	next, cmd := m.handleDragAutoScrollTick(dragAutoScrollTickMsg{epoch: 1})
	if cmd != nil {
		t.Errorf("tick without press returned cmd %T; want nil", cmd)
	}
	if next.(*Model).dragScrollTicking {
		t.Error("dragScrollTicking = true; want false after no-press tick")
	}
	if got := next.(*Model).viewport.YOffset(); got != 5 {
		t.Errorf("no-press tick scrolled to YOffset %d; want 5", got)
	}

	// Press in flight but no edge hover: same no-op.
	m = tickModel()
	m.mousePressX = 3
	m.dragScrollDir = 0
	m.dragScrollTicking = true
	next, cmd = m.handleDragAutoScrollTick(dragAutoScrollTickMsg{epoch: 1})
	if cmd != nil {
		t.Errorf("tick with dir 0 returned cmd %T; want nil", cmd)
	}
	if next.(*Model).dragScrollTicking {
		t.Error("dragScrollTicking = true; want false after dir-0 tick")
	}
	if got := next.(*Model).viewport.YOffset(); got != 5 {
		t.Errorf("dir-0 tick scrolled to YOffset %d; want 5", got)
	}

	// Stale tick from a previous drag (epoch mismatch): ignored without
	// touching the current drag state, so ticking stays armed and no scroll
	// happens.
	m = tickModel()
	m.dragScrollEpoch = 0
	m.mousePressX = 3
	m.dragScrollDir = -1
	m.dragScrollTicking = true
	next, cmd = m.handleDragAutoScrollTick(dragAutoScrollTickMsg{epoch: 1})
	if cmd != nil {
		t.Errorf("stale tick returned cmd %T; want nil", cmd)
	}
	if !next.(*Model).dragScrollTicking {
		t.Error("dragScrollTicking = false; want true (stale tick must not touch current drag state)")
	}
	if got := next.(*Model).viewport.YOffset(); got != 5 {
		t.Errorf("stale tick scrolled to YOffset %d; want 5", got)
	}
}

// viewportAnchorForContentLine anchors a viewport content line to its rendered
// segment row, or returns an empty anchor when the line maps to no segment.
// Test-only helper for the viewport-selection tests.
func (m *Model) viewportAnchorForContentLine(line int) selectionAnchor {
	segIndex, rowInSeg, ok := m.content.segmentAtContentLine(line)
	if !ok {
		return selectionAnchor{}
	}
	return m.content.selectionAnchorForSegmentRow(segIndex, rowInSeg)
}
func TestViewportSelectionSurvivesContentAppendBelow(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.content.AppendLine("first")
	m.syncViewport()

	m = updateModel(t, m, mouseClickMsg{x: 3, y: 5})
	m = updateModel(t, m, mouseMotionMsg{x: 10, y: 5})
	if !m.selection.startAnchor.ok || !m.selection.endAnchor.ok {
		t.Fatal("click/motion did not anchor the selection to the content segment")
	}

	// New content streams in below the selected line: the selection must
	// survive and keep extracting the originally selected text.
	m.content.AppendLine("second")
	m.syncViewport()

	if !m.selection.hasSelection() {
		t.Error("viewport selection was cleared by appending content below")
	}
	if m.mousePressX < 0 {
		t.Error("mouse press was cancelled by appending content below")
	}
	if got := m.extractViewportText(); got != "first" {
		t.Errorf("extractViewportText = %q; want %q", got, "first")
	}
}

// findUserLineAndUnmappableLine locates the rendered content line containing
// needle, its segment index, and the first unmappable line after it. In these
// fixtures the unmappable line is the blank user separator. Test-only helper
// for the viewport-selection tests.
func findUserLineAndUnmappableLine(t *testing.T, m *Model, needle string) (userLine, userSeg, blankLine int) {
	t.Helper()
	renderedLines := strings.Split(m.content.String(m.viewport.Width()), "\n")
	userLine, userSeg, blankLine = -1, -1, -1
	for i := range renderedLines {
		segIndex, _, ok := m.content.segmentAtContentLine(i)
		if !ok {
			if blankLine < 0 && userLine >= 0 {
				blankLine = i
			}
			continue
		}
		if strings.Contains(ansi.Strip(renderedLines[i]), needle) {
			userLine, userSeg = i, segIndex
		}
	}
	return userLine, userSeg, blankLine
}

func TestViewportSelectionSurvivesDragEndOnUserSeparator(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.content.AppendUser("select this user line")
	m.content.AppendEvent(output.NewDelegationStartedEvent("child-1", "do work"))
	m.syncViewport()

	// The blank separator line between the user segment and the delegation box
	// maps to no segment. A drag ending there must snap to the nearest mappable
	// line (the user segment row) at anchor capture so the endpoint stays
	// anchored when content shifts.
	userLine, userSeg, blankLine := findUserLineAndUnmappableLine(t, m, "select this user line")
	if userLine < 0 {
		t.Fatal("user segment text line not found in rendered content")
	}
	if blankLine < 0 {
		t.Fatal("blank separator line between user segment and delegation box not found")
	}

	endLine, endAnchor := m.viewportSelectionEndpoint(blankLine)
	if !endAnchor.ok {
		t.Fatal("viewportSelectionEndpoint(blankLine) returned an unanchored endpoint; want it to snap to the user segment row")
	}
	if endLine == blankLine {
		t.Fatal("viewportSelectionEndpoint(blankLine) kept the blank line; want it to snap to a mappable line")
	}
	if segIndex, _, ok := m.content.segmentAtContentLine(endLine); !ok || segIndex != userSeg {
		t.Errorf("snapped end line %d maps to segment %d (ok=%v); want user segment %d", endLine, segIndex, ok, userSeg)
	}

	startLine, startAnchor := m.viewportSelectionEndpoint(userLine)
	m.activeRegion = regionViewport
	m.selection = selectionState{
		start:       selectionPoint{line: startLine, col: 0},
		end:         selectionPoint{line: endLine, col: 0},
		active:      true,
		startAnchor: startAnchor,
		endAnchor:   endAnchor,
	}

	// The collapsed delegation header re-renders on a spinner tick, which must
	// remap the anchored endpoints instead of clearing the selection. Assert the
	// spinner tick actually changes the render so this exercises a real remap.
	before := m.content.String(m.viewport.Width())
	m.content.AdvanceDelegationSpinners()
	m.syncViewport()
	after := m.content.String(m.viewport.Width())
	if before == after {
		t.Fatal("test setup: delegation spinner tick did not change the render")
	}

	if !m.selection.hasSelection() {
		t.Fatal("viewport selection was cleared by the delegation spinner re-render")
	}
	if got := m.extractViewportText(); !strings.Contains(got, "select this user line") {
		t.Errorf("extractViewportText = %q; want it to contain the selected user text", got)
	}
}

func TestViewportSelectionRemapsWhenContentShiftsAboveUnanchoredEndpoint(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.showThinking = true
	m.content.showThinking = true
	m.content.segments = []contentSegment{
		{kind: segmentThinkingBlock, thinkData: &thinkingBlockData{body: "secret reasoning", collapsed: true}, renderDirty: true},
	}
	m.content.AppendUser("select this user line")
	m.content.AppendEvent(output.NewDelegationStartedEvent("child-1", "do work"))
	m.syncViewport()

	// The blank separator below the user segment maps to no segment; the drag
	// endpoint there snaps to the user segment row at anchor capture.
	userLine, userSeg, blankLine := findUserLineAndUnmappableLine(t, m, "select this user line")
	if userLine < 0 || userSeg < 0 {
		t.Fatal("user segment text line not found in rendered content")
	}
	if blankLine < 0 {
		t.Fatal("blank separator line below the user segment not found")
	}

	startLine, startAnchor := m.viewportSelectionEndpoint(userLine)
	endLine, endAnchor := m.viewportSelectionEndpoint(blankLine)
	m.activeRegion = regionViewport
	m.selection = selectionState{
		start:       selectionPoint{line: startLine, col: 0},
		end:         selectionPoint{line: endLine, col: 0},
		active:      true,
		startAnchor: startAnchor,
		endAnchor:   endAnchor,
	}

	// Hiding the thinking block shifts the user segment and everything below it
	// up. The snapped end must follow the user segment row instead of pointing
	// at the stale absolute line, which now lands in the delegation box. Mirror
	// handleToggleThinkingMsg, which marks thinking segments dirty before sync.
	m.showThinking = false
	m.content.showThinking = false
	for i := range m.content.segments {
		if m.content.segments[i].kind == segmentThinkingBlock {
			m.content.segments[i].renderDirty = true
		}
	}
	m.content.gen++
	m.syncViewport()

	if !m.selection.hasSelection() {
		t.Fatal("viewport selection was cleared by hiding the thinking block above it")
	}
	if segIndex, _, ok := m.content.segmentAtContentLine(m.selection.start.line); !ok || segIndex != userSeg {
		t.Errorf("start endpoint maps to segment %d (ok=%v) after the shift; want user segment %d", segIndex, ok, userSeg)
	}
	if segIndex, _, ok := m.content.segmentAtContentLine(m.selection.end.line); !ok || segIndex != userSeg {
		t.Errorf("end endpoint maps to segment %d (ok=%v) after the shift; want user segment %d", segIndex, ok, userSeg)
	}
	if got := m.extractViewportText(); !strings.Contains(got, "select this user line") || strings.Contains(got, "child-1") {
		t.Errorf("extractViewportText = %q; want the user text without delegation chrome", got)
	}
}

func TestViewportSelectionDragSnapsBlankLineViaMouseHandlers(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.content.AppendUser("select this user line")
	m.content.AppendEvent(output.NewDelegationStartedEvent("child-1", "do work"))
	m.syncViewport()

	userLine, _, blankLine := findUserLineAndUnmappableLine(t, m, "select this user line")
	if userLine < 0 || blankLine < 0 {
		t.Fatal("user text or blank separator line not found in rendered content")
	}

	// Drive the real capture sites: click on the user line, then drag onto the
	// blank separator below it. The end anchor must snap back to the user row.
	// Extra content below keeps contentTopPad small enough that the blank
	// separator projects comfortably inside the viewport's draggable rows.
	m.content.AppendLine("more below\nmore below 2\nmore below 3")
	m.syncViewport()
	userY := m.screenYAtContentLine(userLine)
	blankY := m.screenYAtContentLine(blankLine)
	if got := m.contentLineAtScreenY(blankY); got != blankLine {
		t.Fatalf("test setup: blankY %d maps to content line %d; want %d", blankY, got, blankLine)
	}
	wantLine, wantAnchor := m.viewportSelectionEndpoint(blankLine)
	m = updateModel(t, m, mouseClickMsg{x: 5, y: userY})
	m = updateModel(t, m, mouseMotionMsg{x: 5, y: blankY})

	if !m.selection.endAnchor.ok {
		t.Fatal("drag end anchor = not ok; want the blank separator to snap to the user segment row")
	}
	if m.selection.end.line == blankLine {
		t.Fatal("drag end kept the blank separator line; want it to snap to the user row")
	}
	if m.selection.end.line != wantLine {
		t.Errorf("selection end line = %d; want snapped user row %d", m.selection.end.line, wantLine)
	}
	if m.selection.endAnchor != wantAnchor {
		t.Errorf("selection end anchor = %+v; want %+v", m.selection.endAnchor, wantAnchor)
	}
	if segIndex, _, ok := m.content.segmentAtContentLine(m.selection.end.line); !ok || segIndex != 0 {
		t.Errorf("selection end maps to segment %d (ok=%v); want user segment 0", segIndex, ok)
	}
}

func TestViewportSelectionNonBlankUnmappableEndpointClears(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.content.AppendLine("alpha")
	m.content.AppendLine("beta")
	m.syncViewport()

	// A non-blank line with no owning segment, e.g. streaming preview content
	// that has not become a segment yet, must stay unanchored: no snapping.
	m.fmtBgCacheInput += "\nextra streamed line"
	lines := strings.Split(m.fmtBgCacheInput, "\n")
	extraLine := len(lines) - 1
	if got, anchor := m.viewportSelectionEndpoint(extraLine); anchor.ok {
		t.Errorf("viewportSelectionEndpoint(%d) returned ok anchor %+v; want unanchored", extraLine, anchor)
	} else if got != extraLine {
		t.Errorf("viewportSelectionEndpoint(%d) returned line %d; want the original %d", extraLine, got, extraLine)
	}

	// An anchored selection whose end sits on that unanchored line is cleared
	// by a same-width content change: remapEndpoint returns false for !ok.
	m.activeRegion = regionViewport
	m.selection = selectionState{
		start:  selectionPoint{line: 0, col: 0},
		end:    selectionPoint{line: extraLine, col: 0},
		active: true,
	}
	m.selection.startAnchor = m.viewportAnchorForContentLine(0)
	m.selection.endAnchor = selectionAnchor{}
	m.mousePressX = 5
	m.mousePressY = 5
	m.dragScrollDir = 1
	m.dragScrollTicking = true

	m.content.AppendLine("gamma")
	m.syncViewport()

	if m.selection.hasSelection() {
		t.Error("selection survived a content change with an unanchored non-blank endpoint")
	}
	if m.mousePressX != -1 || m.mousePressY != -1 {
		t.Errorf("mouse press not reset: (%d,%d); want (-1,-1)", m.mousePressX, m.mousePressY)
	}
	if m.dragScrollDir != 0 || m.dragScrollTicking {
		t.Errorf("drag state not reset: dir=%d ticking=%v", m.dragScrollDir, m.dragScrollTicking)
	}
}

func TestViewportSelectionRemapsWhenContentGrowsAbove(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.content.AppendLine("alpha")
	m.content.AppendLine("beta")
	m.syncViewport()

	m.activeRegion = regionViewport
	m.selection = selectionState{start: selectionPoint{1, 0}, end: selectionPoint{1, 4}, active: true}
	m.selection.startAnchor = m.viewportAnchorForContentLine(1)
	m.selection.endAnchor = m.viewportAnchorForContentLine(1)
	m.mousePressX = 5
	m.mousePressY = 5

	// Grow the first segment in place so "beta" moves from content line 1 to 2.
	m.content.segments[0].text = "alpha\nalpha2"
	m.content.segments[0].renderDirty = true
	m.content.gen++
	m.syncViewport()

	if m.selection.start.line != 2 || m.selection.end.line != 2 {
		t.Errorf("selection lines = %d-%d; want 2-2", m.selection.start.line, m.selection.end.line)
	}
	if got := m.extractViewportText(); got != "beta" {
		t.Errorf("extractViewportText = %q; want %q", got, "beta")
	}
	if m.mousePressX != 5 {
		t.Error("mouse press was cancelled by remapping the selection")
	}
}

func TestViewportSelectionFollowsIntraSegmentTrim(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	// A single segment whose rendered rows exceed any cap, simulating a capped
	// delegation transcript: rows drop from the top as the transcript grows.
	m.content.segments = []contentSegment{{
		kind:        segmentPlain,
		text:        "row0\nrow1\nrow2\nrow3\nrow4",
		renderDirty: true,
	}}
	m.syncViewport()

	m.activeRegion = regionViewport
	m.selection = selectionState{start: selectionPoint{3, 0}, end: selectionPoint{3, 4}, active: true}
	m.selection.startAnchor = m.viewportAnchorForContentLine(3)
	m.selection.endAnchor = m.viewportAnchorForContentLine(3)
	m.mousePressX = 5
	m.mousePressY = 5

	// The transcript grows so the window drops "row0": "row3" shifts up from
	// content line 3 to line 2 and the selection follows it.
	m.content.segments[0].text = "row1\nrow2\nrow3\nrow4"
	m.content.segments[0].renderDirty = true
	m.content.gen++
	m.syncViewport()

	if m.selection.start.line != 2 || m.selection.end.line != 2 {
		t.Errorf("selection lines = %d-%d; want 2-2 after top-row drop", m.selection.start.line, m.selection.end.line)
	}
	if got := m.extractViewportText(); got != "row3" {
		t.Errorf("extractViewportText = %q; want %q", got, "row3")
	}

	// The window drops the selected row itself: the selection cannot remap and
	// is cleared along with the drag state.
	m.dragScrollDir = 1
	m.dragScrollTicking = true
	m.content.segments[0].text = "row4"
	m.content.segments[0].renderDirty = true
	m.content.gen++
	m.syncViewport()

	if m.selection.hasSelection() {
		t.Error("selection survived after its anchored row was dropped")
	}
	if m.mousePressX != -1 || m.mousePressY != -1 {
		t.Errorf("mouse press not reset after drop: (%d,%d); want (-1,-1)", m.mousePressX, m.mousePressY)
	}
	if m.dragScrollDir != 0 || m.dragScrollTicking {
		t.Errorf("drag state not reset after drop: dir=%d ticking=%v", m.dragScrollDir, m.dragScrollTicking)
	}
}

func TestViewportSelectionClearedOnWidthChange(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.content.AppendLine("first")
	m.syncViewport()

	m.activeRegion = regionViewport
	m.selection = selectionState{start: selectionPoint{0, 0}, end: selectionPoint{0, 5}, active: true}
	m.selection.startAnchor = m.viewportAnchorForContentLine(0)
	m.selection.endAnchor = m.viewportAnchorForContentLine(0)
	m.mousePressX = 5
	m.mousePressY = 5
	m.dragScrollDir = 1
	m.dragScrollTicking = true

	// A width reflow changes wrapping, so row/col anchors are invalidated.
	m.viewport.SetWidth(50)
	m.syncViewport()

	if m.selection.hasSelection() {
		t.Error("viewport selection survived a width reflow")
	}
	if m.mousePressX != -1 || m.mousePressY != -1 {
		t.Errorf("mouse press not cancelled: (%d,%d); want (-1,-1)", m.mousePressX, m.mousePressY)
	}
	if m.dragScrollDir != 0 || m.dragScrollTicking {
		t.Errorf("drag state not cancelled: dir=%d ticking=%v", m.dragScrollDir, m.dragScrollTicking)
	}
}

func TestViewportSelectionClearedOnHiddenSegment(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.showThinking = true
	m.content.showThinking = true
	m.content.segments = []contentSegment{{
		kind:        segmentThinkingBlock,
		thinkData:   &thinkingBlockData{body: "secret reasoning", collapsed: true},
		renderDirty: true,
	}}
	m.syncViewport()

	m.activeRegion = regionViewport
	m.selection = selectionState{start: selectionPoint{0, 0}, end: selectionPoint{0, 5}, active: true}
	m.selection.startAnchor = m.viewportAnchorForContentLine(0)
	m.selection.endAnchor = m.viewportAnchorForContentLine(0)
	m.mousePressX = 5
	m.mousePressY = 5

	// Hiding the thinking segment removes the anchored rows entirely. Mirror
	// handleToggleThinkingMsg, which marks thinking segments dirty before sync.
	m.showThinking = false
	m.content.showThinking = false
	for i := range m.content.segments {
		if m.content.segments[i].kind == segmentThinkingBlock {
			m.content.segments[i].renderDirty = true
		}
	}
	m.content.gen++
	m.syncViewport()

	if m.selection.hasSelection() {
		t.Error("viewport selection survived hiding its segment")
	}
	if m.mousePressX != -1 || m.mousePressY != -1 {
		t.Errorf("mouse press not cancelled: (%d,%d); want (-1,-1)", m.mousePressX, m.mousePressY)
	}
}

func TestViewportSelectionClearedOnClearConversation(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.content.AppendLine("first")
	m.syncViewport()

	m.activeRegion = regionViewport
	m.selection = selectionState{start: selectionPoint{0, 0}, end: selectionPoint{0, 5}, active: true}
	m.selection.startAnchor = m.viewportAnchorForContentLine(0)
	m.selection.endAnchor = m.viewportAnchorForContentLine(0)
	m.mousePressX = 5
	m.mousePressY = 5
	m.dragScrollDir = 1
	m.dragScrollTicking = true

	m.clearConversationState()

	if m.selection.hasSelection() {
		t.Error("viewport selection survived clearConversationState")
	}
	if m.mousePressX != -1 || m.mousePressY != -1 {
		t.Errorf("mouse press not cancelled: (%d,%d); want (-1,-1)", m.mousePressX, m.mousePressY)
	}
	if m.dragScrollDir != 0 || m.dragScrollTicking {
		t.Errorf("drag state not cancelled: dir=%d ticking=%v", m.dragScrollDir, m.dragScrollTicking)
	}
}

func TestSegmentContentLineRoundTrip(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.content.AppendLine("alpha\nalpha2")
	m.content.AppendLine("beta")
	m.content.AppendLine("gamma\ngamma2\ngamma3")
	m.syncViewport()

	rendered := m.content.String(m.viewport.Width())
	lines := strings.Split(rendered, "\n")
	if len(lines) < 6 {
		t.Fatalf("rendered content has %d lines; want at least 6", len(lines))
	}
	for i := range lines {
		segIndex, rowInSeg, ok := m.content.segmentAtContentLine(i)
		if !ok {
			t.Errorf("segmentAtContentLine(%d) = not ok; want ok", i)
			continue
		}
		line, ok := m.content.contentLineForSegmentRow(segIndex, rowInSeg)
		if !ok || line != i {
			t.Errorf("round trip line %d -> segment %d row %d -> %d (ok=%v); want %d",
				i, segIndex, rowInSeg, line, ok, i)
		}
	}
	// Extended cases below assert that every mappable content line round-trips
	// through segmentAtContentLine and contentLineForSegmentRow. Blank
	// separator lines and empty segments are legitimately unmappable, so only
	// lines that map are checked.
	roundTrip := func(t *testing.T, m *Model, lines []string) {
		t.Helper()
		for i := range lines {
			segIndex, rowInSeg, ok := m.content.segmentAtContentLine(i)
			if !ok {
				continue
			}
			line, ok := m.content.contentLineForSegmentRow(segIndex, rowInSeg)
			if !ok || line != i {
				t.Errorf("round trip line %d -> segment %d row %d -> %d (ok=%v); want %d",
					i, segIndex, rowInSeg, line, ok, i)
			}
		}
	}
	renderedLines := func(t *testing.T, m *Model) []string {
		t.Helper()
		return strings.Split(m.content.String(m.viewport.Width()), "\n")
	}

	// (a) A hidden thinking segment between visible ones: both walks skip it,
	// so the visible lines stay mappable.
	m = newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.content.segments = []contentSegment{
		{kind: segmentPlain, text: "visible one", renderDirty: true},
		{kind: segmentThinkingBlock, thinkData: &thinkingBlockData{body: "secret", collapsed: true}, renderDirty: true},
		{kind: segmentPlain, text: "visible two", renderDirty: true},
	}
	m.syncViewport()
	roundTrip(t, m, renderedLines(t, m))

	// (b) A user segment followed by another segment renders a blank margin
	// line that maps to no segment; the real lines round-trip.
	m = newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.content.AppendUser("prompt")
	m.content.AppendLine("answer")
	m.syncViewport()
	roundTrip(t, m, renderedLines(t, m))

	// (c) A visible segment whose recorded height is 0 occupies no lines: both
	// walks must never map a content line into it, and the inverse walk must
	// refuse it outright, so the lines around it stay mappable.
	m = newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.content.segments = []contentSegment{
		{kind: segmentPlain, text: "before", renderDirty: true},
		{kind: segmentPlain, text: "zero", renderDirty: true},
		{kind: segmentPlain, text: "after", renderDirty: true},
	}
	m.syncViewport()
	lines = renderedLines(t, m)
	m.content.segmentHeights[1] = 0
	for i := range lines {
		if segIndex, _, ok := m.content.segmentAtContentLine(i); ok && segIndex == 1 {
			t.Errorf("segmentAtContentLine(%d) mapped into zero-height segment 1", i)
		}
	}
	if line, ok := m.content.contentLineForSegmentRow(1, 0); ok {
		t.Errorf("contentLineForSegmentRow(1, 0) = %d, ok; want not ok", line)
	}
	roundTrip(t, m, lines)

	// (d) segmentHeights shorter than segments (content appended but debounced
	// sync not yet run): a line inside an already-rendered segment still maps
	// and round-trips because the missing trailing heights read as 0.
	m = newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.content.AppendLine("alpha")
	m.content.AppendLine("beta")
	m.syncViewport()
	lines = renderedLines(t, m)
	m.content.AppendLine("gamma")
	roundTrip(t, m, lines)
}

func TestSegmentContentLineTrailingHiddenSegmentNoPanic(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.content.showThinking = false
	m.content.segments = []contentSegment{
		{kind: segmentPlain, text: "visible", renderDirty: true},
		{kind: segmentThinkingBlock, thinkData: &thinkingBlockData{body: "secret", collapsed: true}, renderDirty: true},
	}
	m.syncViewport()

	// Make segmentHeights shorter than segments: the panic path is a trailing
	// hidden thinking segment whose height was never recorded.
	m.content.segmentHeights = []int{1}
	if len(m.content.segmentHeights) >= len(m.content.segments) {
		t.Fatalf("test setup: segmentHeights (%d) must be shorter than segments (%d)", len(m.content.segmentHeights), len(m.content.segments))
	}

	// segmentAtContentLine walks the hidden trailing segment and must not write
	// past the end of segmentHeights (the pre-fix guard panicked here).
	segIndex, rowInSeg, ok := m.content.segmentAtContentLine(1)
	if ok {
		t.Errorf("segmentAtContentLine(1) = segment %d row %d ok; want not ok", segIndex, rowInSeg)
	}
	if len(m.content.segmentHeights) != 1 {
		t.Errorf("segmentAtContentLine extended segmentHeights to %d entries; want 1", len(m.content.segmentHeights))
	}
}

func TestSegmentContentLineOutOfSyncHeights(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.content.AppendLine("alpha")
	m.content.AppendLine("beta")
	m.syncViewport()

	// Append a new segment without running the debounced sync: segmentHeights
	// is now one shorter than segments, so the pre-fix guard refused to map.
	m.content.AppendLine("gamma")
	if len(m.content.segmentHeights) >= len(m.content.segments) {
		t.Fatalf("test setup: segmentHeights (%d) must be shorter than segments (%d)", len(m.content.segmentHeights), len(m.content.segments))
	}

	// Line 1 ("beta") lives in an already-rendered segment and must map even
	// while the height slice is out of sync, and the inverse walk round-trips.
	segIndex, rowInSeg, ok := m.content.segmentAtContentLine(1)
	if !ok {
		t.Fatal("segmentAtContentLine(1) = not ok with out-of-sync heights; want ok")
	}
	if line, ok := m.content.contentLineForSegmentRow(segIndex, rowInSeg); !ok || line != 1 {
		t.Errorf("round trip line 1 -> segment %d row %d -> %d (ok=%v); want 1", segIndex, rowInSeg, line, ok)
	}

	// An anchored selection captured in the out-of-sync state must survive the
	// subsequent sync: the remap must not clear it.
	m.activeRegion = regionViewport
	m.selection = selectionState{start: selectionPoint{1, 0}, end: selectionPoint{1, 4}, active: true}
	m.selection.startAnchor = m.viewportAnchorForContentLine(1)
	m.selection.endAnchor = m.viewportAnchorForContentLine(1)
	m.mousePressX = 5
	m.mousePressY = 5
	m.syncViewport()

	if !m.selection.hasSelection() {
		t.Error("anchored selection cleared by syncViewport after out-of-sync anchoring")
	}
	if m.mousePressX != 5 {
		t.Error("mouse press cancelled by syncViewport after out-of-sync anchoring")
	}
	if got := m.extractViewportText(); got != "beta" {
		t.Errorf("extractViewportText = %q; want %q", got, "beta")
	}
}

func TestSelectionEscClearsDragPressState(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.content.AppendLine("first")
	m.syncViewport()
	m = updateModel(t, m, mouseClickMsg{x: 3, y: 5})
	m = updateModel(t, m, mouseMotionMsg{x: 10, y: 5})
	if !m.selection.hasSelection() {
		t.Fatal("test setup: no active drag selection")
	}

	handled, next, _ := m.handleSelectionEscKey()
	m = next.(*Model)
	if !handled {
		t.Error("handleSelectionEscKey returned false with an active selection")
	}
	if m.selection.hasSelection() {
		t.Error("selection survived Esc")
	}
	if m.mousePressX != -1 || m.mousePressY != -1 {
		t.Errorf("mouse press not reset by Esc: (%d,%d); want (-1,-1)", m.mousePressX, m.mousePressY)
	}
	if m.dragScrollDir != 0 || m.dragScrollTicking {
		t.Errorf("drag state not reset by Esc: dir=%d ticking=%v", m.dragScrollDir, m.dragScrollTicking)
	}

	// A content change plus sync must not leave a press that motion can
	// resurrect into a drag.
	m.content.AppendLine("more content")
	m.syncViewport()
	m = updateModel(t, m, mouseMotionMsg{x: 30, y: 15})
	if m.selection.hasSelection() {
		t.Error("motion after Esc resurrected a selection")
	}
	if m.mousePressX != -1 || m.mousePressY != -1 {
		t.Errorf("motion after Esc set a mouse press: (%d,%d)", m.mousePressX, m.mousePressY)
	}
}

func TestDragEpochInvalidatedOnClear(t *testing.T) {
	t.Parallel()

	// clearSelectionAndDrag bumps the epoch so any pending auto-scroll
	// tick from the cancelled drag goes stale.
	m := buildTestModel(100, 30, false, false)
	m.activeRegion = regionViewport
	m.selection = selectionState{start: selectionPoint{0, 0}, end: selectionPoint{2, 2}, active: true}
	m.mousePressX = 5
	m.mousePressY = 5
	m.dragScrollDir = 1
	m.dragScrollTicking = true
	m.dragScrollEpoch = 7
	m.clearSelectionAndDrag()
	if m.dragScrollEpoch != 8 {
		t.Errorf("dragScrollEpoch = %d after clearSelectionAndDrag; want 8", m.dragScrollEpoch)
	}
	if m.selection.hasSelection() {
		t.Error("selection survived clearSelectionAndDrag")
	}
	if m.mousePressX != -1 || m.mousePressY != -1 {
		t.Errorf("mouse press not reset: (%d,%d)", m.mousePressX, m.mousePressY)
	}
	if m.dragScrollDir != 0 || m.dragScrollTicking {
		t.Errorf("drag state not reset: dir=%d ticking=%v", m.dragScrollDir, m.dragScrollTicking)
	}

	// clearConversationState resets the same fields and also invalidates any
	// pending drag tick from the previous conversation.
	m = newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.content.AppendLine("first")
	m.syncViewport()
	m.activeRegion = regionViewport
	m.selection = selectionState{start: selectionPoint{0, 0}, end: selectionPoint{0, 5}, active: true}
	m.mousePressX = 5
	m.mousePressY = 5
	m.dragScrollDir = 1
	m.dragScrollTicking = true
	m.dragScrollEpoch = 3
	m.clearConversationState()
	if m.selection.hasSelection() {
		t.Error("selection survived clearConversationState")
	}
	if m.mousePressX != -1 || m.mousePressY != -1 {
		t.Errorf("mouse press not reset: (%d,%d)", m.mousePressX, m.mousePressY)
	}
	if m.dragScrollDir != 0 || m.dragScrollTicking {
		t.Errorf("drag state not reset: dir=%d ticking=%v", m.dragScrollDir, m.dragScrollTicking)
	}
	if m.dragScrollEpoch != 4 {
		t.Errorf("dragScrollEpoch = %d after clearConversationState; want 4", m.dragScrollEpoch)
	}
}

func TestViewportDragDisablesAutoScroll(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.content.AppendLine("first")
	m.syncViewport()
	m.autoScroll = true

	// A drag to a new coordinate is a deliberate navigation: follow-to-bottom
	// scrolling must stop so the view does not fight the user.
	m = updateModel(t, m, mouseClickMsg{x: 3, y: 5})
	m = updateModel(t, m, mouseMotionMsg{x: 10, y: 5})
	if m.autoScroll {
		t.Error("autoScroll still true after drag motion")
	}

	// A pure click (press then release at the same point) is not a drag and
	// must leave follow-to-bottom scrolling untouched.
	m = newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.content.AppendLine("first")
	m.syncViewport()
	m.autoScroll = true

	m = updateModel(t, m, mouseClickMsg{x: 3, y: 5})
	m = updateModel(t, m, mouseReleaseMsg{x: 3, y: 5})
	if !m.autoScroll {
		t.Error("autoScroll changed by pure click")
	}
}

func TestViewportSelectionExtractAcrossScroll(t *testing.T) {
	t.Parallel()
	// One auto-scroll tick then release: the selection spans the scroll
	// boundary and extraction returns the full content lines in between.
	// x=3 anchors at content col 0 and x=10 at col 7 (the full line width).
	m := selectionViewportModel(t, 5)
	m = updateModel(t, m, mouseClickMsg{x: 3, y: 5})
	m = updateModel(t, m, mouseMotionMsg{x: 10, y: 15})
	next, _ := m.handleDragAutoScrollTick(dragAutoScrollTickMsg{epoch: m.dragScrollEpoch})
	m = next.(*Model)
	m = updateModel(t, m, mouseReleaseMsg{x: 10, y: 15})

	// The drag clamps to the viewport bottom row, so the selection covers
	// content lines 9 through 15 (viewport.Lines() index 9..15).
	want := strings.Join(m.viewport.Lines()[9:16], "\n")
	if got := m.extractViewportText(); got != want {
		t.Errorf("extractViewportText = %q; want %q", got, want)
	}
	if m.selection.active {
		t.Error("selection.active = true after release")
	}
}
