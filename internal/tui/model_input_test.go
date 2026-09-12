package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

func TestOneshotAllowedAction(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "/exit is allowed", input: "/exit", want: true},
		{name: "/thinking is allowed", input: "/thinking", want: true},
		{name: "/accent is allowed", input: "/accent", want: true},
		{name: "/accent amber is allowed", input: "/accent amber", want: true},
		{name: "/accent foo prefix is allowed", input: "/accent foo", want: true},
		{name: "/oneshot is NOT allowed", input: "/oneshot do something", want: false},
		{name: "hello world is NOT allowed", input: "hello world", want: false},
		{name: "leading whitespace allowed", input: "  /exit", want: true},
		{name: "trailing whitespace allowed", input: "/exit  ", want: true},
		{name: "empty string is NOT allowed", input: "", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := oneshotAllowedAction(tc.input)
			if got != tc.want {
				t.Errorf("oneshotAllowedAction(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestHandleEnterRoutesToSteerDuringOneshot(t *testing.T) {
	t.Parallel()
	input := newModelInput()
	input.SetValue("hello")

	styles := testStyles(theme.AccentAmber)
	q := agent.NewSteerQueue()
	m := &Model{
		oneshotRunning: true,
		steers:         q,
		input:          input,
		content: contentBuffer{
			segments:      make([]contentSegment, 0),
			collapseState: make(map[int]bool),
			styles:        styles,
		},
		styles: styles,
	}

	_, cmd := m.handleEnter()
	if cmd != nil {
		t.Fatalf("handleEnter() returned a non-nil cmd, want nil (steer returns nil)")
	}
	queued := q.Snapshot()
	if len(queued) != 1 {
		t.Fatalf("steer queue len = %d, want 1", len(queued))
	}
	if queued[0].Text != "hello" {
		t.Fatalf("steer queue got %+v, want {hello}", queued[0])
	}
}

func TestSteerActionCapturesImagesForOneshot(t *testing.T) {
	t.Parallel()
	input := newModelInput()
	input.SetValue("describe this")
	input.InsertString(" [Image 1]")

	q := agent.NewSteerQueue()
	styles := testStyles(theme.AccentAmber)
	m := &Model{
		oneshotRunning: true,
		steers:         q,
		input:          input,
		imageMarkers: []imageMarker{
			{label: "[Image 1]", image: agent.ImageBlock{MediaType: "image/png", Data: "queued-image-data"}},
		},
		content: contentBuffer{
			segments:      make([]contentSegment, 0),
			collapseState: make(map[int]bool),
			styles:        styles,
		},
		styles: styles,
	}

	updated := m.executeSteerAction()
	m = updated.(*Model)

	if len(m.imageMarkers) != 0 {
		t.Fatalf("imageMarkers = %d, want 0 after steer", len(m.imageMarkers))
	}

	queued := q.Snapshot()
	if len(queued) != 1 {
		t.Fatalf("steer queue len = %d, want 1", len(queued))
	}
	msg := queued[0]
	if msg.Text != "describe this [Image 1]" {
		t.Errorf("steer text = %q, want %q", msg.Text, "describe this [Image 1]")
	}
	if len(msg.Images) != 1 {
		t.Fatalf("steer images = %d, want 1", len(msg.Images))
	}
	if msg.Images[0].Data != "queued-image-data" {
		t.Errorf("steer image data = %q, want %q", msg.Images[0].Data, "queued-image-data")
	}
}

func TestSteerActionRecordsPromptHistory(t *testing.T) {
	t.Parallel()
	input := newModelInput()
	input.SetValue("  steer this  ")

	q := agent.NewSteerQueue()
	ctrl := &testController{}
	styles := testStyles(theme.AccentAmber)
	m := &Model{
		steers:     q,
		controller: ctrl,
		input:      input,
		content: contentBuffer{
			segments:      make([]contentSegment, 0),
			collapseState: make(map[int]bool),
			styles:        styles,
		},
		styles: styles,
	}

	m.executeSteerAction()

	queued := q.Snapshot()
	if len(queued) != 1 {
		t.Fatalf("steer queue len = %d, want 1", len(queued))
	}

	ctrl.mu.Lock()
	actions := ctrl.actions
	ctrl.mu.Unlock()
	if len(actions) != 1 {
		t.Fatalf("controller actions = %d, want 1", len(actions))
	}
	recorded, ok := actions[0].(interactive.RecordPromptHistory)
	if !ok {
		t.Fatalf("action = %T, want interactive.RecordPromptHistory", actions[0])
	}
	if recorded.Text != "steer this" {
		t.Errorf("recorded text = %q, want %q", recorded.Text, "steer this")
	}
}

func TestSteerActionWhitespaceOnlyInputDoesNothing(t *testing.T) {
	t.Parallel()
	input := newModelInput()
	input.SetValue("   ")

	q := agent.NewSteerQueue()
	ctrl := &testController{}
	styles := testStyles(theme.AccentAmber)
	m := &Model{
		steers:     q,
		controller: ctrl,
		input:      input,
		content: contentBuffer{
			segments:      make([]contentSegment, 0),
			collapseState: make(map[int]bool),
			styles:        styles,
		},
		styles: styles,
	}

	m.executeSteerAction()

	if got := q.Len(); got != 0 {
		t.Fatalf("steer queue len = %d, want 0", got)
	}
	ctrl.mu.Lock()
	actionCount := len(ctrl.actions)
	ctrl.mu.Unlock()
	if actionCount != 0 {
		t.Fatalf("controller actions = %d, want 0", actionCount)
	}
}

func TestSteerActionControllerErrorStillQueuesSteer(t *testing.T) {
	t.Parallel()
	input := newModelInput()
	input.SetValue("steer with failure")

	q := agent.NewSteerQueue()
	steerErr := fmt.Errorf("record failed")
	ctrl := &testController{err: steerErr}
	styles := testStyles(theme.AccentAmber)
	m := &Model{
		steers:     q,
		controller: ctrl,
		input:      input,
		content: contentBuffer{
			segments:      make([]contentSegment, 0),
			collapseState: make(map[int]bool),
			styles:        styles,
		},
		styles: styles,
	}

	updated := m.executeSteerAction()
	m = updated.(*Model)

	queued := q.Snapshot()
	if len(queued) != 1 {
		t.Fatalf("steer queue len = %d, want 1", len(queued))
	}
	if got := m.content.String(80); !strings.Contains(got, steerErr.Error()) {
		t.Errorf("content = %q, want controller error", got)
	}
}

func TestHandleEnterRoutesToSteerDuringBusyRegularRun(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}
	q := agent.NewSteerQueue()
	m := newModel(Config{
		Controller: ctrl,
		SteerQueue: q,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m.input.SetValue("hello")

	updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if got := q.Len(); got != 1 {
		t.Fatalf("steer queue len = %d, want 1 (busy regular run must queue steer)", got)
	}
	if ctrl.countSubmitPrompt() != 0 {
		t.Fatalf("SubmitPrompt count = %d, want 0 (busy regular run must not submit)", ctrl.countSubmitPrompt())
	}
}

func TestSteerQueueSharedBetweenComposerAndOneshotRun(t *testing.T) {
	t.Parallel()
	sess, err := interactive.NewSession(interactive.Dependencies{BaseEvents: output.NoopSink{}})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	queue := sess.ActiveRunController().SteerQueue()

	m := newModel(Config{Controller: sess, SteerQueue: queue}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	// Queue during a regular busy run.
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m.input.SetValue("steer during regular run")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	// Queue again while simulating an active oneshot run.
	m.oneshotRunning = true
	m.input.SetValue("steer during oneshot")
	updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	// Both messages must land in the exact queue instance the oneshot run
	// drains from — proving the composer never uses a second instance.
	got := sess.ActiveRunController().SteerQueue().Drain()
	if len(got) != 2 || got[0].Text != "steer during regular run" || got[1].Text != "steer during oneshot" {
		t.Fatalf("Drain() = %+v, want [{steer during regular run} {steer during oneshot}]", got)
	}
}

func newMinimalModel(inputValue string) *Model {
	inp := newModelInput()
	inp.SetValue(inputValue)
	styles := testStyles(theme.AccentAmber)
	return &Model{
		input:            inp,
		styles:           styles,
		subAgentsEnabled: true,
		content: contentBuffer{
			segments:      make([]contentSegment, 0),
			collapseState: make(map[int]bool),
			styles:        styles,
		},
	}
}

func TestLaunchOneshotActionClearsComposer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		factory OneshotRunnerFactoryBuilder
	}{
		{name: "nil factory", factory: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newMinimalModel("/oneshot build the thing")
			m.oneshotRunnerFactory = tc.factory
			updated, _ := m.executeLaunchOneshotAction("build the thing")
			got := updated.(*Model).input.Value()
			if got != "" {
				t.Errorf("input after oneshot dispatch = %q, want empty", got)
			}
		})
	}
}

func TestResumeOneshotActionClearsComposer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		factory OneshotRunnerFactoryBuilder
	}{
		{name: "nil factory", factory: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newMinimalModel("/oneshot --resume abc123")
			m.oneshotRunnerFactory = tc.factory
			updated, _ := m.executeResumeOneshotAction("abc123")
			got := updated.(*Model).input.Value()
			if got != "" {
				t.Errorf("input after resume dispatch = %q, want empty", got)
			}
		})
	}
}

