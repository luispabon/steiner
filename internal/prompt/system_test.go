package prompt

import (
	"strings"
	"testing"
)

const (
	testIdentityMarker   = "You are steiner, a lean coding agent."
	testDelegationMarker = "## Your role"
	testCoreRulesMarker  = "Core rules:"
	testWorkflowMarker   = "Before editing:"
	testCaveHumanMarker  = "## Output voice"
	parentApprovalLine   = "Ask for user's permission before editing."
	childApprovalLine    = "Do not ask for permission to proceed or for confirmation before editing."
)

func TestSystemPreambleHasNoToolGuidance(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", false, false, "").Content
	// Tool guidance and patch format moved to tool descriptions — must not appear in system prompt.
	// Note: delegation guidance (## Your role block) is workflow strategy, not tool mechanics — it is intentionally absent from this test's assertions.
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

func TestSystemPreambleSectionsAndOrdering(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		override        string
		delegation      bool
		advisor         bool
		caveHuman       bool
		suffix          string
		wantPresent     []string
		wantAbsent      []string
		wantOrder       []string
		wantSuffixLast  bool
		wantCoreAbsent  []string
		wantIdentityCnt int
	}{
		{
			name:            "delegation enabled",
			delegation:      true,
			wantPresent:     []string{testIdentityMarker, testDelegationMarker, testCoreRulesMarker, testWorkflowMarker},
			wantOrder:       []string{testIdentityMarker, testDelegationMarker, testCoreRulesMarker, testWorkflowMarker},
			wantCoreAbsent:  []string{"Default to delegation; work locally only when the conditions below are clearly met.", "Sub-agents receive only the task you provide.", "Every sub-agent task MUST use the template below.", "Use `delegate` for separable work another agent can complete independently and summarize back."},
			wantIdentityCnt: 1,
		},
		{
			name:            "delegation disabled",
			delegation:      false,
			wantPresent:     []string{testIdentityMarker, testCoreRulesMarker, testWorkflowMarker},
			wantAbsent:      []string{testDelegationMarker},
			wantOrder:       []string{testIdentityMarker, testCoreRulesMarker, testWorkflowMarker},
			wantCoreAbsent:  []string{"Default to delegation; work locally only when the conditions below are clearly met.", "Sub-agents receive only the task you provide.", "Every sub-agent task MUST use the template below."},
			wantIdentityCnt: 1,
		},
		{
			name:            "cave-human append after base sections",
			delegation:      true,
			caveHuman:       true,
			suffix:          "system suffix",
			wantPresent:     []string{testIdentityMarker, testDelegationMarker, testCoreRulesMarker, testWorkflowMarker, testCaveHumanMarker, "system suffix"},
			wantOrder:       []string{testIdentityMarker, testDelegationMarker, testCoreRulesMarker, testWorkflowMarker, testCaveHumanMarker, "system suffix"},
			wantSuffixLast:  true,
			wantCoreAbsent:  []string{"Default to delegation; work locally only when the conditions below are clearly met.", "Sub-agents receive only the task you provide.", "Every sub-agent task MUST use the template below."},
			wantIdentityCnt: 1,
		},
		{
			name:            "override preserves identity and delegation",
			override:        "Custom override content",
			delegation:      true,
			suffix:          "suffix",
			wantPresent:     []string{testIdentityMarker, testDelegationMarker, "Custom override content", "suffix"},
			wantAbsent:      []string{testCoreRulesMarker, testWorkflowMarker},
			wantOrder:       []string{testIdentityMarker, testDelegationMarker, "Custom override content", "suffix"},
			wantSuffixLast:  true,
			wantIdentityCnt: 1,
		},
		{
			name:            "override without delegation",
			override:        "Custom override content",
			delegation:      false,
			wantPresent:     []string{testIdentityMarker, "Custom override content"},
			wantAbsent:      []string{testDelegationMarker, testCoreRulesMarker, testWorkflowMarker},
			wantOrder:       []string{testIdentityMarker, "Custom override content"},
			wantIdentityCnt: 1,
		},
		{
			name:            "advisor enabled",
			advisor:         true,
			wantPresent:     []string{testIdentityMarker, "## Advisor", testCoreRulesMarker, testWorkflowMarker},
			wantOrder:       []string{testIdentityMarker, "## Advisor", testCoreRulesMarker, testWorkflowMarker},
			wantIdentityCnt: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := systemPreambleWithAdvisor(SystemPreambleParams{
				Override:          tc.override,
				DelegationEnabled: tc.delegation,
				AdvisorEnabled:    tc.advisor,
				Mode:              workflowModeParent,
				CaveHuman:         tc.caveHuman,
				SystemSuffix:      tc.suffix,
			}).Content

			for _, want := range tc.wantPresent {
				if !strings.Contains(content, want) {
					t.Fatalf("system preamble missing %q in %q", want, content)
				}
			}
			for _, forbidden := range tc.wantAbsent {
				if strings.Contains(content, forbidden) {
					t.Fatalf("system preamble unexpectedly contains %q in %q", forbidden, content)
				}
			}
			if got := strings.Count(content, testIdentityMarker); got != tc.wantIdentityCnt {
				t.Fatalf("identity count = %d, want %d in %q", got, tc.wantIdentityCnt, content)
			}

			for i := 1; i < len(tc.wantOrder); i++ {
				prev := strings.Index(content, tc.wantOrder[i-1])
				next := strings.Index(content, tc.wantOrder[i])
				if prev == -1 || next == -1 {
					t.Fatalf("missing ordering marker in %q", content)
				}
				if prev >= next {
					t.Fatalf("marker %q (index %d) should appear before %q (index %d) in %q", tc.wantOrder[i-1], prev, tc.wantOrder[i], next, content)
				}
			}

			if tc.wantSuffixLast && tc.suffix != "" && !strings.HasSuffix(strings.TrimSpace(content), tc.suffix) {
				t.Fatalf("suffix %q not at end of preamble in %q", tc.suffix, content)
			}

			coreStart := strings.Index(content, testCoreRulesMarker)
			workflowStart := strings.Index(content, testWorkflowMarker)
			if coreStart != -1 && workflowStart != -1 && coreStart < workflowStart {
				coreSection := content[coreStart:workflowStart]
				for _, forbidden := range tc.wantCoreAbsent {
					if strings.Contains(coreSection, forbidden) {
						t.Fatalf("core rules section unexpectedly contains %q in %q", forbidden, coreSection)
					}
				}
			}
		})
	}
}

