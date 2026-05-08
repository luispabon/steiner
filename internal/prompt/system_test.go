package prompt

import (
	"strings"
	"testing"
)

func TestSystemPreambleScratchpadInstructionsUseCurrentFourFieldSchema(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", true, false).Content
	for _, want := range []string{
		"- intent: what you are trying to achieve right now",
		"- decisions: key choices made and why",
		"- open: unresolved problems or unknowns blocking progress",
		"- next: the single next action you will take after this turn",
	} {
		if got := strings.Count(content, want); got != 1 {
			t.Fatalf("system preamble count for %q = %d, want 1 in %q", want, got, content)
		}
	}
	for _, forbidden := range []string{"goal:", "plan:", "step:", "files:"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("system preamble still contains %q in %q", forbidden, content)
		}
	}
}

func TestSystemPreambleHasNoToolGuidance(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", true, false).Content
	// Tool guidance and patch format moved to tool descriptions — must not appear in system prompt.
	for _, forbidden := range []string{
		"Tool guidance:",
		"Patch format:",
		"*** Begin Patch",
		"*** End Patch",
		"Use apply_patch for all file mutations.",
		"Use edit for targeted modifications.",
		"Use write only for new files or intentional full rewrites.",
		"old_string",
		"new_string",
		"path+hunks",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("system preamble still contains %q in %q", forbidden, content)
		}
	}
}