func TestPrepareOneshotRunRefusesWhenSubAgentsDisabled(t *testing.T) {
	t.Parallel()
	m := newMinimalModel("/oneshot build the thing")
	m.subAgentsEnabled = false

	updated, ok := m.prepareOneshotRun()
	if ok {
		t.Fatalf("prepareOneshotRun() ok = true, want false when sub-agents are disabled")
	}
	if updated.oneshotRunning {
		t.Errorf("oneshotRunning = true, want false")
	}
	if len(updated.content.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(updated.content.segments))
	}
	want := "oneshot unavailable: sub-agents are disabled in config"
	if got := updated.content.segments[0].text; got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
	if got := updated.input.Value(); got != "" {
		t.Errorf("input after refusal = %q, want empty", got)
	}
}

func TestPrepareOneshotRunProceedsPastGuardWhenSubAgentsEnabled(t *testing.T) {
	t.Parallel()
	m := newMinimalModel("/oneshot build the thing")

	updated, ok := m.prepareOneshotRun()
	if ok {
		t.Fatalf("prepareOneshotRun() ok = true, want false (no runner factory wired in this test)")
	}
	if len(updated.content.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(updated.content.segments))
	}
	got := updated.content.segments[0].text
	if got == "oneshot unavailable: sub-agents are disabled in config" {
		t.Errorf("content = %q, guard fired even though sub-agents are enabled", got)
	}
	want := "oneshot runner factory not configured"
	if got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