func TestSystemPreambleDelegationInstructions(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", true, false, "").Content
	for _, want := range []string{
		testDelegationMarker,
		"You are the orchestrator.",
		"You are not the default implementation worker.",
		"Your context is the scarce resource.",
		"Every file you read locally stays in it for the rest of the conversation",
		"Sub-agent context is ephemeral — it vanishes once the agent reports back.",
		"You own the parts that cannot be delegated",
		"## Your specialists",
		"| Agent | Lane | Do not use for |",
		"| `explore` | Navigate the codebase: find files, symbols, patterns, usages, or call sites | questions answerable from the web or docs — that is `research` |",
		"| `research` | Gather information: search the web, read docs, synthesize across sources | anything answerable from the repo alone — that is `explore` |",
		"| `code` | Implement a scoped change: one deliverable, exact files named, design pre-digested | design decisions, or work whose files you have not identified |",
		"| `evaluate` | Analyze a scoped sub-problem: weigh options, produce a recommendation. Not for task planning | task planning, or questions with one obvious answer |",
		"| `sanity_check` | Run checks: tests, lint, build. Report pass/fail. No code changes | anything that changes files |",
		"| `review` | Examine code changes: bugs, regressions, missing tests, plan adherence. No fixes | broad 'review the whole PR' scope, or applying fixes |",
		"Before acting on any task, classify it into one of:",
		"Investigation → always `explore`",
		"Research → always `research`",
		"Implementation → `code`, unless the routing threshold below applies",
		"Verification → always `sanity_check`",
		"Review → always `review`",
		"`evaluate` is a reasoning aid, not a task category.",
		"## Routing threshold",
		"Delegate by default.",
		"one `read` of a file you are about to edit;",
		"one `grep` for a known pattern, one `ls`, one `git diff`, one `gofmt`, or one targeted test;",
		"applied with `mutate`",
		"If you cannot state in one line why delegation would cost more than doing it yourself, delegate.",
		"## Briefing a sub-agent",
		"When delegating to `code`: name the exact files and function signatures to change.",
		"Delegating is not free: the sub-agent starts cold and re-reads what you already hold",
		"When delegating to `review`: scope to specific files or a diff range.",
		"Sub-agents receive only the task you provide.",
		"Sub-agents cannot delegate further or ask the user questions.",
		"Every sub-agent task MUST use the template below.",
		"Objective: what the sub-agent must accomplish",
		"Context: file paths, symbols, or background",
		"Deliverable: the concrete output expected",
		"Constraints: boundaries",
		"Success criteria: how the sub-agent knows it is done",
		"Verification: commands/checks to run, if applicable",
		"Put the context you already hold into the brief",
		"rather than making the sub-agent rediscover it",
		"Do not paste broad conversation history.",
		"| Find DRY/refactoring opportunities across the codebase | `explore`: report files, repeated patterns, risks, and next steps. |",
		"| Understand how a feature works across multiple files | `explore`: trace the call chain and report. |",
		"| Read one file you are about to edit | Work locally. |",
		"| Edit a file whose contents are already in your context | Work locally with `mutate`. |",
		"Ask a sub-agent to find something across multiple files",
		"| Run broad verification while continuing local work | `sanity_check`: run checks and summarize exact failures. |",
		"| Evaluate two approaches to a design problem | `evaluate`: analyze tradeoffs and recommend. |",
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
		"## Delegation",
		"Work locally in exactly two cases:",
		"Never work locally when:",
		"Default to delegation; work locally only when the conditions below are clearly met.",
		"The result is needed in your current context",
		"You need the edit sites, not the whole file.",
		"State in one line why you are editing directly rather than calling `code`",
		"You need to read 2+ files to understand something",
		"You need to find where something is defined or used",
		"You are about to grep then read the results",
		"The task is separable from your current work",
		"### Delegation tips",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("delegation preamble still contains old guidance %q", forbidden)
		}
	}
}

