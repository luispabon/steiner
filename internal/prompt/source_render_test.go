package prompt

import (
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

func TestBlockMessageDurableContextUsesUserRole(t *testing.T) {
	t.Parallel()

	message := blockMessage(ContextBlock{
		Source:  ContextSourceDurableContext,
		Path:    "retained context state",
		Content: `{"kind":"durable_context","content":"focus"}`,
	})

	if got, want := message.Role, provider.MessageRoleUser; got != want {
		t.Fatalf("role = %q, want %q", got, want)
	}
}