func TestOpenOneshotResumePickerRefusesWhenSubAgentsDisabled(t *testing.T) {
	t.Parallel()
	m := newMinimalModel("")
	m.subAgentsEnabled = false

	opened := m.openOneshotResumePicker()
	if opened {
		t.Fatalf("openOneshotResumePicker() = true, want false when sub-agents are disabled")
	}
	if len(m.content.segments) != 1 {
		t.Fatalf("segments count = %d, want 1", len(m.content.segments))
	}
	want := "oneshot unavailable: sub-agents are disabled in config"
	if got := m.content.segments[0].text; got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

func TestBuildSlashOverlayItemsAllowlistDuringOneshot(t *testing.T) {
	t.Parallel()
	m := &Model{oneshotRunning: true}
	items := m.buildSlashOverlayItems()
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	got := make([]string, len(items))
	for i, item := range items {
		got[i] = item.command
	}
	want := []string{"/exit", "/thinking", "/accent"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("item[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNoArgSkillInvocationEnablesAndSubmits(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}
	m := newModel(Config{
		SkillNames: []string{"myskill"},
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("/myskill")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if !m.enabledSkills["myskill"] {
		t.Fatal("enabledSkills[myskill] = false, want true after no-arg invocation")
	}
	if ctrl.countSubmitPrompt() != 1 {
		t.Fatalf("SubmitPrompt count = %d, want 1 (skill must submit immediately)", ctrl.countSubmitPrompt())
	}
	prompts := ctrl.submitPrompts()
	if prompts[0].Text != "/myskill" {
		t.Fatalf("submitted text = %q, want %q", prompts[0].Text, "/myskill")
	}
}

func TestArgSkillInvocationEnablesAndSubmitsArgs(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}
	m := newModel(Config{
		SkillNames: []string{"myskill"},
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("/myskill do the thing")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if !m.enabledSkills["myskill"] {
		t.Fatal("enabledSkills[myskill] = false, want true after args invocation")
	}
	if ctrl.countSubmitPrompt() != 1 {
		t.Fatalf("SubmitPrompt count = %d, want 1", ctrl.countSubmitPrompt())
	}
	prompts := ctrl.submitPrompts()
	if prompts[0].Text != "/myskill do the thing" {
		t.Fatalf("submitted text = %q, want %q", prompts[0].Text, "/myskill do the thing")
	}
}

func TestSkillInvocationSwitchesExecutionMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		skill    string
		wantMode config.ExecutionMode
	}{
		{"plan", config.ExecutionModePlan},
		{"implement", config.ExecutionModeBuild},
		{"review", config.ExecutionModeBuild},
		{"someCustomSkill", config.ExecutionModeBuild},
	}
	for _, tt := range tests {
		t.Run(tt.skill, func(t *testing.T) {
			t.Parallel()
			ctrl := &testController{}
			m := newModel(Config{
				SkillNames: []string{tt.skill},
				Controller: ctrl,
			}, nil)
			m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

			m.input.SetValue("/" + tt.skill + " some args")
			updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

			var got []config.ExecutionMode
			for _, a := range ctrl.actions {
				if sm, ok := a.(interactive.SwitchMode); ok {
					got = append(got, sm.Mode)
				}
			}
			if len(got) != 1 || got[0] != tt.wantMode {
				t.Fatalf("SwitchMode actions = %#v, want exactly one with mode %q", got, tt.wantMode)
			}
		})
	}
}

func TestBuildSlashOverlayItemsIncludesCacheStats(t *testing.T) {
	t.Parallel()
	m := &Model{}
	items := m.buildSlashOverlayItems()

	found := false
	for _, item := range items {
		if item.command == "/cache-stats" {
			found = true
			if item.name != "Cache stats" {
				t.Errorf("name = %q, want %q", item.name, "Cache stats")
			}
			if item.desc != "show cache hit rate stats" {
				t.Errorf("desc = %q, want %q", item.desc, "show cache hit rate stats")
			}
			break
		}
	}
	if !found {
		t.Fatal("/cache-stats not found in overlay items")
	}
}

func TestProfileSlashCommandDispatchesSwitch(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}
	m := newModel(Config{Controller: ctrl, ProfileName: "default"}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	if got, want := m.sidebar.profile, "default"; got != want {
		t.Fatalf("initial sidebar profile = %q, want %q", got, want)
	}

	m.input.SetValue("/profile fast")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	var got []interactive.SwitchProfile
	for _, action := range ctrl.actions {
		if profile, ok := action.(interactive.SwitchProfile); ok {
			got = append(got, profile)
		}
	}
	if len(got) != 1 || got[0].Name != "fast" {
		t.Fatalf("SwitchProfile actions = %#v, want one action for fast", got)
	}
	if got, want := m.sidebar.profile, "fast"; got != want {
		t.Fatalf("sidebar profile after switch = %q, want %q", got, want)
	}
	if got := m.content.segments[len(m.content.segments)-1].text; got != "profile switched to fast" {
		t.Fatalf("success status = %q, want profile switched status", got)
	}
	if m.input.Value() != "" {
		t.Fatalf("input value = %q, want empty", m.input.Value())
	}
}

func TestProfileSlashCommandWithoutNameOpensPicker(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}
	m := newModel(Config{Controller: ctrl, ProfileNames: []string{"default", "fast"}}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("/profile")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	for _, action := range ctrl.actions {
		if _, ok := action.(interactive.SwitchProfile); ok {
			t.Fatal("SwitchProfile dispatched without a profile name")
		}
	}
	if !m.profilePicker.IsOpen() {
		t.Fatal("expected profile picker to be open")
	}
	if m.input.Value() != "/profile " {
		t.Fatalf("input value = %q, want %q", m.input.Value(), "/profile ")
	}
}

func TestProfileSlashCommandDisplaysControllerError(t *testing.T) {
	t.Parallel()
	ctrl := &testController{err: fmt.Errorf("profile switch failed")}
	m := newModel(Config{Controller: ctrl}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})

	m.input.SetValue("/profile fast")
	m = updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if got := stripANSI(m.content.segments[len(m.content.segments)-1].text); !strings.Contains(got, "profile switch failed") {
		t.Fatalf("error status = %q, want to contain profile switch failed", got)
	}
	if m.input.Value() != "" {
		t.Fatalf("input value = %q, want empty", m.input.Value())
	}
}
