package tui

import (
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/session"
)

// listOnlySessionStore implements SessionLister but not oneshot.SessionStore
// (no Save method), so it drives the oneshot session-store failure path.
type listOnlySessionStore struct{}

func (listOnlySessionStore) List() ([]session.IndexEntry, error) { return nil, nil }

func (listOnlySessionStore) Load(string) (session.Session, error) { return session.Session{}, nil }

func TestOneshotSessionStoreOrEmitFailureEmitsOneshotReport(t *testing.T) {
	var events []output.Event
	sink := output.SinkFunc(func(ev output.Event) { events = append(events, ev) })

	store, ok := oneshotSessionStoreOrEmit(listOnlySessionStore{}, sink, "run-1")
	if ok || store != nil {
		t.Fatalf("oneshotSessionStoreOrEmit() = (%v, %v), want (nil, false)", store, ok)
	}
	if len(events) != 2 {
		t.Fatalf("emitted %d events, want 2", len(events))
	}

	report, isReport := events[0].Payload.(output.ContextReportEvent)
	if !isReport {
		t.Fatalf("first event payload = %T, want output.ContextReportEvent", events[0].Payload)
	}
	if report.Title != "Oneshot Report" {
		t.Errorf("overlay title = %q, want %q", report.Title, "Oneshot Report")
	}
	if report.Content != "session store does not implement required oneshot interface" {
		t.Errorf("overlay content = %q", report.Content)
	}
	if events[1].Type != output.EventTypeOneshotFinished {
		t.Errorf("second event type = %q, want %q", events[1].Type, output.EventTypeOneshotFinished)
	}
}
