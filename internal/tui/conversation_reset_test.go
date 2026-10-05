package tui

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestConversationResetReplacesTranscript(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.applyEvent(output.NewUserInputEvent("alpha session", "resume", nil))

	// Resume A, then B: only B may remain visible.
	m.applyEvent(output.NewConversationResetEvent())
	m.applyEvent(output.NewUserInputEvent("bravo session", "resume", nil))

	got := stripANSI(m.content.String(80))
	if strings.Contains(got, "alpha session") {
		t.Errorf("transcript = %q, want alpha session cleared", got)
	}
	if !strings.Contains(got, "bravo session") {
		t.Errorf("transcript = %q, want replayed bravo session", got)
	}
}

func TestConversationResetKeepsLoadChrome(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.status.mode = "plan"
	m.input.SetValue("draft in progress")
	cleanups := 0
	m.sessionResetCleanup = func() { cleanups++ }

	m.applyEvent(output.NewConversationResetEvent())

	if m.status.mode != "plan" {
		t.Errorf("status.mode = %q, want %q: the mode listener owns it on load", m.status.mode, "plan")
	}
	if m.input.Value() != "draft in progress" {
		t.Errorf("composer = %q, want draft kept", m.input.Value())
	}
	if cleanups != 0 {
		t.Errorf("sessionResetCleanup calls = %d, want 0: the event also fires on startup resume", cleanups)
	}
}

func TestConversationResetDropsPreviousSessionState(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m.roster.upsert("agent-1")
	m.strandedResults = 2
	m.enabledSkills["review"] = true
	m.sidebar.promptUsed = 500
	m.activity = m.activity.waiting("running", "model")

	m.applyEvent(output.NewConversationResetEvent())

	if got := len(m.roster.entries); got != 0 {
		t.Errorf("roster entries = %d, want 0", got)
	}
	if m.strandedResults != 0 {
		t.Errorf("strandedResults = %d, want 0", m.strandedResults)
	}
	if m.enabledSkills["review"] {
		t.Error("skill review still enabled, want replay to re-enable it")
	}
	if m.sidebar.promptUsed != 0 {
		t.Errorf("sidebar.promptUsed = %d, want 0", m.sidebar.promptUsed)
	}
	if m.activity.spinning {
		t.Error("activity still spinning after reset")
	}
}
