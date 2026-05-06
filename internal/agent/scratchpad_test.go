package agent

import (
	"strings"
	"testing"
)

func TestScratchpadRenderContainsAllFields(t *testing.T) {
	s := Scratchpad{
		Intent:          "fix auth timeout",
		Decisions:       "use context deadline; avoid global state",
		Open:            "why does it only fail under load?",
		Next:            "add test reproducing timeout",
		WorkingFile:     "internal/auth/handler.go",
		LastAction:      "edited internal/auth/handler.go: tightened timeout handling",
		SessionState:    "session state: turn=4 compactions=1",
		TrackedFiles:    []string{"README.md lines 1-40/120"},
		RecentToolCalls: []string{"read path=README.md"},
	}
	got := s.Render()

	checks := []struct {
		label string
		want  string
	}{
		{"session", "session state: turn=4 compactions=1"},
		{"working file", "working file: internal/auth/handler.go"},
		{"last action", "last action: edited internal/auth/handler.go: tightened timeout handling"},
		{"tracked files", "tracked files:\n- README.md lines 1-40/120"},
		{"recent tool calls", "recent tool calls:\n- read path=README.md"},
		{"intent", "intent: fix auth timeout"},
		{"decisions", "decisions: use context deadline; avoid global state"},
		{"open", "open: why does it only fail under load?"},
		{"next", "next: add test reproducing timeout"},
	}
	for _, c := range checks {
		if !strings.Contains(got, c.want) {
			t.Errorf("Render() missing %s: %q not in %q", c.label, c.want, got)
		}
	}
	if !strings.Contains(got, "[Current task state]") {
		t.Fatalf("Render() missing top-level header: %q", got)
	}
}

func TestScratchpadRenderFieldOrder(t *testing.T) {
	s := Scratchpad{
		Intent:          "i",
		Decisions:       "d",
		Open:            "o",
		Next:            "n",
		SessionState:    "session state: turn=1 compactions=0",
		WorkingFile:     "f",
		LastAction:      "a",
		TrackedFiles:    []string{"tracked.txt"},
		RecentToolCalls: []string{"tool call"},
	}
	got := s.Render()
	order := []string{"session state:", "working file:", "last action:", "tracked files:", "recent tool calls:", "intent:", "decisions:", "open:", "next:"}
	prev := 0
	for _, field := range order {
		idx := strings.Index(got, field)
		if idx < prev {
			t.Errorf("field %q appears before expected position (prev=%d, idx=%d)", field, prev, idx)
		}
		prev = idx
	}
}

func TestScratchpadRenderNoXMLTags(t *testing.T) {
	s := Scratchpad{Intent: "test"}
	got := s.Render()
	if strings.Contains(got, "<scratchpad>") || strings.Contains(got, "</scratchpad>") {
		t.Errorf("Render() should not contain XML tags, got: %q", got)
	}
}

func TestScratchpadRenderEmptyFields(t *testing.T) {
	s := Scratchpad{Intent: "only intent set"}
	got := s.Render()
	if !strings.Contains(got, "[Current task state]") {
		t.Fatalf("Render() missing top-level header: %q", got)
	}
	if !strings.Contains(got, "intent: only intent set") {
		t.Errorf("Render() missing intent")
	}
	for _, field := range []string{"decisions:", "open:", "next:"} {
		if !strings.Contains(got, field) {
			t.Errorf("Render() missing field label %q", field)
		}
	}
}
