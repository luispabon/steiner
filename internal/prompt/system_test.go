package prompt

import (
	"strings"
	"testing"
)

func TestSystemPreambleScratchpadInstructionsUseCurrentFourFieldSchema(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", true).Content
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
