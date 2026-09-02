package tui

import (
	"testing"
)

func TestParseStructuredDelegateBriefWithObjective(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"objective":       "implement feature X",
		"context":         "background info",
		"deliverable":     "working code",
		"constraints":     []any{"constraint 1", "constraint 2"},
		"success_criteria": []any{"success 1"},
		"checks":          []any{},
	}

	b, ok := parseStructuredDelegateBrief(args)
	if !ok {
		t.Fatal("parseStructuredDelegateBrief returned ok=false, want true")
	}
	if b.objective != "implement feature X" {
		t.Errorf("objective = %q, want %q", b.objective, "implement feature X")
	}
	if b.context != "background info" {
		t.Errorf("context = %q, want %q", b.context, "background info")
	}
	if b.deliverable != "working code" {
		t.Errorf("deliverable = %q, want %q", b.deliverable, "working code")
	}
	if len(b.constraints) != 2 {
		t.Errorf("constraints length = %d, want 2", len(b.constraints))
	}
	if len(b.successCriteria) != 1 {
		t.Errorf("successCriteria length = %d, want 1", len(b.successCriteria))
	}
	if b.checks != nil {
		t.Errorf("checks = %v, want nil", b.checks)
	}
}

func TestParseStructuredDelegateBriefObjectiveBlank(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"objective": "  ",
		"context":   "background",
	}

	_, ok := parseStructuredDelegateBrief(args)
	if ok {
		t.Error("parseStructuredDelegateBrief returned ok=true, want false (blank objective)")
	}
}

func TestParseStructuredDelegateBriefNoObjective(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"context": "background",
	}

	_, ok := parseStructuredDelegateBrief(args)
	if ok {
		t.Error("parseStructuredDelegateBrief returned ok=true, want false (missing objective)")
	}
}

func TestParseStructuredDelegateBriefFiltersEmptyStrings(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"objective": "task",
		"constraints": []any{"c1", "  ", "c2", ""},
	}

	b, ok := parseStructuredDelegateBrief(args)
	if !ok {
		t.Fatal("parseStructuredDelegateBrief returned ok=false, want true")
	}
	if len(b.constraints) != 2 {
		t.Errorf("constraints length = %d, want 2 (empty strings filtered)", len(b.constraints))
	}
}

func TestDelegateArgTextIncludesObjective(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"objective": "primary goal",
		"task":      "secondary",
	}

	result := delegateArgText(args)
	if result != "primary goal" {
		t.Errorf("delegateArgText = %q, want %q (objective takes priority)", result, "primary goal")
	}
}
