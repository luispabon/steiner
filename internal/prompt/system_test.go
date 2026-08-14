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
			wantCoreAbsent:  []string{"Delegate by default. Work locally only on a genuinely self-contained action that will not lead to others:", "Sub-agents receive only the task you provide.", "Every sub-agent task MUST use every section of this template:", "## Your workflow"},
			wantIdentityCnt: 1,
		},
		{
			name:            "delegation disabled",
			delegation:      false,
			wantPresent:     []string{testIdentityMarker, testCoreRulesMarker, testWorkflowMarker},
			wantAbsent:      []string{testDelegationMarker},
			wantOrder:       []string{testIdentityMarker, testCoreRulesMarker, testWorkflowMarker},
			wantCoreAbsent:  []string{"Delegate by default. Work locally only on a genuinely self-contained action that will not lead to others:", "Sub-agents receive only the task you provide.", "Every sub-agent task MUST use every section of this template:"},
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
			wantCoreAbsent:  []string{"Delegate by default. Work locally only on a genuinely self-contained action that will not lead to others:", "Sub-agents receive only the task you provide.", "Every sub-agent task MUST use every section of this template:"},
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
		"Your job is to orchestrate sub-agents.",
		"You are not the default implementation worker.",
		"Preserve your context for orchestration.",
		"Treat every direct file read as permanent context.",
		"Verify only the minimum load-bearing claims needed to act, using targeted spot-checks; do not re-read whole files or retrace a sub-agent's investigation.",
		"You own the parts that cannot be delegated",
		"## Your specialists",
		"| Agent | Lane | Do not use for |",
		"| `explore` | Navigate the codebase: find files, symbols, patterns, usages, or call sites | questions that are answerable from the web or documentation—that is `research` |",
		"| `research` | Search the web, read documentation, and synthesize external sources (read-only) | anything answerable from the repository alone—that is `explore` |",
		"| `code` | Implement a scoped change: one deliverable, exact files named, design pre-digested | design decisions or work whose files have not been identified |",
		"| `evaluate` | Analyse a scoped sub-problem, weigh options, and recommend an approach | task planning or questions with one obvious answer |",
		"| `sanity_check` | Run tests, lint, and builds; report pass or fail; make no changes | anything that changes files |",
		"| `review` | Examine code changes for bugs, regressions, missing tests, and plan adherence; make no fixes | broad “review the whole PR” scopes or applying fixes |",
		"## Your workflow",
		"Unless a skill overrides it, follow this workflow after receiving a task from the user:",
		"1. Use `explore` for any initial code-local investigation.",
		"2. Ask the user clarifying questions, one at a time.",
		"3. Perform any other required research using `research` or `explore`. Continue an existing investigation with `follow_up` to the same sub-agent; do not reproduce its searches or reads locally.",
		"4. Summarise your understanding under Goal, Assumptions, Scope, and Unknowns, then ask the user for confirmation or further discussion. After any discussion, revise and restate the summary.",
		"5. Present a high-level implementation plan, then ask the user for confirmation or further discussion. If two or more good solutions exist, present their pros and cons and give your recommendation. Use `evaluate` for harder, scoped sub-problems. After any discussion, revise and restate the plan.",
		"6. Break the plan into implementation steps, each a single logical unit that a small model can hold in context and execute without further design decisions—for example, a type, its builder, and its tests. Merge small steps with their neighbours.",
		"7. Dispatch one `code` sub-agent for each implementation step.",
		"8. After implementation completes, dispatch a single `review` sub-agent to check the work.",
		"9. If amendments are needed, dispatch a `code` sub-agent to address all review findings, then run `review` again.",
		"10. Finally, call `sanity_check` to run the project’s tests and checks.",
		"## Delegation vs direct work",
		"Delegate by default. Work locally only on a genuinely self-contained action that will not lead to others:",
		"One bounded lookup (`read`, `grep`, `glob`, `ls`, or `git diff`) whose result you need directly. If it must be followed by another lookup for the task, delegate before continuing.",
		"A self-contained formatting action, such as running `gofmt`, that does not begin a multi-phase task.",
		"A tiny user-directed correction whose exact replacement text or source lines are supplied in the current request, applied with `mutate`.",
		"Examples:",
		"## Briefing a sub-agent",
		"When delegating to `code`, name the exact files and relevant symbols or sections to change.",
		"Pre-digest the design: the `code` agent executes; it does not design.",
		"When delegating to `review`, scope the task to specific files or a diff range and state what to check.",
		"Sub-agents receive only the task you provide.",
		"They cannot delegate or ask the user questions.",
		"Include context you already hold (paths, symbols, and relevant excerpts), rather than making the sub-agent rediscover it.",
		"Include only task-relevant conversation context.",
		"Every sub-agent task MUST use every section of this template:",
		"* Objective: What the sub-agent must accomplish—find X, change Y, or evaluate Z.",
		"* Context: The file paths, symbols, and background it needs.",
		"* Deliverable: The required output—an evidence-backed report, code change, pass/fail result, or recommendation.",
		"* Constraints: What not to touch, behaviour to preserve, packages to remain within, and actions it must not take.",
		"* Success criteria: How it knows the task is complete.",
		"* Checks to run: Applicable commands.",
		"| Multi-file behaviour investigation | Use `explore` to trace the behaviour, then reassess. |",
		"| Bounded design choice after discovery | Use `evaluate` to compare approaches, then `code`. |",
		"| Completed free-form implementation phase | Use `review`, fix findings with `code`, then `sanity_check`. |",
		"| Tiny exact user-supplied correction | Work locally with `mutate`. |",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("delegation preamble missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"Delegating is not free: the sub-agent starts cold",
		"Delegate to avoid acquiring context, not to avoid doing work.",
		"one targeted test",
		"you already hold the exact lines",
		"whose contents are already in your context",
		"Read one file you are about to edit",
		"single isolated, low-risk action",
		"If you cannot state in one line why delegation would cost more than doing it yourself, delegate.",
		"Two or more files, a search whose results you will then read",
		"unless the routing threshold below applies",
		"Prefer specialized delegate tools when the task fits.",
		"Before starting a task locally, classify it.",
		"One known tool call is enough",
		"Perform an initial code-local investigation using `explore`.",
		"and which won't lead to further lookups",
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
		"## Phase routing",
		"## Routing threshold",
		"Every task falls into one of the categories below.",
		"Classify before substantive tool use",
		"Investigation → always `explore`",
		"`evaluate` is a reasoning aid, not a task category.",
		"Use the dedicated tool (`read`, `grep`, `glob`, `ls`) instead of `bash`",
		"Your context is the scarce resource.",
		"Sub-agent context is ephemeral",
		"Delegating is how you avoid acquiring context",
		"Never use a single unstructured paragraph or omit sections",
		"Do not paste broad conversation history.",
		"Put the context you already hold into the brief",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("delegation preamble still contains old guidance %q", forbidden)
		}
	}
}

