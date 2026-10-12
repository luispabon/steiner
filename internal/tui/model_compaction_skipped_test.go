package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/output"
)

// TestModelSkippedCompactionClearsCompactingState proves a started compaction
// closed by a skipped event leaves the compacting state without reporting a
// compaction.
func TestModelSkippedCompactionClearsCompactingState(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	m = updateModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:     "compaction",
		Scope:    "conversation",
		Severity: "compacting",
		Turn:     3,
	})})
	if !m.compaction.Active() {
		t.Fatal("compaction.Active() = false after compacting event, want true")
	}

	m = updateModel(t, m, runtimeEventMsg{Event: output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:     "compaction",
		Scope:    "conversation",
		Severity: "skipped",
		Turn:     3,
		Notes:    []string{"compaction skipped: no stage applied"},
	})})

	if m.compaction.Active() {
		t.Fatal("compaction.Active() = true after skipped event, want false")
	}
	if m.sidebar.compaction.Active() {
		t.Fatal("sidebar.compaction.Active() = true after skipped event, want false")
	}
	if m.activity.label == "context compacted" {
		t.Fatalf("activity.label = %q, want no compaction claim for skipped compaction", m.activity.label)
	}
	if m.activity.label != "waiting" || !m.activity.busy() {
		t.Fatalf("activity = %#v, want spinning waiting state like a model call", m.activity)
	}
	got := m.content.String(m.viewport.Width())
	if !strings.Contains(got, "skipped: no stage applied") {
		t.Fatalf("content = %q, want skipped compaction banner", got)
	}
	if strings.Contains(got, "✓") {
		t.Fatalf("content = %q, want no success mark for skipped compaction", got)
	}
	if strings.Contains(got, "Context compacted") {
		t.Fatalf("content = %q, want no compacted banner for skipped compaction", got)
	}
}