func TestSystemPreambleCaveHumanMode(t *testing.T) {
	t.Parallel()

	content := SystemPreamble("", false, true, "").Content
	if !strings.Contains(content, testCaveHumanMarker) {
		t.Fatalf("cave-human preamble missing output voice block in %q", content)
	}
}

func TestSystemPreambleAdvisorGuidance(t *testing.T) {
	t.Parallel()

	content := systemPreambleWithAdvisor(SystemPreambleParams{Override: "", DelegationEnabled: false, AdvisorEnabled: true, Mode: workflowModeParent, CaveHuman: false, SystemSuffix: ""}).Content
	for _, want := range []string{
		"## Advisor",
		"If you need a stronger-model strategic check, call `advisor`.",
		"It gives steering only; it does not mutate code, run tools, or replace your judgment.",
		"surface the conflict explicitly rather than silently complying or silently discarding the advice.",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("advisor preamble missing %q in %q", want, content)
		}
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
			name:       "suffix appended after cave-human mode",
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

func TestSystemPreambleWorkflowApprovalByMode(t *testing.T) {
	t.Parallel()

	parent := systemPreambleWithAdvisor(SystemPreambleParams{Override: "", DelegationEnabled: true, AdvisorEnabled: false, Mode: workflowModeParent, CaveHuman: false, SystemSuffix: ""}).Content
	if !strings.Contains(parent, testWorkflowMarker) {
		t.Fatalf("parent preamble missing %q in %q", testWorkflowMarker, parent)
	}
	if !strings.Contains(parent, parentApprovalLine) {
		t.Fatalf("parent preamble missing approval line %q in %q", parentApprovalLine, parent)
	}

	child := systemPreambleWithAdvisor(SystemPreambleParams{Override: "", DelegationEnabled: true, AdvisorEnabled: false, Mode: workflowModeDelegatedChild, CaveHuman: false, SystemSuffix: ""}).Content
	if !strings.Contains(child, testWorkflowMarker) {
		t.Fatalf("child preamble missing %q in %q", testWorkflowMarker, child)
	}
	if !strings.Contains(child, childApprovalLine) {
		t.Fatalf("child preamble missing delegated approval line %q in %q", childApprovalLine, child)
	}
	if strings.Contains(child, parentApprovalLine) {
		t.Fatalf("child preamble unexpectedly contains parent approval line %q in %q", parentApprovalLine, child)
	}
}

func TestSystemPreambleExecutionModesInParent(t *testing.T) {
	t.Parallel()

	content := systemPreambleWithAdvisor(SystemPreambleParams{Override: "", DelegationEnabled: false, AdvisorEnabled: false, Mode: workflowModeParent, CaveHuman: false, SystemSuffix: ""}).Content
	for _, want := range []string{
		"## Execution modes",
		"Interactive sessions run in `plan` or `build` mode.",
		"The current mode",
		"arrives as a bracketed notice inside user messages.",
		"In `plan` mode:",
		"Project edits are restricted",
		"`.steiner/plans/`",
		"Write it to",
		"`.steiner/plans/<slug>/plan.md`",
		"In `build` mode, normal workspace editing rules apply.",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("parent preamble missing %q in %q", want, content)
		}
	}
}

