package output

import "testing"

func TestConversationEventConstructors(t *testing.T) {
	t.Parallel()

	state := NewConversationStateEvent("waiting", true, 2, false)
	if state.Type != EventTypeConversationState {
		t.Fatalf("Type = %q, want %q", state.Type, EventTypeConversationState)
	}
	got, ok := state.Payload.(ConversationStateEvent)
	if !ok {
		t.Fatalf("Payload = %T, want ConversationStateEvent", state.Payload)
	}
	if want := (ConversationStateEvent{State: "waiting", Held: true, Pending: 2}); got != want {
		t.Fatalf("payload = %#v, want %#v", got, want)
	}

	warn := NewConversationWarningEvent(ConversationWarningKindSave, "save failed")
	if warn.Type != EventTypeConversationWarning {
		t.Fatalf("Type = %q, want %q", warn.Type, EventTypeConversationWarning)
	}
	if p, ok := warn.Payload.(ConversationWarningEvent); !ok || p.Kind != ConversationWarningKindSave || p.Message != "save failed" {
		t.Fatalf("payload = %#v, want kind save and message save failed", warn.Payload)
	}
}
