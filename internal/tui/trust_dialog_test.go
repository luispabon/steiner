package tui

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/config"
)

func testInspection() config.ProjectInspection {
	return config.ProjectInspection{
		ProjectRoot: "/home/user/project",
		ConfigPath:  "/home/user/project/.steiner/config.yaml",
		Changes: []config.FieldChange{
			{Path: "sandbox.enabled", Before: "true", After: "false", Security: true},
			{Path: "tui.fps", Before: "30", After: "60", Security: false},
		},
	}
}

func keyPress(text string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Text: text, Code: rune(text[0])}
}

func TestTrustDialogKeyChoices(t *testing.T) {
	tests := []struct {
		name       string
		key        tea.KeyPressMsg
		wantChoice TrustChoice
	}{
		{"always", keyPress("a"), TrustAlways},
		{"session", keyPress("s"), TrustSession},
		{"deny", keyPress("d"), TrustDeny},
		{"esc", tea.KeyPressMsg{Code: tea.KeyEsc}, TrustDeny},
		{"ctrl+c", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, TrustDeny},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTrustDialogModel(testInspection())
			_, cmd := m.Update(tt.key)
			if !m.done {
				t.Fatalf("expected done=true")
			}
			if m.selected != tt.wantChoice {
				t.Fatalf("selected = %v, want %v", m.selected, tt.wantChoice)
			}
			if cmd == nil {
				t.Fatalf("expected a quit command")
			}
		})
	}
}

func TestTrustDialogTabThenEnter(t *testing.T) {
	m := newTrustDialogModel(testInspection())
	if m.selected != TrustDeny {
		t.Fatalf("expected initial selected = TrustDeny, got %v", m.selected)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.done {
		t.Fatalf("tab must not set done")
	}
	if m.selected != TrustAlways {
		t.Fatalf("after tab, selected = %v, want TrustAlways", m.selected)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.done {
		t.Fatalf("expected done=true after enter")
	}
	if m.selected != TrustAlways {
		t.Fatalf("selected after enter = %v, want TrustAlways", m.selected)
	}
	if cmd == nil {
		t.Fatalf("expected a quit command")
	}
}

func TestTrustDialogViewSecurityMarking(t *testing.T) {
	m := newTrustDialogModel(testInspection())
	view := m.View()
	if !strings.Contains(view.Content, "! sandbox.enabled") {
		t.Fatalf("expected view to contain %q, got:\n%s", "! sandbox.enabled", view.Content)
	}
	if !strings.Contains(view.Content, "  tui.fps") {
		t.Fatalf("expected view to contain %q, got:\n%s", "  tui.fps", view.Content)
	}
}

func TestTrustDialogViewPermanenceSentence(t *testing.T) {
	const sentence = "Once trusted, future changes to this project's config apply without asking."
	cases := []config.ProjectInspection{
		testInspection(),
		{ProjectRoot: "/x"},
		{ProjectRoot: "/x", ConfigPath: "/x/.steiner/config.yaml", ParseError: "bad yaml"},
		{ProjectRoot: "/x", ConfigPath: "/x/.steiner/config.yaml"},
	}
	for i, insp := range cases {
		m := newTrustDialogModel(insp)
		view := m.View()
		if !strings.Contains(view.Content, sentence) {
			t.Fatalf("case %d: expected permanence sentence in view, got:\n%s", i, view.Content)
		}
	}
}

func TestTrustDialogViewNoConfig(t *testing.T) {
	m := newTrustDialogModel(config.ProjectInspection{ProjectRoot: "/x"})
	view := m.View()
	if !strings.Contains(view.Content, "This project has no steiner config.") {
		t.Fatalf("expected no-config message, got:\n%s", view.Content)
	}
}

func TestTrustDialogViewParseError(t *testing.T) {
	m := newTrustDialogModel(config.ProjectInspection{
		ProjectRoot: "/x",
		ConfigPath:  "/x/.steiner/config.yaml",
		ParseError:  "bad yaml",
	})
	view := m.View()
	if !strings.Contains(view.Content, "could not be parsed: bad yaml") {
		t.Fatalf("expected parse-error message, got:\n%s", view.Content)
	}
}

func TestTrustDialogViewNoChanges(t *testing.T) {
	m := newTrustDialogModel(config.ProjectInspection{
		ProjectRoot: "/x",
		ConfigPath:  "/x/.steiner/config.yaml",
	})
	view := m.View()
	if !strings.Contains(view.Content, "does not change any settings.") {
		t.Fatalf("expected no-changes message, got:\n%s", view.Content)
	}
}

func TestTrustDialogScroll(t *testing.T) {
	insp := config.ProjectInspection{
		ProjectRoot: "/x",
		ConfigPath:  "/x/.steiner/config.yaml",
	}
	for i := 0; i < 20; i++ {
		insp.Changes = append(insp.Changes, config.FieldChange{
			Path:   "field" + string(rune('a'+i)),
			Before: "0",
			After:  "1",
		})
	}

	m := newTrustDialogModel(insp)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	initial := m.View().Content

	m.Update(tea.KeyPressMsg{Text: "j"})
	scrolled := m.View().Content

	if initial == scrolled {
		t.Fatalf("expected scrolled view to differ from initial view")
	}

	m.Update(tea.KeyPressMsg{Text: "k"})
	back := m.View().Content
	if back != initial {
		t.Fatalf("expected scrolling back up to restore the initial view")
	}
}

func TestNoticeDialogDismiss(t *testing.T) {
	m := newNoticeDialogModel("Notice", "something happened")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.done {
		t.Fatalf("expected done=true")
	}
	if cmd == nil {
		t.Fatalf("expected a quit command")
	}
}

func TestNoticeDialogView(t *testing.T) {
	m := newNoticeDialogModel("Notice", "something happened")
	view := m.View()
	if !strings.Contains(view.Content, "Notice") {
		t.Fatalf("expected title in view, got:\n%s", view.Content)
	}
	if !strings.Contains(view.Content, "something happened") {
		t.Fatalf("expected message in view, got:\n%s", view.Content)
	}
	if !strings.Contains(view.Content, "Press Enter to continue") {
		t.Fatalf("expected footer in view, got:\n%s", view.Content)
	}
}

func TestRunTrustDialogEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	choice, err := RunTrustDialog(ctx, DialogIO{In: strings.NewReader("a"), Out: io.Discard}, testInspection())
	if err != nil {
		t.Skipf("RunTrustDialog requires a real terminal input in this environment: %v", err)
	}
	if choice != TrustAlways {
		t.Fatalf("choice = %v, want TrustAlways", choice)
	}
}
