package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/interactive"
)

// conversationTestController extends testController with a fake
// conversationReader implementation, so tests can control whether the
// "conversation empty" fast path applies without depending on a real
// *interactive.Session.
type conversationTestController struct {
	testController
	conversation []agent.Message
}

func (c *conversationTestController) Conversation() []agent.Message {
	return c.conversation
}

func (c *testController) switchOrchestrationLevelActions() []interactive.SwitchOrchestrationLevel {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []interactive.SwitchOrchestrationLevel
	for _, a := range c.actions {
		if v, ok := a.(interactive.SwitchOrchestrationLevel); ok {
			result = append(result, v)
		}
	}
	return result
}

func TestOrchestrationSlashCommandBuild(t *testing.T) {
	t.Parallel()
	sc := lookupCommand("/orchestration")
	if sc == nil {
		t.Fatal("/orchestration command not registered")
	}
	cases := []struct {
		name                string
		arg                 string
		wantOpenPicker      bool
		wantSetLevel        string
		wantInvalidArgument string
	}{
		{name: "no arg opens picker", arg: "", wantOpenPicker: true},
		{name: "low sets level", arg: "low", wantSetLevel: "low"},
		{name: "standard sets level", arg: "standard", wantSetLevel: "standard"},
		{name: "bogus reported invalid", arg: "bogus", wantInvalidArgument: "bogus"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			action := sc.Build(tc.arg)
			if action.openOrchestrationPicker != tc.wantOpenPicker {
				t.Errorf("openOrchestrationPicker = %v, want %v", action.openOrchestrationPicker, tc.wantOpenPicker)
			}
			if action.setOrchestrationLevel != tc.wantSetLevel {
				t.Errorf("setOrchestrationLevel = %q, want %q", action.setOrchestrationLevel, tc.wantSetLevel)
			}
			if action.invalidOrchestrationLevel != tc.wantInvalidArgument {
				t.Errorf("invalidOrchestrationLevel = %q, want %q", action.invalidOrchestrationLevel, tc.wantInvalidArgument)
			}
		})
	}
}

func TestOrchestrationSlashCommandInvalidArgReportsStatus(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}
	m := newModel(Config{Controller: ctrl, SubAgentsEnabled: true, OrchestrationLevel: "standard"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.input.SetValue("/orchestration bogus")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if len(ctrl.switchOrchestrationLevelActions()) != 0 {
		t.Fatalf("SwitchOrchestrationLevel dispatched for invalid arg, want no-op")
	}
	if len(m.content.segments) != 1 {
		t.Fatalf("content segments = %d, want 1", len(m.content.segments))
	}
	want := `invalid orchestration level "bogus" (use low or standard)`
	seg := m.content.segments[0]
	if seg.kind != segmentStatus {
		t.Fatalf("segment kind = %v, want segmentStatus", seg.kind)
	}
	if !strings.Contains(seg.text, want) {
		t.Fatalf("segment text = %q, want to contain %q", seg.text, want)
	}
}

func TestRequestOrchestrationLevelDisabled(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}
	m := newModel(Config{Controller: ctrl, SubAgentsEnabled: false}, nil)

	m.requestOrchestrationLevel(config.OrchestrationLevelLow)

	if len(ctrl.switchOrchestrationLevelActions()) != 0 {
		t.Fatal("dispatched SwitchOrchestrationLevel while sub-agents disabled")
	}
	if len(m.content.segments) != 1 || !strings.Contains(m.content.segments[0].text, "orchestration unavailable") {
		t.Fatalf("content segments = %#v, want one unavailable status line", m.content.segments)
	}
}

func TestRequestOrchestrationLevelSameAsCurrentIsNoop(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}
	m := newModel(Config{Controller: ctrl, SubAgentsEnabled: true, OrchestrationLevel: "standard"}, nil)

	m.requestOrchestrationLevel(config.OrchestrationLevelStandard)

	if len(ctrl.switchOrchestrationLevelActions()) != 0 {
		t.Fatal("dispatched SwitchOrchestrationLevel for a no-op switch")
	}
	if len(m.content.segments) != 0 {
		t.Fatalf("content segments = %#v, want none", m.content.segments)
	}
	if m.orchestrationConfirm.IsOpen() {
		t.Fatal("confirm modal opened for a no-op switch")
	}
}

func TestRequestOrchestrationLevelEmptyConversationSwitchesImmediately(t *testing.T) {
	t.Parallel()
	ctrl := &conversationTestController{}
	m := newModel(Config{Controller: ctrl, SubAgentsEnabled: true, OrchestrationLevel: "standard"}, nil)

	m.requestOrchestrationLevel(config.OrchestrationLevelLow)

	actions := ctrl.switchOrchestrationLevelActions()
	if len(actions) != 1 || actions[0].Level != config.OrchestrationLevelLow {
		t.Fatalf("SwitchOrchestrationLevel actions = %#v, want one for low", actions)
	}
	if m.orchestrationConfirm.IsOpen() {
		t.Fatal("confirm modal opened despite empty conversation")
	}
}