// TestSystemPreambleDelegationWorkflow pins the numbered `## Your workflow`
// list: steps 1-6 are fixed, the advisor step renders inline as step 7 only
// when advisorEnabled, and the dispatch/review/fix/sanity steps follow,
// renumbered (8-11 with advisor enabled, 7-10 without). The disabled form
// must not name the `advisor` tool at all.
func TestSystemPreambleDelegationWorkflow(t *testing.T) {
	t.Parallel()

	steps1to6 := []string{
		"1. Use `explore` for any initial code-local investigation.",
		"2. Ask the user clarifying questions, one at a time.",
		"3. Perform any other required research using `research` or `explore`. Continue an existing investigation with `follow_up` to the same sub-agent; do not reproduce its searches or reads locally.",
		"4. Summarise your understanding under Goal, Assumptions, Scope, and Unknowns, then ask the user for confirmation or further discussion. After any discussion, revise and restate the summary.",
		"5. Present a high-level implementation plan, then ask the user for confirmation or further discussion. If two or more good solutions exist, present their pros and cons and give your recommendation. Use `evaluate` for harder, scoped sub-problems. After any discussion, revise and restate the plan.",
		"6. Break the plan into implementation steps, each a single logical unit that a small model can hold in context and execute without further design decisions—for example, a type, its builder, and its tests. Merge small steps with their neighbours.",
	}

	cases := []struct {
		name       string
		advisor    bool
		wantSteps  []string
		wantAbsent []string
	}{
		{
			name:    "advisor disabled: ten steps, no advisor",
			advisor: false,
			wantSteps: append(steps1to6,
				"7. Dispatch one `code` sub-agent for each implementation step.",
				"8. After implementation completes, dispatch a single `review` sub-agent to check the work.",
				"9. If amendments are needed, dispatch a `code` sub-agent to address all review findings, then run `review` again.",
				"10. Finally, call `sanity_check` to run the project’s tests and checks.",
			),
			wantAbsent: []string{
				"Consult `advisor`, if available, and incorporate its feedback.",
				"11. ",
				"`advisor`",
			},
		},
		{
			name:    "advisor enabled: eleven steps, advisor inline",
			advisor: true,
			wantSteps: append(steps1to6,
				"7. Consult `advisor`, if available, and incorporate its feedback.",
				"8. Dispatch one `code` sub-agent for each implementation step.",
				"9. After implementation completes, dispatch a single `review` sub-agent to check the work.",
				"10. If amendments are needed, dispatch a `code` sub-agent to address all review findings, then run `review` again.",
				"11. Finally, call `sanity_check` to run the project’s tests and checks.",
			),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := systemPreambleWithAdvisor(SystemPreambleParams{
				DelegationEnabled: true,
				AdvisorEnabled:    tc.advisor,
				Mode:              workflowModeParent,
			}).Content

			last := -1
			for _, step := range tc.wantSteps {
				idx := strings.Index(content, step)
				if idx == -1 {
					t.Fatalf("workflow missing step %q in %q", step, content)
				}
				if idx <= last {
					t.Fatalf("workflow step %q appears before or overlapping a prior step in %q", step, content)
				}
				last = idx
			}
			for _, forbidden := range tc.wantAbsent {
				if strings.Contains(content, forbidden) {
					t.Fatalf("workflow unexpectedly contains %q in %q", forbidden, content)
				}
			}
		})
	}
}

