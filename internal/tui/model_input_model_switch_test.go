package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/provider"
)

// newSwitchTestModel builds a model with models small and large and profiles
// default and fast, sized so the view renders.
func newSwitchTestModel(t *testing.T, cfg Config) *Model {
	t.Helper()
	if cfg.Model == "" {
		cfg.Model = "small"
	}
	if cfg.CurrentModelAlias == "" {
		cfg.CurrentModelAlias = "small"
	}
	cfg.ModelNames = []string{"small", "large"}
	cfg.ProfileNames = []string{"default", "fast"}
	return updateModel(t, newModel(cfg, nil), tea.WindowSizeMsg{Width: 120, Height: 40})
}

func pressKeys(t *testing.T, m *Model, keys ...tea.KeyPressMsg) *Model {
	t.Helper()
	for _, k := range keys {
		m = updateModel(t, m, k)
	}
	return m
}

func keybindAlt(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Mod: tea.ModAlt}
}

func keybindPickerOpen(m *Model, key rune) bool {
	if key == 'm' {
		return m.modelPicker.IsOpen()
	}
	return m.profilePicker.IsOpen()
}

func assertDraftKept(t *testing.T, m *Model) {
	t.Helper()
	if got := m.input.Value(); got != "draft text" {
		t.Fatalf("draft = %q, want %q", got, "draft text")
	}
	if got := m.input.Column(); got != 3 {
		t.Fatalf("cursor column = %d, want 3", got)
	}
}

var (
	keyDown  = tea.KeyPressMsg{Code: tea.KeyDown}
	keyEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyEsc   = tea.KeyPressMsg{Code: tea.KeyEsc}
	keyRight = tea.KeyPressMsg{Code: tea.KeyRight}
)

func TestKeybindPickersOpenAndEscKeepDraft(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		key  rune
		busy bool
	}{
		{name: "model idle", key: 'm'},
		{name: "model during run", key: 'm', busy: true},
		{name: "profile idle", key: 'p'},
		{name: "profile during run", key: 'p', busy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newSwitchTestModel(t, Config{Controller: &testController{}})
			m.activity.spinning = tc.busy
			m.input.SetValue("draft text")
			m.input.SetCursorColumn(3)

			m = pressKeys(t, m, keybindAlt(tc.key))
			if !keybindPickerOpen(m, tc.key) {
				t.Fatal("picker did not open")
			}
			assertDraftKept(t, m)

			m = pressKeys(t, m, keyEsc)
			if keybindPickerOpen(m, tc.key) {
				t.Fatal("picker still open after esc")
			}
			assertDraftKept(t, m)
		})
	}
}

func TestKeybindPickerEnterCommitsAndKeepsDraft(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		key         rune
		busy        bool
		wantPrimary string
		wantProfile string
	}{
		{name: "model idle", key: 'm', wantPrimary: "large"},
		{name: "model during run", key: 'm', busy: true, wantPrimary: "large"},
		{name: "profile idle", key: 'p', wantProfile: "fast"},
		{name: "profile during run", key: 'p', busy: true, wantProfile: "fast"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newSwitchTestModel(t, Config{Controller: &testController{}})
			m.activity.spinning = tc.busy
			m.input.SetValue("draft text")
			m.input.SetCursorColumn(3)

			m = pressKeys(t, m, keybindAlt(tc.key), keyDown, keyEnter)
			if tc.wantPrimary != "" && m.primaryModel != tc.wantPrimary {
				t.Fatalf("primaryModel = %q, want %q", m.primaryModel, tc.wantPrimary)
			}
			if tc.wantProfile != "" && m.sidebar.profile != tc.wantProfile {
				t.Fatalf("profile = %q, want %q", m.sidebar.profile, tc.wantProfile)
			}
			if keybindPickerOpen(m, tc.key) {
				t.Fatal("picker still open after enter")
			}
			assertDraftKept(t, m)
		})
	}
}

