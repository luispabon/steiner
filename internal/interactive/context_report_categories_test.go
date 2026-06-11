package interactive

import (
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func TestBlockMessageDurableContextUsesSystemRole(t *testing.T) {
	t.Parallel()

	message := blockMessage(prompt.ContextBlock{
		Source:  prompt.ContextSourceDurableContext,
		Path:    "retained context state",
		Content: `{"kind":"durable_context","content":"focus"}`,
	})

	if got, want := message.Role, provider.MessageRoleSystem; got != want {
		t.Fatalf("role = %q, want %q", got, want)
	}
}
