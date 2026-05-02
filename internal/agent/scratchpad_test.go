package agent

import (
	"strings"
	"testing"
)

func TestScratchpadRenderContainsAllFields(t *testing.T) {
	s := Scratchpad{
		Goal:      "fix auth timeout",
		Plan:      "1. Reproduce 2. Fix 3. Test",
		Step:      "reading auth code",
		Decisions: "use context deadline; avoid global state",
		Files:     "internal/auth/handler.go (read)",
		Open:      "why does it only fail under load?",
		Next:      "add test reproducing timeout",
	}
	got := s.Render()

	checks := []struct {
		label string
		want  string
	}{
		{"header", "[Current task state]"},
		{"goal", "goal: fix auth timeout"},
		{"plan", "plan: 1. Reproduce 2. Fix 3. Test"},
		{"step", "step: reading auth code"},
		{"decisions", "decisions: use context deadline; avoid global state"},
		{"files", "files: internal/auth/handler.go (read)"},
		{"open", "open: why does it only fail under load?"},
		{"next", "next: add test reproducing timeout"},
	}
	for _, c := range checks {
		if !strings.Contains(got, c.want) {
			t.Errorf("Render() missing %s: %q not in %q", c.label, c.want, got)
		}
	}
}

func TestScratchpadRenderFieldOrder(t *testing.T) {
	s := Scratchpad{
		Goal:      "g",
		Plan:      "p",
		Step:      "s",
		Decisions: "d",
		Files:     "f",
		Open:      "o",
		Next:      "n",
	}
	got := s.Render()
	// Verify order: goal < plan < step < decisions < files < open < next
	order := []string{"goal:", "plan:", "step:", "decisions:", "files:", "open:", "next:"}
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
	s := Scratchpad{Goal: "test"}
	got := s.Render()
	if strings.Contains(got, "<scratchpad>") || strings.Contains(got, "</scratchpad>") {
		t.Errorf("Render() should not contain XML tags, got: %q", got)
	}
}

func TestScratchpadRenderEmptyFields(t *testing.T) {
	s := Scratchpad{Goal: "only goal set"}
	got := s.Render()
	if !strings.Contains(got, "[Current task state]") {
		t.Errorf("Render() missing header")
	}
	if !strings.Contains(got, "goal: only goal set") {
		t.Errorf("Render() missing goal")
	}
	// Other fields present with empty values.
	for _, field := range []string{"plan:", "step:", "decisions:", "files:", "open:", "next:"} {
		if !strings.Contains(got, field) {
			t.Errorf("Render() missing field label %q", field)
		}
	}
}
