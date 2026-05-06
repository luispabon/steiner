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

func TestSystemPreambleMutationGuidanceUsesApplyPatch(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", true).Content
	for _, want := range []string{
		"- Use apply_patch for all file mutations.",
		"Patch format:",
		"*** Begin Patch",
		"*** Add File: path/to/file",
		"*** Update File: path/to/file",
		"*** Delete File: path/to/file",
		"- Do not use bash, sed, perl, python, cat, tee, or shell redirection for ad-hoc file edits.",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("system preamble missing %q in %q", want, content)
		}
	}
	for _, forbidden := range []string{
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
