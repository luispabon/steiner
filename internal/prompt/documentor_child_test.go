package prompt

import (
	"strings"
	"testing"
)

const (
	codeRoleSentence       = "You are steiner's code sub-agent. Complete the task in your brief within its stated scope."
	documentorRoleSentence = "You are steiner's documentor sub-agent. Write or update documentation as your brief describes, within its stated scope."
)

// TestDocumentorChildSectionOrder pins the documentor child identity, the
// shared worktree block, the single delegated-task authorization, and the
// Working rules section in both default and override section order.
func TestDocumentorChildSectionOrder(t *testing.T) {
	t.Parallel()

	worktreeDuties := strings.TrimSpace(renderTemplate("child_worktree.md.tmpl", nil))
	for _, tc := range []struct {
		name     string
		override string
	}{
		{name: "default sections"},
		{name: "override sections", override: "custom override"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := systemPreambleWithAdvisor(SystemPreambleParams{
				Override:       tc.override,
				AdvisorEnabled: true,
				LSPEnabled:     true,
				Mode:           DelegatedDocumentorWorkflowMode(),
			}).Content

			markers := []string{
				testIdentityMarker,
				documentorRoleSentence,
				"## Documentor duties",
				"### Write scope",
				"### Accuracy",
				"### Worktree and commits",
				worktreeDuties,
				"## Delegated task",
				"## Working rules",
				"### While editing",
			}
			last := -1
			for _, marker := range markers {
				idx := strings.Index(content, marker)
				if idx == -1 {
					t.Fatalf("documentor child missing %q in %q", marker, content)
				}
				if idx <= last {
					t.Fatalf("documentor child marker %q is out of order in %q", marker, content)
				}
				last = idx
			}

			if got := strings.Count(content, documentorRoleSentence); got != 1 {
				t.Fatalf("documentor role sentence count = %d, want 1", got)
			}
			if got := strings.Count(content, "## Delegated task"); got != 1 {
				t.Fatalf("delegated-task authorization count = %d, want 1", got)
			}
			for _, forbidden := range []string{
				"use `bash` only for read-only inspection, git, and",
				"## Code agent duties",
				codeRoleSentence,
				"## Work methodology",
				"### Planning",
				"### Splitting tasks into deliverables",
				"### Implementing",
				"## Your role",
				"## Your sub-agents",
				"## Continuing sub-agents",
				"## Delegation vs direct work",
				"## Briefing a sub-agent",
				"## Execution modes",
			} {
				if strings.Contains(content, forbidden) {
					t.Fatalf("documentor child contains %q", forbidden)
				}
			}
			for _, want := range []string{"## Advisor", "## Code intelligence (LSP)"} {
				if !strings.Contains(content, want) {
					t.Fatalf("documentor child missing enabled %q guidance", want)
				}
			}
		})
	}
}

// TestDocumentorChildBashAndVerificationRules pins the documentor-specific
// git restrictions and the documentation verification instruction, which the
// shared working rules do not cover.
func TestDocumentorChildBashAndVerificationRules(t *testing.T) {
	t.Parallel()

	content := systemPreambleWithAdvisor(SystemPreambleParams{Mode: DelegatedDocumentorWorkflowMode()}).Content

	for _, want := range []string{
		"the git subcommands `status`, `diff`, `log`, `show`, `add`, `commit`, and `rev-parse`",
		"Never run git commands that change working-tree files, for example `checkout`, `restore`, `apply`, `mv`, `rm`, `reset`, or `stash`.",
		"Before reporting completion, verify every claim you changed against the code, confirm relative links and anchors resolve, and report the checks you ran.",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("documentor child missing %q", want)
		}
	}
}

// TestParentPreambleCoversDocumentorDelegation pins the parent prompt's
// documentor mentions: plan mode denies documentor dispatch, and the async
// rules protect running documentor worktrees, not only code worktrees.
func TestParentPreambleCoversDocumentorDelegation(t *testing.T) {
	t.Parallel()

	content := systemPreambleWithAdvisor(SystemPreambleParams{
		DelegationEnabled: true,
		AsyncSubAgents:    true,
		Mode:              workflowModeParent,
	}).Content

	for _, want := range []string{
		"`code` and `documentor` delegation is denied",
		"Do not edit files in a running `code` or `documentor` agent's scope.",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("parent preamble missing %q", want)
		}
	}
}

// TestDocumentorChildSectionIsolatedByMode proves the documentor identity
// renders only for the documentor workflow mode, not for parent, code, or
// non-code child modes.
func TestDocumentorChildSectionIsolatedByMode(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		mode workflowMode
	}{
		{name: "parent", mode: workflowModeParent},
		{name: "code child", mode: workflowModeDelegatedCodeSubAgent},
		{name: "non-code child", mode: workflowModeDelegatedNonCodeChild},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := systemPreambleWithAdvisor(SystemPreambleParams{Mode: tc.mode}).Content
			for _, marker := range []string{documentorRoleSentence, "## Documentor duties", "### Write scope"} {
				if strings.Contains(content, marker) {
					t.Fatalf("%s preamble contains documentor marker %q", tc.name, marker)
				}
			}
		})
	}
}
