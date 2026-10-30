package output

import (
	"encoding/json"
	"strings"
	"testing"
)

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

func TestConversationStateEventJSONOmitsZeroEpoch(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(NewConversationStateEvent("idle", false, 0, false))
	if err != nil {
		t.Fatalf("marshal unstamped event: %v", err)
	}
	if strings.Contains(string(encoded), "epoch") {
		t.Fatalf("unstamped event = %s, want the epoch field omitted", encoded)
	}

	event := NewConversationStateEvent("waiting", true, 2, false)
	payload, ok := event.Payload.(ConversationStateEvent)
	if !ok {
		t.Fatalf("Payload = %T, want ConversationStateEvent", event.Payload)
	}
	payload.Epoch = 3
	event.Payload = payload
	encoded, err = json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal stamped event: %v", err)
	}
	if !strings.Contains(string(encoded), `"epoch":3`) {
		t.Fatalf("stamped event = %s, want epoch 3", encoded)
	}
}
