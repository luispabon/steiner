package prompt

import (
	"strings"
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

func TestApplyBudgetTruncatesOversizedContent(t *testing.T) {
	t.Parallel()

	t.Run("content under budget", func(t *testing.T) {
		t.Parallel()

		policy := DefaultAssemblyPolicy()
		policy.Budgets.SkillBytes = 100
		tracker := newBudgetTracker(policy.Budgets)
		content := "small content"

		clipped, truncated, ok := applyBudget(tracker, ContextSourceSkill, content)

		if !ok {
			t.Fatalf("applyBudget() ok = false, want true")
		}
		if truncated {
			t.Fatalf("truncated = true, want false (content under budget)")
		}
		if got, want := clipped, content; got != want {
			t.Fatalf("clipped = %q, want %q (unchanged)", got, want)
		}
	})

	t.Run("content exceeds budget", func(t *testing.T) {
		t.Parallel()

		policy := DefaultAssemblyPolicy()
		policy.Budgets.SkillBytes = 50
		tracker := newBudgetTracker(policy.Budgets)
		content := strings.Repeat("x", 1000)

		clipped, truncated, ok := applyBudget(tracker, ContextSourceSkill, content)

		if !ok {
			t.Fatalf("applyBudget() ok = false, want true")
		}
		if !truncated {
			t.Fatalf("truncated = false, want true (content exceeds budget)")
		}
		// Clipped content must be shorter than original
		if len(clipped) >= len(content) {
			t.Fatalf("clipped length = %d, want < %d (original)", len(clipped), len(content))
		}
		// Clipped content should not exceed the budget (with some tolerance for UTF-8 safe cutting)
		if len(clipped) > 50 {
			t.Fatalf("clipped length = %d, want <= 50 (budget)", len(clipped))
		}
	})
}