func TestRequestOrchestrationLevelOneshotRunningOpensModalEvenWhenEmpty(t *testing.T) {
	t.Parallel()
	ctrl := &conversationTestController{}
	m := newModel(Config{Controller: ctrl, SubAgentsEnabled: true, OrchestrationLevel: "standard"}, nil)
	m.oneshotRunning = true

	m.requestOrchestrationLevel(config.OrchestrationLevelLow)

	if len(ctrl.switchOrchestrationLevelActions()) != 0 {
		t.Fatal("dispatched SwitchOrchestrationLevel immediately during a oneshot run")
	}
	if !m.orchestrationConfirm.IsOpen() {
		t.Fatal("confirm modal did not open during a oneshot run despite empty conversation")
	}
}

func TestRequestOrchestrationLevelNonConversationReaderOpensModal(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}
	m := newModel(Config{Controller: ctrl, SubAgentsEnabled: true, OrchestrationLevel: "standard"}, nil)

	m.requestOrchestrationLevel(config.OrchestrationLevelLow)

	if len(ctrl.switchOrchestrationLevelActions()) != 0 {
		t.Fatal("dispatched SwitchOrchestrationLevel for a controller without Conversation()")
	}
	if !m.orchestrationConfirm.IsOpen() {
		t.Fatal("confirm modal did not open for a controller without Conversation()")
	}
}

