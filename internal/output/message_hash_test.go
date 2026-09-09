package output

import (
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func TestNewAPIRequestEvent_EmptyMessagePreservesHash(t *testing.T) {
	event := NewAPIRequestEvent("model", []provider.Message{{}}, nil, nil, nil, prompt.ModelTokenBudget{}, 0, 0)
	payload := event.Payload.(APIRequestEvent)
	if got, want := payload.MessageHashes[0], "e3b0c442"; got != want {
		t.Fatalf("MessageHashes[0] = %q, want %q", got, want)
	}
}
