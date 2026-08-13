package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/agent"
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
	m := &Model{
		oneshotRunning: true,
		oneshotSteerCh: make(chan agent.SteerMessage, 4),
		input:          input,
		content: contentBuffer{
			segments:      make([]contentSegment, 0),
			collapseState: make(map[int]bool),
			styles:        styles,
		},
		styles: styles,
	}

	updated, cmd := m.handleEnter()
	m = updated.(*Model)
	if cmd != nil {
		t.Fatalf("handleEnter() returned a non-nil cmd, want nil (steer returns nil)")
	}
	if !m.steerQueued {
		t.Fatal("steerQueued = false, want true")
	}
	select {
	case msg := <-m.oneshotSteerCh:
		if msg.Text != "hello" {
			t.Fatalf("steer channel got %+v, want {hello}", msg)
		}
	default:
		t.Fatal("steer channel empty, expected hello")
	}
}

func TestSteerActionCapturesImagesForOneshot(t *testing.T) {
	t.Parallel()
	input := newModelInput()
	input.SetValue("describe this")
	input.InsertString(" [Image 1]")

	steerCh := make(chan agent.SteerMessage, 4)
	styles := testStyles(theme.AccentAmber)
	m := &Model{
		oneshotRunning: true,
		oneshotSteerCh: steerCh,
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

	select {
	case msg := <-steerCh:
		if msg.Text != "describe this [Image 1]" {
			t.Errorf("steer text = %q, want %q", msg.Text, "describe this [Image 1]")
		}
		if len(msg.Images) != 1 {
			t.Fatalf("steer images = %d, want 1", len(msg.Images))
		}
		if msg.Images[0].Data != "queued-image-data" {
			t.Errorf("steer image data = %q, want %q", msg.Images[0].Data, "queued-image-data")
		}
	default:
		t.Fatal("steer channel empty, expected message with image")
	}
}

func TestHandleEnterRoutesToSteerDuringBusyRegularRun(t *testing.T) {
	t.Parallel()
	ctrl := &testController{}
	m := newModel(Config{
		Controller: ctrl,
	}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewRunStartedEvent("interactive", "gpt-test", "", 4, 256)})
	m.input.SetValue("hello")

	updateModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if ctrl.countSteerPrompt() != 1 {
		t.Fatalf("SteerPrompt count = %d, want 1 (busy regular run must queue steer)", ctrl.countSteerPrompt())
	}
	if ctrl.countSubmitPrompt() != 0 {
		t.Fatalf("SubmitPrompt count = %d, want 0 (busy regular run must not submit)", ctrl.countSubmitPrompt())
	}
}

func newMinimalModel(inputValue string) *Model {
	inp := newModelInput()
	inp.SetValue(inputValue)
	styles := testStyles(theme.AccentAmber)
	return &Model{
		input:  inp,
		styles: styles,
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
	if prompts[0].Text != "do the thing" {
		t.Fatalf("submitted text = %q, want %q", prompts[0].Text, "do the thing")
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
