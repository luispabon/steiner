package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/session"
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

func TestConversationResetLeavesLoadedSessionImages(t *testing.T) {
	t.Parallel()
	store, write := newMarkerStore(t)
	oldPath, oldRef := write("old.png")
	m := newModel(Config{}, nil)
	m.imageStore = store
	m.imageMarkers = []imageMarker{{label: "[" + oldRef.ID + "]", image: agent.ImageBlock{ID: oldRef.ID, FilePath: oldPath}}}

	// The load rebinds the shared store before the TUI handles the reset event;
	// image IDs restart per session, so the new session reuses the same ID.
	if err := store.BindSession("loaded", 1); err != nil {
		t.Fatalf("BindSession: %v", err)
	}
	if err := os.MkdirAll(store.Dir(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	loadedPath := filepath.Join(store.Dir(), "loaded.png")
	if err := os.WriteFile(loadedPath, []byte("loaded"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	loadedRef := store.Register(loadedPath, "image/png", 1, 1, 6)
	if loadedRef.ID != oldRef.ID {
		t.Fatalf("loaded image ID = %q, want collision with %q", loadedRef.ID, oldRef.ID)
	}

	m.applyEvent(output.NewConversationResetEvent())

	if _, ok := store.Get(loadedRef.ID); !ok {
		t.Error("reset deleted the loaded session's image from the store")
	}
	if _, err := os.Stat(loadedPath); err != nil {
		t.Errorf("loaded session's image file removed: %v", err)
	}
	if len(m.imageMarkers) != 0 {
		t.Errorf("imageMarkers = %d, want 0", len(m.imageMarkers))
	}
}

func TestSessionPickerDeletesPendingImagesBeforeLoad(t *testing.T) {
	t.Parallel()
	store, write := newMarkerStore(t)
	path, ref := write("pending.png")
	m := newModel(Config{}, nil)
	m.controller = &testController{}
	m.imageStore = store
	m.imageMarkers = []imageMarker{{label: "[" + ref.ID + "]", image: agent.ImageBlock{ID: ref.ID, FilePath: path}}}
	m.sessionPicker = m.sessionPicker.Open([]session.IndexEntry{{ID: "s1", Title: "t"}})

	m.handleSessionPickerKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if _, ok := store.Get(ref.ID); ok {
		t.Error("pending image still in the store after switching sessions")
	}
	if len(m.imageMarkers) != 0 {
		t.Errorf("imageMarkers = %d, want 0", len(m.imageMarkers))
	}
}
