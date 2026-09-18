package tui

import (
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

// TestApplyEventHistoryLoadedDoesNotMutatePayload proves the TUI copies the
// history payload before reversing it: the slice belongs to the producer and is
// also retained by the transcript, so reversing or aliasing it in place would
// corrupt both.
func TestApplyEventHistoryLoadedDoesNotMutatePayload(t *testing.T) {
	m := newModel(Config{}, nil)

	original := []string{"one", "two", "three"}
	event := output.NewHistoryLoadedEvent(original)
	_ = m.applyEvent(event)

	want := []string{"three", "two", "one"}
	if !reflect.DeepEqual(m.fileHistory, want) {
		t.Fatalf("fileHistory = %v, want %v", m.fileHistory, want)
	}
	if m.fileHistoryIdx != -1 {
		t.Fatalf("fileHistoryIdx = %d, want -1", m.fileHistoryIdx)
	}
	if !reflect.DeepEqual(original, []string{"one", "two", "three"}) {
		t.Fatalf("caller slice mutated = %v, want [one two three]", original)
	}

	payload := event.Payload.(output.HistoryLoadedEvent)
	if !reflect.DeepEqual(payload.Prompts, []string{"one", "two", "three"}) {
		t.Fatalf("event payload mutated = %v", payload.Prompts)
	}

	m.fileHistory[0] = "changed"
	if payload.Prompts[0] != "one" {
		t.Fatalf("fileHistory aliases the event payload: payload[0] = %q", payload.Prompts[0])
	}
}