// TestDelegationWorkflowConsistentAcrossOverridePath pins that the normal and
// override preamble paths both embed the same flag-aware delegation canon, so
// the advisor step gating and renumbering behave identically in both.
func TestDelegationWorkflowConsistentAcrossOverridePath(t *testing.T) {
	t.Parallel()

	for _, advisor := range []bool{false, true} {
		name := "advisor disabled"
		if advisor {
			name = "advisor enabled"
		}
		t.Run(name, func(t *testing.T) {
			canon := strings.TrimSpace(delegationInstructions(advisor))
			normal := systemPreambleWithAdvisor(SystemPreambleParams{DelegationEnabled: true, AdvisorEnabled: advisor, Mode: workflowModeParent}).Content
			override := systemPreambleWithAdvisor(SystemPreambleParams{Override: "Custom override content", DelegationEnabled: true, AdvisorEnabled: advisor, Mode: workflowModeParent}).Content

			if !strings.Contains(normal, canon) {
				t.Fatalf("normal preamble does not embed the rendered delegation canon in %q", normal)
			}
			if !strings.Contains(override, canon) {
				t.Fatalf("override preamble does not embed the rendered delegation canon in %q", override)
			}
		})
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
		"It gives strategic guidance considering the full conversation context, rather than analysis of a single scoped sub-problem you hand it.",
		"surface the conflict explicitly rather than silently complying or silently discarding the advice.",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("advisor preamble missing %q in %q", want, content)
		}
	}
}

// TestDelegationCanonDoesNotNameAdvisorWhenDisabled pins the fact that the
// delegation and advisor sections are gated independently: canon must not
// point the orchestrator at `advisor` in a session where the advisor tool is
// not registered and the advisor section is not rendered. It covers both the
// normal and override preamble paths, which share the same flag-aware canon
// renderer.
func TestDelegationCanonDoesNotNameAdvisorWhenDisabled(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		override string
	}{
		{name: "normal preamble"},
		{name: "override preamble", override: "Custom override content"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := systemPreambleWithAdvisor(SystemPreambleParams{Override: tc.override, DelegationEnabled: true, AdvisorEnabled: false, Mode: workflowModeParent, CaveHuman: false, SystemSuffix: ""}).Content

			if !strings.Contains(content, "## Your specialists") {
				t.Fatalf("delegation canon not rendered in %q", content)
			}
			if strings.Contains(content, "`advisor`") {
				t.Errorf("preamble names the `advisor` tool with advisor disabled; canon must not reference a tool that is not registered:\n%s", content)
			}
		})
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
	// The before-editing and verification bullets of the shared workflow block
	// are pinned here so a regression in renderWorkflowInstructions is caught.
	for _, want := range []string{
		"- Ensure relevant files and nearby tests are inspected before making changes.",
		"- Ensure the narrowest relevant checks run first.",
	} {
		if !strings.Contains(parent, want) {
			t.Fatalf("parent preamble missing %q in %q", want, parent)
		}
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
