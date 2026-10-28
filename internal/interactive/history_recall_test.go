package interactive

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestRequestHistoryRecallEmitsReport(t *testing.T) {
	t.Parallel()
	const archive = "# Compacted\n\nEarlier turns."
	longErr := errors.New(strings.Repeat("e", 150) + "\nsecond line")
	tests := []struct {
		name        string
		noStore     bool
		setup       func(store *mockSessionStore, id string)
		wantContent string
		wantDisplay string
		wantScroll  bool
	}{
		{
			name:        "success opens overlay scrolled to end",
			setup:       func(store *mockSessionStore, id string) { store.history[id] = archive },
			wantContent: archive,
			wantDisplay: output.ContextReportDisplayOverlay,
			wantScroll:  true,
		},
		{
			name:        "missing archive is inline notice",
			setup:       func(*mockSessionStore, string) {},
			wantContent: "No archived conversation history for this session yet.",
			wantDisplay: output.ContextReportDisplayInline,
		},
		{
			name:        "blank archive is inline notice",
			setup:       func(store *mockSessionStore, id string) { store.history[id] = " \n\t " },
			wantContent: "No archived conversation history for this session yet.",
			wantDisplay: output.ContextReportDisplayInline,
		},
		{
			name:        "nil store is inline notice",
			noStore:     true,
			wantContent: "No archived conversation history for this session yet.",
			wantDisplay: output.ContextReportDisplayInline,
		},
		{
			name:        "read error is inline notice",
			setup:       func(store *mockSessionStore, _ string) { store.historyErr = errors.New("disk offline") },
			wantContent: "Could not read conversation history: disk offline",
			wantDisplay: output.ContextReportDisplayInline,
		},
		{
			name:        "long multi-line read error stays inline",
			setup:       func(store *mockSessionStore, _ string) { store.historyErr = longErr },
			wantContent: "Could not read conversation history: " + longErr.Error(),
			wantDisplay: output.ContextReportDisplayInline,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var events []output.Event
			deps := Dependencies{BaseEvents: output.SinkFunc(func(event output.Event) {
				events = append(events, event)
			})}
			store := newMockSessionStore()
			if !tt.noStore {
				deps.SessionStore = store
			}
			s, err := NewSession(deps)
			if err != nil {
				t.Fatalf("NewSession failed: %v", err)
			}
			if tt.setup != nil {
				tt.setup(store, s.SessionID())
			}

			if err := s.Handle(context.Background(), RequestHistoryRecall{}); err != nil {
				t.Fatalf("Handle(RequestHistoryRecall) = %v, want nil", err)
			}

			var reports []output.ContextReportEvent
			for _, event := range events {
				if payload, ok := event.Payload.(output.ContextReportEvent); ok {
					reports = append(reports, payload)
				}
			}
			if len(reports) != 1 {
				t.Fatalf("context report events = %d, want 1", len(reports))
			}
			got := reports[0]
			if got.Title != "Conversation history" {
				t.Fatalf("title = %q, want %q", got.Title, "Conversation history")
			}
			if got.Content != tt.wantContent {
				t.Fatalf("content = %q, want %q", got.Content, tt.wantContent)
			}
			if got.Display != tt.wantDisplay {
				t.Fatalf("display = %q, want %q", got.Display, tt.wantDisplay)
			}
			if got.ScrollToEnd != tt.wantScroll {
				t.Fatalf("scroll_to_end = %v, want %v", got.ScrollToEnd, tt.wantScroll)
			}
		})
	}
}