func TestRequestOrchestrationLevelNonEmptyConversationOpensModal(t *testing.T) {
	t.Parallel()
	ctrl := &conversationTestController{conversation: []agent.Message{{Role: "user"}}}
	m := newModel(Config{Controller: ctrl, SubAgentsEnabled: true, OrchestrationLevel: "standard"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.requestOrchestrationLevel(config.OrchestrationLevelLow)

	if len(ctrl.switchOrchestrationLevelActions()) != 0 {
		t.Fatal("dispatched SwitchOrchestrationLevel with a non-empty conversation")
	}
	if !m.orchestrationConfirm.IsOpen() {
		t.Fatal("confirm modal did not open with a non-empty conversation")
	}
	if got := m.orchestrationConfirm.selectedAction(); got != confirmModalLeft {
		t.Fatalf("default selection = %v, want confirmModalLeft", got)
	}
	if got, want := m.orchestrationConfirm.spec.Heading, "Switch orchestration to low?"; got != want {
		t.Fatalf("heading = %q, want %q", got, want)
	}
	if got, want := m.orchestrationConfirm.spec.Body, "Invalidates the prompt cache."; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if got, want := m.pendingOrchestrationLevel, config.OrchestrationLevelLow; got != want {
		t.Fatalf("pendingOrchestrationLevel = %q, want %q", got, want)
	}
}

func TestRequestOrchestrationLevelBusyAppendsAppliesAfterTurn(t *testing.T) {
	t.Parallel()
	ctrl := &conversationTestController{conversation: []agent.Message{{Role: "user"}}}
	m := newModel(Config{Controller: ctrl, SubAgentsEnabled: true, OrchestrationLevel: "standard"}, nil)
	m.activity.spinning = true

	m.requestOrchestrationLevel(config.OrchestrationLevelLow)

	want := "Invalidates the prompt cache. Applies after this turn."
	if got := m.orchestrationConfirm.spec.Body; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestOrchestrationConfirmModalConfirmDispatchesOnce(t *testing.T) {
	t.Parallel()
	ctrl := &conversationTestController{conversation: []agent.Message{{Role: "user"}}}
	m := newModel(Config{Controller: ctrl, SubAgentsEnabled: true, OrchestrationLevel: "standard"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.requestOrchestrationLevel(config.OrchestrationLevelLow)
	if !m.orchestrationConfirm.IsOpen() {
		t.Fatal("confirm modal did not open")
	}

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	actions := ctrl.switchOrchestrationLevelActions()
	if len(actions) != 1 || actions[0].Level != config.OrchestrationLevelLow {
		t.Fatalf("SwitchOrchestrationLevel actions = %#v, want one for low", actions)
	}
	if m.orchestrationConfirm.IsOpen() {
		t.Fatal("confirm modal left open after confirming")
	}
	if m.pendingOrchestrationLevel != "" {
		t.Fatalf("pendingOrchestrationLevel = %q, want cleared", m.pendingOrchestrationLevel)
	}
}

func TestOrchestrationConfirmModalCancelDoesNotDispatch(t *testing.T) {
	t.Parallel()
	ctrl := &conversationTestController{conversation: []agent.Message{{Role: "user"}}}
	m := newModel(Config{Controller: ctrl, SubAgentsEnabled: true, OrchestrationLevel: "standard"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.requestOrchestrationLevel(config.OrchestrationLevelLow)
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if len(ctrl.switchOrchestrationLevelActions()) != 0 {
		t.Fatal("dispatched SwitchOrchestrationLevel after cancel")
	}
	if m.orchestrationConfirm.IsOpen() {
		t.Fatal("orchestrationConfirm.IsOpen() = true after cancel, want closed")
	}
	if m.pendingOrchestrationLevel != "" {
		t.Fatalf("pendingOrchestrationLevel = %q, want cleared", m.pendingOrchestrationLevel)
	}
}

func TestOrchestrationConfirmModalEscDoesNotDispatch(t *testing.T) {
	t.Parallel()
	ctrl := &conversationTestController{conversation: []agent.Message{{Role: "user"}}}
	m := newModel(Config{Controller: ctrl, SubAgentsEnabled: true, OrchestrationLevel: "standard"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m.requestOrchestrationLevel(config.OrchestrationLevelLow)
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})

	if len(ctrl.switchOrchestrationLevelActions()) != 0 {
		t.Fatal("dispatched SwitchOrchestrationLevel after esc")
	}
	if m.orchestrationConfirm.IsOpen() {
		t.Fatal("confirm modal left open after esc")
	}
	if m.pendingOrchestrationLevel != "" {
		t.Fatalf("pendingOrchestrationLevel = %q, want cleared", m.pendingOrchestrationLevel)
	}
}

func TestOrchestrationPickerCursorStartsOnCurrentLevel(t *testing.T) {
	t.Parallel()
	m := newModel(Config{SubAgentsEnabled: true, OrchestrationLevel: "low"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	if opened := m.openOrchestrationPicker(); !opened {
		t.Fatal("picker did not open")
	}
	if got := m.orchestrationPicker.Selected(); got != config.OrchestrationLevelLow {
		t.Fatalf("initial selection = %q, want low", got)
	}
}

func TestOrchestrationPickerMarkerIndependentOfCursor(t *testing.T) {
	t.Parallel()
	m := newModel(Config{SubAgentsEnabled: true, OrchestrationLevel: "standard"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.openOrchestrationPicker()

	rendered := stripANSI(m.orchestrationPicker.View())
	if got := strings.Count(rendered, "●"); got != 1 {
		t.Fatalf("current-level markers = %d, want 1 in %q", got, rendered)
	}

	m.orchestrationPicker = m.orchestrationPicker.moveSelection(1)
	rendered = stripANSI(m.orchestrationPicker.View())
	lines := strings.Split(rendered, "\n")
	var markerLine, cursorLine string
	for _, line := range lines {
		if strings.Contains(line, "●") {
			markerLine = line
		}
		if strings.Contains(line, "▸") {
			cursorLine = line
		}
	}
	if !strings.Contains(markerLine, "standard") {
		t.Fatalf("marker line = %q, want to still mark standard", markerLine)
	}
	if !strings.Contains(cursorLine, "low") {
		t.Fatalf("cursor line = %q, want to have moved to low", cursorLine)
	}
}

func TestOrchestrationPickerEnterTriggersApplyFlow(t *testing.T) {
	t.Parallel()
	ctrl := &conversationTestController{}
	m := newModel(Config{Controller: ctrl, SubAgentsEnabled: true, OrchestrationLevel: "standard"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.openOrchestrationPicker()

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	actions := ctrl.switchOrchestrationLevelActions()
	if len(actions) != 1 || actions[0].Level != config.OrchestrationLevelLow {
		t.Fatalf("SwitchOrchestrationLevel actions = %#v, want one for low", actions)
	}
	if m.orchestrationPicker.IsOpen() {
		t.Fatal("picker left open after enter")
	}
}

func TestOrchestrationPickerEscClosesWithoutDispatch(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}
	m := newModel(Config{Controller: ctrl, SubAgentsEnabled: true, OrchestrationLevel: "standard"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.openOrchestrationPicker()

	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEsc})

	if m.orchestrationPicker.IsOpen() {
		t.Fatal("picker left open after esc")
	}
	if len(ctrl.switchOrchestrationLevelActions()) != 0 {
		t.Fatal("dispatched SwitchOrchestrationLevel on esc")
	}
}

func TestOpenOrchestrationPickerDisabledShowsStatusAndDoesNotOpen(t *testing.T) {
	t.Parallel()
	m := newModel(Config{SubAgentsEnabled: false}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	if opened := m.openOrchestrationPicker(); opened {
		t.Fatal("picker opened while sub-agents disabled")
	}
	if m.orchestrationPicker.IsOpen() {
		t.Fatal("picker state open while sub-agents disabled")
	}
	if len(m.content.segments) != 1 || !strings.Contains(m.content.segments[0].text, "orchestration unavailable") {
		t.Fatalf("content segments = %#v, want one unavailable status line", m.content.segments)
	}
}