func TestKeybindModelReasoningConfirmKeepsDraft(t *testing.T) {
	t.Parallel()
	ctrl := &conversationTestController{conversation: []agent.Message{{Role: "user"}}}
	m := newSwitchTestModel(t, Config{
		Controller: ctrl,
		ModelReasoningCapabilities: map[string]provider.ReasoningCapabilities{
			"large": {SupportedEfforts: []string{"low", "medium", "high"}},
		},
	})
	m.input.SetValue("draft text")
	m.input.SetCursorColumn(3)

	m = pressKeys(t, m, keybindAlt('m'), keyDown, keyEnter)
	if !m.reasoningPicker.IsOpen() {
		t.Fatal("reasoning step did not open")
	}
	m = pressKeys(t, m, keyDown, keyDown, keyEnter)
	if !m.modelSwitchConfirm.IsOpen() {
		t.Fatal("confirm modal did not open for a switch with a non-empty conversation")
	}
	assertDraftKept(t, m)

	m = pressKeys(t, m, keyRight, keyEnter)
	sw := ctrl.switchModelActions()
	if len(sw) != 1 || sw[0].Name != "large" || sw[0].Reasoning == nil || sw[0].Reasoning.Effort != "medium" {
		t.Fatalf("SwitchModel actions = %+v, want one for large at medium", sw)
	}
	assertDraftKept(t, m)
}

func TestKeybindModelSwitchFailureKeepsDraft(t *testing.T) {
	t.Parallel()
	ctrl := &testController{switchModelErr: errors.New("switch rejected")}
	m := newSwitchTestModel(t, Config{Controller: ctrl})
	m.input.SetValue("draft text")
	m.input.SetCursorColumn(3)

	m = pressKeys(t, m, keybindAlt('m'), keyDown, keyEnter)
	if m.primaryModel != "small" {
		t.Fatalf("primaryModel = %q, want small after a failed switch", m.primaryModel)
	}
	assertDraftKept(t, m)
	if !strings.Contains(stripANSI(m.View().Content), "model large is not configured") {
		t.Fatal("failed switch did not report the status line")
	}
}

func TestRequestModelSwitchConfirmGate(t *testing.T) {
	t.Parallel()
	msgs := []agent.Message{{Role: "user"}}
	effort := &provider.ReasoningOverride{Kind: provider.ReasoningOverrideEffort, Effort: "high"}
	for _, tc := range []struct {
		name         string
		conversation []agent.Message
		busy         bool
		target       string
		reasoning    *provider.ReasoningOverride
		wantConfirm  bool
		wantBody     string
	}{
		{name: "empty conversation commits", target: "large"},
		{name: "non-empty conversation confirms", conversation: msgs, target: "large", wantConfirm: true, wantBody: "Invalidates the prompt cache."},
		{name: "busy run says applies to next run", conversation: msgs, busy: true, target: "large", wantConfirm: true, wantBody: "Invalidates the prompt cache. Applies to the next run."},
		{name: "same model without reasoning commits", conversation: msgs, target: "small"},
		{name: "same model with new reasoning confirms", conversation: msgs, target: "small", reasoning: effort, wantConfirm: true, wantBody: "Invalidates the prompt cache."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := &conversationTestController{conversation: tc.conversation}
			m := newSwitchTestModel(t, Config{Controller: ctrl})
			m.activity.spinning = tc.busy

			m.requestModelSwitch(tc.target, tc.reasoning)

			if got := m.modelSwitchConfirm.IsOpen(); got != tc.wantConfirm {
				t.Fatalf("confirm open = %t, want %t", got, tc.wantConfirm)
			}
			if tc.wantConfirm {
				if got := m.modelSwitchConfirm.spec.Body; got != tc.wantBody {
					t.Fatalf("body = %q, want %q", got, tc.wantBody)
				}
				if n := len(ctrl.switchModelActions()); n != 0 {
					t.Fatalf("SwitchModel actions = %d before confirm, want 0", n)
				}
				return
			}
			if n := len(ctrl.switchModelActions()); n != 1 {
				t.Fatalf("SwitchModel actions = %d, want 1", n)
			}
		})
	}
}

