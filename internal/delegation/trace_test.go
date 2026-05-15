package delegation

import (
	"testing"
)

func TestTraceCollector_EmptyResult(t *testing.T) {
	tc := newTraceCollector("agent-1", "some task")
	entries := tc.result()
	if entries != nil {
		t.Errorf("expected nil entries for empty collector, got %d", len(entries))
	}
}

func TestTraceCollector_AddsEntries(t *testing.T) {
	tc := newTraceCollector("agent-1", "test task")
	tc.add("start", "delegation started", map[string]any{"max_turns": 15})
	tc.add("child_run_complete", "initial run finished", nil)

	entries := tc.result()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Phase != "start" {
		t.Errorf("expected phase 'start', got %q", entries[0].Phase)
	}
	if entries[0].Message != "delegation started" {
		t.Errorf("expected message 'delegation started', got %q", entries[0].Message)
	}
	if entries[0].Fields["max_turns"] != 15 {
		t.Errorf("expected max_turns=15, got %v", entries[0].Fields["max_turns"])
	}
	if entries[1].Fields != nil {
		t.Errorf("expected nil fields for second entry, got %v", entries[1].Fields)
	}
}

func TestTraceCollector_ResultIsACopy(t *testing.T) {
	tc := newTraceCollector("agent-1", "task")
	tc.add("a", "first", nil)
	first := tc.result()
	tc.add("b", "second", nil)
	second := tc.result()

	if len(first) != 1 {
		t.Errorf("first result should have 1 entry, got %d", len(first))
	}
	if len(second) != 2 {
		t.Errorf("second result should have 2 entries, got %d", len(second))
	}
}
