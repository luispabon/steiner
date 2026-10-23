package interactive

import (
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestEmitSaveWarningEmitsSessionSaveHealthDiagnostic(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
	})
	events = nil

	s.emitSaveWarning(errors.New("disk full"))

	if len(events) != 1 {
		t.Fatalf("events = %#v, want one session_health diagnostic", events)
	}
	payload, ok := events[0].Payload.(output.ContextSessionHealthEvent)
	if !ok {
		t.Fatalf("payload = %T, want ContextSessionHealthEvent", events[0].Payload)
	}
	if payload.Scope != output.SessionHealthScopeSave {
		t.Errorf("scope = %q, want %q", payload.Scope, output.SessionHealthScopeSave)
	}
	if got := output.ContextDiagnosticSeverity(payload); got != "warning" {
		t.Errorf("severity = %q, want warning", got)
	}
}