func TestModelSwitchConfirmModalResolution(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		keys       []tea.KeyPressMsg
		wantSwitch bool
	}{
		{name: "right then enter switches", keys: []tea.KeyPressMsg{keyRight, keyEnter}, wantSwitch: true},
		{name: "enter on default cancels", keys: []tea.KeyPressMsg{keyEnter}},
		{name: "esc cancels", keys: []tea.KeyPressMsg{keyEsc}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := &conversationTestController{conversation: []agent.Message{{Role: "user"}}}
			m := newSwitchTestModel(t, Config{Controller: ctrl})
			m.requestModelSwitch("large", nil)

			m = pressKeys(t, m, tc.keys...)

			if m.modelSwitchConfirm.IsOpen() {
				t.Fatal("confirm modal still open")
			}
			if m.pendingModelName != "" || m.pendingModelReasoning != nil {
				t.Fatalf("pending switch not cleared: name=%q reasoning=%+v", m.pendingModelName, m.pendingModelReasoning)
			}
			wantPrimary := "small"
			wantActions := 0
			if tc.wantSwitch {
				wantPrimary = "large"
				wantActions = 1
			}
			if m.primaryModel != wantPrimary {
				t.Fatalf("primaryModel = %q, want %q", m.primaryModel, wantPrimary)
			}
			if n := len(ctrl.switchModelActions()); n != wantActions {
				t.Fatalf("SwitchModel actions = %d, want %d", n, wantActions)
			}
		})
	}
}

func TestModelSwitchBusyConfirmReportsNextRun(t *testing.T) {
	t.Parallel()
	ctrl := &conversationTestController{conversation: []agent.Message{{Role: "user"}}}
	m := newSwitchTestModel(t, Config{Controller: ctrl})
	m.activity.spinning = true

	m.requestModelSwitch("large", nil)
	m = pressKeys(t, m, keyRight, keyEnter)

	if !strings.Contains(stripANSI(m.View().Content), "model switched to large (applies to the next run)") {
		t.Fatal("status line does not say the switch applies to the next run")
	}
}

func TestTypedModelCommandWithConversationConfirms(t *testing.T) {
	t.Parallel()
	ctrl := &conversationTestController{conversation: []agent.Message{{Role: "user"}}}
	m := newSwitchTestModel(t, Config{Controller: ctrl})
	m.input.SetValue("/model large")

	m = pressKeys(t, m, keyEnter)
	if !m.modelSwitchConfirm.IsOpen() {
		t.Fatal("confirm modal did not open for typed /model")
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("composer = %q after typed /model, want empty", got)
	}

	pressKeys(t, m, keyRight, keyEnter)
	if n := len(ctrl.switchModelActions()); n != 1 {
		t.Fatalf("SwitchModel actions = %d, want 1", n)
	}
}

func TestModelKeybindBlockedByOpenOverlays(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		setup func(m *Model)
	}{
		{name: "approval active", setup: func(m *Model) { m.approval.active = true }},
		{name: "model switch confirm open", setup: func(m *Model) { m.requestModelSwitch("large", nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := &conversationTestController{conversation: []agent.Message{{Role: "user"}}}
			m := newSwitchTestModel(t, Config{Controller: ctrl})
			tc.setup(m)

			m = pressKeys(t, m, keybindAlt('m'))
			if m.modelPicker.IsOpen() {
				t.Fatal("model picker opened over an active overlay or approval")
			}
		})
	}
}

func TestHelpListsModelAndProfileKeybinds(t *testing.T) {
	t.Parallel()
	m := newSwitchTestModel(t, Config{})
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 110, Height: 80})
	m.helpVisible = true

	if !strings.Contains(stripANSI(m.View().Content), "alt+m / alt+p") {
		t.Fatal("help overlay does not list alt+m / alt+p")
	}
}
