package prompt

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

func TestBlockMessageRoleBySource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source ContextSource
		want   provider.MessageRole
	}{
		{name: "preamble", source: ContextSourcePreamble, want: provider.MessageRoleSystem},
		{name: "phase prompt", source: ContextSourcePhasePrompt, want: provider.MessageRoleSystem},
		{name: "global agents", source: ContextSourceGlobalAgentsMD, want: provider.MessageRoleSystem},
		{name: "project agents", source: ContextSourceProjectAgentsMD, want: provider.MessageRoleSystem},
		{name: "conversation summary", source: ContextSourceConversationSummary, want: provider.MessageRoleSystem},
		{name: "project context", source: ContextSourceProjectContext, want: provider.MessageRoleUser},
		{name: "skill", source: ContextSourceSkill, want: provider.MessageRoleUser},
		{name: "durable context", source: ContextSourceDurableContext, want: provider.MessageRoleUser},
		{name: "conversation", source: ContextSourceConversation, want: provider.MessageRoleUser},
		{name: "session date", source: ContextSourceSessionDate, want: provider.MessageRoleUser},
		{name: "unknown source", source: ContextSource("unknown"), want: provider.MessageRoleUser},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			message := blockMessage(ContextBlock{Source: tt.source, Content: "content"})

			if got := message.Role; got != tt.want {
				t.Fatalf("role = %q, want %q", got, tt.want)
			}
		})
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

func TestApplyBudgetExemptsPreambleAndPhasePrompt(t *testing.T) {
	t.Parallel()

	policy := DefaultAssemblyPolicy()
	policy.Budgets.SkillBytes = 8
	tracker := newBudgetTracker(policy.Budgets)
	oversized := strings.Repeat("x", 1000)

	tests := []struct {
		name   string
		source ContextSource
	}{
		{name: "preamble", source: ContextSourcePreamble},
		{name: "phase prompt", source: ContextSourcePhasePrompt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clipped, truncated, ok := applyBudget(tracker, tt.source, oversized)

			if !ok {
				t.Fatalf("applyBudget(%q) ok = false, want true (source is exempt)", tt.source)
			}
			if truncated {
				t.Fatalf("truncated = true, want false for exempt source %q", tt.source)
			}
			if got, want := clipped, oversized; got != want {
				t.Fatalf("clipped = %q, want %q (unchanged)", got, want)
			}
		})
	}

	// Contrast: a budgeted source with oversized content must still be clipped,
	// so the exempt-source assertions above cannot pass vacuously.
	clipped, truncated, ok := applyBudget(tracker, ContextSourceSkill, oversized)
	if !ok {
		t.Fatal("applyBudget(ContextSourceSkill) ok = false, want true")
	}
	if !truncated {
		t.Fatal("truncated = false for budgeted source, want true")
	}
	if len(clipped) >= len(oversized) {
		t.Fatalf("clipped length = %d, want < %d", len(clipped), len(oversized))
	}
}
