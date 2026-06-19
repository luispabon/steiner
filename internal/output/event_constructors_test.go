package output

import (
	"errors"
	"testing"
	"time"
)

func TestNewOneshotFinishedEvent(t *testing.T) {
	t.Run("without error", func(t *testing.T) {
		event := NewOneshotFinishedEvent("run-123", nil)
		if event.Type != EventTypeOneshotFinished {
			t.Fatalf("Type = %q, want %q", event.Type, EventTypeOneshotFinished)
		}
		if event.Timestamp.IsZero() {
			t.Fatal("Timestamp is zero")
		}
		if event.Timestamp.Location() != time.UTC {
			t.Fatalf("Location = %v, want UTC", event.Timestamp.Location())
		}
		p, ok := event.Payload.(OneshotFinishedEvent)
		if !ok {
			t.Fatalf("Payload type = %T", event.Payload)
		}
		if p.RunID != "run-123" {
			t.Fatalf("RunID = %q", p.RunID)
		}
		if p.Err != "" {
			t.Fatalf("Err = %q", p.Err)
		}
	})

	t.Run("with error", func(t *testing.T) {
		event := NewOneshotFinishedEvent("run-456", errors.New("something broke"))
		p, ok := event.Payload.(OneshotFinishedEvent)
		if !ok {
			t.Fatalf("Payload type = %T", event.Payload)
		}
		if p.RunID != "run-456" {
			t.Fatalf("RunID = %q", p.RunID)
		}
		if p.Err != "something broke" {
			t.Fatalf("Err = %q", p.Err)
		}
	})

	t.Run("UTC timestamp", func(t *testing.T) {
		event := NewOneshotFinishedEvent("", nil)
		if event.Timestamp.Location() != time.UTC {
			t.Fatalf("Location = %v", event.Timestamp.Location())
		}
	})
}
