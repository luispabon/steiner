package prompt

import (
	"strings"
	"testing"
)

func TestSystemPreambleHasNoToolGuidance(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", false, false, "").Content
	// Tool guidance and patch format moved to tool descriptions — must not appear in system prompt.
	// Note: delegation guidance (## Delegation block) is workflow strategy, not tool mechanics — it is intentionally absent from this test's assertions.
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

func TestSystemPreambleDelegationInstructions(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", true, false, "").Content
	for _, want := range []string{
		"## Delegation",
		"Every file you read locally stays in your context for the rest of the conversation",
		"Sub-agent context is ephemeral",
		"Default to delegation; work locally only when the conditions below are clearly met.",
		"Before acting on any task, classify it into one of:",
		"Investigation: find files, usages, patterns, duplication, bug locations, or design risks. Always delegate via `explore`.",
		"Research: inspect docs, APIs, dependencies, repo history, or prior examples. Always delegate via `research`.",
		"Delegate via `code` unless you are already mid-edit in the same file.",
		"Delegate via `verify`, especially when you can continue other work.",
		"Review: inspect code or changes for bugs, regressions, missing tests, or plan adherence. Delegate via `explore` or `plan`.",
		"Work locally only when ALL of:",
		"The result is needed in your current context",
		"Never work locally when:",
		"You need to read 2+ files to understand something",
		"You need to find where something is defined or used",
		"You are about to grep then read the results",
		"The task is separable from your current work",
		"Sub-agents receive only the task you provide.",
		"Sub-agents cannot delegate further or ask the user questions.",
		"Every sub-agent task MUST use the template below.",
		"Objective: what the sub-agent must accomplish",
		"Context: file paths, symbols, or background",
		"Deliverable: the concrete output expected",
		"Constraints: boundaries",
		"Success criteria: how the sub-agent knows it is done",
		"`plan` is for focused sub-problem analysis, not overall task planning.",
		"| `explore` | Navigate the codebase: find files, symbols, patterns, usages, or call sites |",
		"| `research` | Gather information: search the web, read docs, synthesize external sources |",
		"| `code` | Implement a scoped change: write code, run tests, fix errors |",
		"| `plan` | Analyze a specific sub-problem: evaluate options, tradeoffs, produce a recommendation |",
		"| `verify` | Run checks: tests, lint, build. Report pass/fail. No code changes |",
		"| `delegate` | Generic: when no specialized type fits, or when you need custom tool access or system prompt |",
		"| Find DRY/refactoring opportunities across the codebase | `explore`: report files, repeated patterns, risks, and next steps. |",
		"| Understand how a feature works across multiple files | `explore`: trace the call chain and report. |",
		"| Read one file you are about to edit | Work locally. |",
		"Ask a sub-agent to find something across multiple files",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("delegation preamble missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"Prefer specialized delegate tools when the task fits.",
		"Before starting a task locally, classify it.",
		"One known tool call is enough",
		"tightly coupled to your current edits",
		"The result would immediately require another delegation",
		"The task is too vague for an independent agent to know success.",
		"Exploring the codebase to answer a factual question",
		"Implementing a bounded change scoped to 1–3 files where the requirements are clear",
		"Running verification (tests, lint, build) and interpreting results",
		"Reviewing or analysing code in files you have not yet read",
		"Searching for information across many files (grep + read chains)",
		"Performing a refactor with known, mechanical scope",
		"Use `delegate` for separable work another agent can complete independently and summarize back.",
		"When delegating, pass a self-contained task with paths/search terms, constraints, ownership, expected output",
		"| Read one known file or inspect one known diff | Work locally. |",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("delegation preamble still contains old guidance %q", forbidden)
		}
	}
}

func TestSystemPreambleDelegationAbsentWhenDisabled(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", false, false, "").Content
	if strings.Contains(content, "## Delegation") {
		t.Fatalf("delegation instructions present when delegationEnabled=false")
	}
}

func TestSystemPreambleDelegationIncludedWhenEnabled(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", true, false, "").Content
	if !strings.Contains(content, "## Delegation") {
		t.Fatalf("delegation instructions missing when enabled")
	}
}

func TestSystemPreambleCavemanMode(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", false, true, "").Content
	if !strings.Contains(content, "Respond terse") {
		t.Fatalf("caveman mode preamble missing terse instruction in %q", content)
	}
}

func TestSystemPreambleSystemSuffix(t *testing.T) {
	cases := []struct {
		name       string
		suffix     string
		wantIn     string
		wantInLast bool
	}{
		{
			name:       "empty suffix produces unchanged output",
			suffix:     "",
			wantIn:     "You are steiner",
			wantInLast: false,
		},
		{
			name:       "suffix appended after default preamble",
			suffix:     "Custom instruction here.",
			wantIn:     "Custom instruction here.",
			wantInLast: true,
		},
		{
			name:       "suffix appended after caveman mode",
			suffix:     "Extended thinking enabled",
			wantIn:     "Extended thinking enabled",
			wantInLast: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := SystemPreamble("", false, false, tc.suffix).Content
			if !strings.Contains(content, tc.wantIn) {
				t.Fatalf("preamble missing %q", tc.wantIn)
			}
			if tc.wantInLast && tc.suffix != "" {
				if !strings.HasSuffix(strings.TrimSpace(content), tc.suffix) {
					t.Fatalf("suffix %q not at end of preamble", tc.suffix)
				}
			}
		})
	}
}

func TestSystemPreambleSuffixAfterCavemanBlock(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", false, true, "Extended thinking enabled").Content
	// Verify caveman instruction comes before suffix
	cavemanIdx := strings.Index(content, "Respond terse")
	suffixIdx := strings.Index(content, "Extended thinking enabled")

	if cavemanIdx == -1 {
		t.Fatalf("caveman instruction not found in content")
	}
	if suffixIdx == -1 {
		t.Fatalf("suffix not found in content")
	}
	if cavemanIdx >= suffixIdx {
		t.Fatalf("caveman block should appear before suffix")
	}
	// Verify suffix is at the end
	if !strings.HasSuffix(strings.TrimSpace(content), "Extended thinking enabled") {
		t.Fatalf("suffix should be at the end of preamble")
	}
}

func TestSystemPreambleSuffixAfterOverride(t *testing.T) {
	t.Parallel()

	override := "Custom system prompt"
	suffix := "Additional instruction"
	content := SystemPreamble(override, false, false, suffix).Content

	if !strings.Contains(content, override) {
		t.Fatalf("override not found in content")
	}
	if !strings.Contains(content, suffix) {
		t.Fatalf("suffix not found in content")
	}
	if !strings.HasSuffix(strings.TrimSpace(content), suffix) {
		t.Fatalf("suffix should appear after override")
	}
}