func TestSystemPreambleExecutionModesAbsentInDelegatedChild(t *testing.T) {
	t.Parallel()

	content := systemPreambleWithAdvisor(SystemPreambleParams{Override: "", DelegationEnabled: false, AdvisorEnabled: false, Mode: workflowModeDelegatedChild, CaveHuman: false, SystemSuffix: ""}).Content
	if strings.Contains(content, "## Execution modes") {
		t.Fatalf("delegated child preamble should not contain execution modes section in %q", content)
	}
}

func TestSystemPreambleByteStable(t *testing.T) {
	t.Parallel()

	// Call the preamble builder twice with identical inputs and verify byte-identity.
	// This proves the preamble has no mode variance (since ExecutionMode is not a parameter)
	// and no per-turn randomness.
	first := systemPreambleWithAdvisor(SystemPreambleParams{Override: "", DelegationEnabled: true, AdvisorEnabled: false, Mode: workflowModeParent, CaveHuman: false, SystemSuffix: ""}).Content
	second := systemPreambleWithAdvisor(SystemPreambleParams{Override: "", DelegationEnabled: true, AdvisorEnabled: false, Mode: workflowModeParent, CaveHuman: false, SystemSuffix: ""}).Content

	if first != second {
		t.Fatalf("preamble not byte-identical across builds:\nfirst:\n%s\n\nsecond:\n%s", first, second)
	}
}

const testRoleProseMarker = "not the default implementation worker"

func TestSystemPreambleRoleProseGating(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name              string
		delegationEnabled bool
		mode              workflowMode
		wantRole          bool
	}{
		{
			name:              "delegation enabled, parent mode: role prose present",
			delegationEnabled: true,
			mode:              workflowModeParent,
			wantRole:          true,
		},
		{
			name:              "delegation disabled, parent mode: role prose absent",
			delegationEnabled: false,
			mode:              workflowModeParent,
			wantRole:          false,
		},
		{
			name:              "oneshot phase (delegated child mode, delegation enabled): role prose present",
			delegationEnabled: true,
			mode:              DelegatedChildWorkflowMode(),
			wantRole:          true,
		},
		{
			name:              "delegated child (delegation disabled): role prose absent",
			delegationEnabled: false,
			mode:              DelegatedChildWorkflowMode(),
			wantRole:          false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := systemPreambleWithAdvisor(SystemPreambleParams{
				DelegationEnabled: tc.delegationEnabled,
				Mode:              tc.mode,
			}).Content

			if !strings.Contains(content, testIdentityMarker) {
				t.Fatalf("preamble missing identity %q in %q", testIdentityMarker, content)
			}

			gotRole := strings.Contains(content, testRoleProseMarker)
			if gotRole != tc.wantRole {
				t.Fatalf("role prose present = %v, want %v in %q", gotRole, tc.wantRole, content)
			}
		})
	}
}

func TestSystemPreambleRoleProseViaOverride(t *testing.T) {
	t.Parallel()

	content := SystemPreambleWithAdvisor(SystemPreambleParams{
		Override:          "Custom override content",
		DelegationEnabled: true,
		Mode:              workflowModeParent,
	}).Content

	if !strings.Contains(content, testIdentityMarker) {
		t.Fatalf("override preamble missing identity %q in %q", testIdentityMarker, content)
	}
	if !strings.Contains(content, testRoleProseMarker) {
		t.Fatalf("override preamble missing role prose %q in %q", testRoleProseMarker, content)
	}
	if !strings.Contains(content, "Custom override content") {
		t.Fatalf("override preamble missing override content in %q", content)
	}
}

func TestSystemPreambleRoleProsePosition(t *testing.T) {
	t.Parallel()

	content := systemPreambleWithAdvisor(SystemPreambleParams{
		DelegationEnabled: true,
		AdvisorEnabled:    true,
		Mode:              workflowModeParent,
	}).Content

	roleIdx := strings.Index(content, testRoleProseMarker)
	advisorIdx := strings.Index(content, "## Advisor")
	coreIdx := strings.Index(content, testCoreRulesMarker)

	if roleIdx < 0 || advisorIdx < 0 || coreIdx < 0 {
		t.Fatalf("missing marker: roleIdx=%d advisorIdx=%d coreIdx=%d in %q", roleIdx, advisorIdx, coreIdx, content)
	}
	if roleIdx >= advisorIdx {
		t.Fatalf("role prose (index %d) should appear before advisor section (index %d) in %q", roleIdx, advisorIdx, content)
	}
	if roleIdx >= coreIdx {
		t.Fatalf("role prose (index %d) should appear before core rules (index %d) in %q", roleIdx, coreIdx, content)
	}
}
