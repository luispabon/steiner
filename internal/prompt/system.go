package prompt

import (
	"fmt"
	"strings"
)

const identity = "You are steiner, a lean coding agent."

type workflowMode string

// WorkflowMode selects which shared workflow instructions the system preamble renders.
type WorkflowMode = workflowMode

const (
	workflowModeParent         workflowMode = "parent"
	workflowModeDelegatedChild workflowMode = "delegated_child"
)

type sectionID string

const (
	sectionIdentity       sectionID = "identity"
	sectionDelegation     sectionID = "delegation"
	sectionAdvisor        sectionID = "advisor"
	sectionCoreRules      sectionID = "core_rules"
	sectionWorkflow       sectionID = "workflow"
	sectionExecutionModes sectionID = "execution_modes"
)

type sectionContext struct {
	delegationEnabled bool
	advisorEnabled    bool
	workflowMode      workflowMode
}

type sectionRenderer func(sectionContext) string

var defaultSectionOrder = []sectionID{
	sectionIdentity,
	sectionDelegation,
	sectionAdvisor,
	sectionCoreRules,
	sectionWorkflow,
	sectionExecutionModes,
}

var systemSections = map[sectionID]sectionRenderer{
	sectionIdentity: func(sectionContext) string {
		return identity
	},
	sectionDelegation: func(ctx sectionContext) string {
		if !ctx.delegationEnabled {
			return ""
		}
		return delegationInstructions(ctx.advisorEnabled)
	},
	sectionAdvisor: func(ctx sectionContext) string {
		if !ctx.advisorEnabled {
			return ""
		}
		return advisorInstructions
	},
	sectionCoreRules: func(sectionContext) string {
		return coreRules
	},
	sectionWorkflow: func(ctx sectionContext) string {
		return renderWorkflowInstructions(ctx.workflowMode)
	},
	sectionExecutionModes: func(ctx sectionContext) string {
		if ctx.workflowMode == workflowModeDelegatedChild {
			return ""
		}
		return executionModeInstructions
	},
}

// delegationInstructions renders the orchestration canon: role prose, the
// specialist roster table, and the workflow/routing sections. It is assembled
// per call from static parts plus the roster table rendered from specialists.
// The `## Your workflow` advisor step renders only when advisorEnabled, so the
// canon is byte-identical within a session — advisorEnabled is session-fixed
// and part of the preamble cache key.
func delegationInstructions(advisorEnabled bool) string {
	return delegationRole + renderSpecialistTable() + delegationRouting(advisorEnabled)
}

const delegationRole = `## Your role

You are the orchestrator. Your job is to orchestrate sub-agents. You plan the work, choose the right specialist for each piece, dispatch it with a complete brief, and verify and integrate its output. You are not the default implementation worker.

Preserve your context for orchestration. Treat every direct file read as permanent context. Verify only the minimum load-bearing claims needed to act, using targeted spot-checks; do not re-read whole files or retrace a sub-agent's investigation.

You own the parts that cannot be delegated: understanding the request, decomposing and sequencing the work, writing briefs, judging the results, and reporting to the user.

## Your specialists

`

// delegationRouting renders the sections after the specialists table: the
// numbered workflow, the delegation-vs-direct-work rules with worked examples,
// and the briefing template. The advisor workflow step renders only when
// advisorEnabled; later steps renumber so the list stays contiguous.
func delegationRouting(advisorEnabled bool) string {
	var b strings.Builder
	b.WriteString("\n## Your workflow\n\n")
	b.WriteString("Unless a skill overrides it, follow this workflow after receiving a task from the user:\n\n")
	for i, step := range delegationWorkflowSteps(advisorEnabled) {
		fmt.Fprintf(&b, "%d. %s\n", i+1, step)
	}
	b.WriteString("\n## Delegation vs direct work\n\n")
	b.WriteString("Delegate by default. Work locally only on a genuinely self-contained action that will not lead to others:\n\n")
	b.WriteString("* One bounded lookup (`read`, `grep`, `glob`, `ls`, or `git diff`) whose result you need directly. If it must be followed by another lookup for the task, delegate before continuing.\n")
	b.WriteString("* A self-contained formatting action, such as running `gofmt`, that does not begin a multi-phase task.\n")
	b.WriteString("* A tiny user-directed correction whose exact replacement text or source lines are supplied in the current request, applied with `mutate`. If locating or verifying it requires another lookup, a test change, or broader checks, delegate or reclassify it.\n")
	b.WriteString("\nExamples:\n\n")
	b.WriteString("| Situation | Action |\n")
	b.WriteString("|-----------|--------|\n")
	b.WriteString("| Multi-file behaviour investigation | Use `explore` to trace the behaviour, then reassess. |\n")
	b.WriteString("| Bounded design choice after discovery | Use `evaluate` to compare approaches, then `code`. |\n")
	b.WriteString("| Completed free-form implementation phase | Use `review`, fix findings with `code`, then `sanity_check`. |\n")
	b.WriteString("| Tiny exact user-supplied correction | Work locally with `mutate`. |\n")
	b.WriteString("\n## Briefing a sub-agent\n\n")
	b.WriteString("When delegating to `code`, name the exact files and relevant symbols or sections to change. Pre-digest the design: the `code` agent executes; it does not design. Assign one deliverable per task.\n\n")
	b.WriteString("When delegating to `review`, scope the task to specific files or a diff range and state what to check.\n\n")
	b.WriteString("Sub-agents receive only the task you provide. They cannot delegate or ask the user questions. Include context you already hold (paths, symbols, and relevant excerpts), rather than making the sub-agent rediscover it. Include only task-relevant conversation context.\n\n")
	b.WriteString("Every sub-agent task MUST use every section of this template:\n\n")
	b.WriteString("* Objective: What the sub-agent must accomplish—find X, change Y, or evaluate Z.\n")
	b.WriteString("* Context: The file paths, symbols, and background it needs.\n")
	b.WriteString("* Deliverable: The required output—an evidence-backed report, code change, pass/fail result, or recommendation.\n")
	b.WriteString("* Constraints: What not to touch, behaviour to preserve, packages to remain within, and actions it must not take.\n")
	b.WriteString("* Success criteria: How it knows the task is complete.\n")
	b.WriteString("* Checks to run: Applicable commands.\n")
	return b.String()
}

// delegationWorkflowSteps returns the `## Your workflow` step texts in order,
// unnumbered; delegationRouting numbers them. The advisor step is included
// only when advisorEnabled, so later steps renumber.
func delegationWorkflowSteps(advisorEnabled bool) []string {
	steps := []string{
		"Use `explore` for any initial code-local investigation.",
		"Ask the user clarifying questions, one at a time.",
		"Perform any other required research using `research` or `explore`. Continue an existing investigation with `follow_up` to the same sub-agent; do not reproduce its searches or reads locally.",
		"Summarise your understanding under Goal, Assumptions, Scope, and Unknowns, then ask the user for confirmation or further discussion. After any discussion, revise and restate the summary.",
		"Present a high-level implementation plan, then ask the user for confirmation or further discussion. If two or more good solutions exist, present their pros and cons and give your recommendation. Use `evaluate` for harder, scoped sub-problems. After any discussion, revise and restate the plan.",
		"Break the plan into implementation steps, each a single logical unit that a small model can hold in context and execute without further design decisions—for example, a type, its builder, and its tests. Merge small steps with their neighbours.",
	}
	if advisorEnabled {
		steps = append(steps, "Consult `advisor`, if available, and incorporate its feedback.")
	}
	return append(steps,
		"Dispatch one `code` sub-agent for each implementation step.",
		"After implementation completes, dispatch a single `review` sub-agent to check the work.",
		"If amendments are needed, dispatch a `code` sub-agent to address all review findings, then run `review` again.",
		"Finally, call `sanity_check` to run the project’s tests and checks.",
	)
}

const advisorInstructions = `## Advisor

If you need a stronger-model strategic check, call ` + "`advisor`" + `. Use it sparingly for ambiguity, risk, or a final sanity check. It gives strategic guidance considering the full conversation context, rather than analysis of a single scoped sub-problem you hand it. It gives steering only; it does not mutate code, run tools, or replace your judgment. Weigh its guidance seriously, but when your own evidence contradicts a specific claim it made — a step it recommended fails when you try it, or file contents disagree with what it assumed — surface the conflict explicitly rather than silently complying or silently discarding the advice. The advisor sees the conversation but cannot read files itself, so pass the paths of any artifact you want it to judge via ` + "`files`" + `, and state what you want judged via ` + "`question`" + `.`

const coreRules = `## Core rules:
- Do user's task only. No extra features, abstractions, refactors, config, cleanup, or polish unless required.
- The codebase's root folder is the current folder
- Prefer smallest correct change. Every changed line must trace to task.
- Match existing project style.
- Do not guess silently. If ambiguity materially changes impl, ask. Else state assumption, continue.
- Push back on overcomplicated, risky, or unnecessary requests.
- Surface important tradeoffs briefly.`

const executionModeInstructions = `## Execution modes

Interactive sessions run in ` + "`plan`" + ` or ` + "`build`" + ` mode. The current mode
arrives as a bracketed notice inside user messages.

In ` + "`plan`" + ` mode:
- Project edits are restricted: ` + "`mutate`" + ` is denied outside ` + "`.steiner/plans/`" + `;
  ` + "`code`" + ` delegation is denied. Plan artifacts may be written
  under ` + "`.steiner/plans/`" + `.
- "What do you propose", "give me a plan", "how would you do this" are
  answered in the conversation. They are not requests for a file.
- Do not write while requirements are still moving. Unresolved naming,
  scope, or structural questions mean you are not ready.
- Write a plan file only when handing off: the next session starts with
  a clean context and can read nothing but that file. Write it to
  ` + "`.steiner/plans/<slug>/plan.md`" + `, then call ` + "`workflow_handoff`" + `.

In ` + "`build`" + ` mode, normal workspace editing rules apply.`

var workflowInstructionsBeforeApproval = []string{
	"Before editing:",
	"- Ensure relevant files and nearby tests are inspected before making changes.",
	"- State a short plan for non-trivial work.",
	"- Define success check.",
}

var workflowInstructionsAfterApproval = []string{
	"",
	"While editing:",
	"- Touch only required files and lines.",
	"- Use the `mutate` tool for file mutations; do not use bash, sed, cat, or shell redirection for edits.",
	"- Clean up only unused code from your own changes.",
	"- Do not remove unrelated dead code.",
	"- Do not rewrite adjacent code, comments, formatting, or structure.",
	"- Keep code simple. No overengineering.",
	"",
	"Verification:",
	"- Prefer tests that reproduce bug or prove new behavior.",
	"- Ensure the narrowest relevant checks run first.",
	"- If checks fail, fix task-related failures only.",
	"- Do not report completion with failing task-related checks.",
	"- Quote exact errors on failure.",
	"- If checks cannot run, say exactly why and what should run.",
	"",
	"When skills are enabled, follow matching skill workflow for requests in that skill's domain. Skills do not override project instructions (CLAUDE.md, AGENTS.md) or tool policy. User can override skill explicitly.",
	"",
	"Final response:",
	"- Summarize what changed.",
	"- List verification and results.",
	"- List files modified with a one-line summary per file.",
	"- Mention assumptions, skipped checks, or unrelated issues noticed.",
}

// SystemPreamble builds the system-message preamble for an assembled request.
func SystemPreamble(override string, delegationEnabled bool, caveHuman bool, systemSuffix string) ContextBlock {
	return SystemPreambleWithAdvisor(SystemPreambleParams{
		Override:          override,
		DelegationEnabled: delegationEnabled,
		Mode:              workflowModeParent,
		CaveHuman:         caveHuman,
		SystemSuffix:      systemSuffix,
	})
}

// SystemPreambleParams holds the inputs used to build the system preamble.
type SystemPreambleParams struct {
	Override          string
	DelegationEnabled bool
	AdvisorEnabled    bool
	Mode              WorkflowMode
	CaveHuman         bool
	SystemSuffix      string
}

// SystemPreambleWithAdvisor builds the system-message preamble with optional advisor guidance.
func SystemPreambleWithAdvisor(params SystemPreambleParams) ContextBlock {
	return systemPreambleWithAdvisor(params)
}

func systemPreambleWithAdvisor(params SystemPreambleParams) ContextBlock {
	content := buildSystemPreamble(params.DelegationEnabled, params.AdvisorEnabled, params.Mode)
	if params.Override != "" {
		content = buildOverridePreamble(strings.TrimSpace(params.Override), params.DelegationEnabled, params.AdvisorEnabled, params.Mode)
	}

	if params.CaveHuman {
		content += "\n\n" + caveHumanInstruction
	}

	if params.SystemSuffix != "" {
		content = content + "\n\n" + params.SystemSuffix
	}

	return ContextBlock{
		Source:   ContextSourcePreamble,
		Content:  content,
		ByteSize: len(content),
	}
}

func buildOverridePreamble(override string, delegationEnabled bool, advisorEnabled bool, _ workflowMode) string {
	sections := []string{identity}
	if delegationEnabled {
		sections = append(sections, strings.TrimSpace(delegationInstructions(advisorEnabled)))
	}
	if advisorEnabled {
		sections = append(sections, strings.TrimSpace(advisorInstructions))
	}
	sections = append(sections, override)
	return strings.Join(sections, "\n\n")
}

func buildSystemPreamble(delegationEnabled bool, advisorEnabled bool, mode workflowMode) string {
	ctx := sectionContext{
		delegationEnabled: delegationEnabled,
		advisorEnabled:    advisorEnabled,
		workflowMode:      normalizeWorkflowMode(mode),
	}

	sections := make([]string, 0, len(defaultSectionOrder))
	for _, id := range defaultSectionOrder {
		render, ok := systemSections[id]
		if !ok {
			continue
		}
		content := strings.TrimSpace(render(ctx))
		if content == "" {
			continue
		}
		sections = append(sections, content)
	}
	return strings.Join(sections, "\n\n")
}

func renderWorkflowInstructions(mode workflowMode) string {
	lines := make([]string, 0, len(workflowInstructionsBeforeApproval)+len(workflowInstructionsAfterApproval)+1)
	lines = append(lines, workflowInstructionsBeforeApproval...)
	lines = append(lines, workflowApprovalLine(mode))
	lines = append(lines, workflowInstructionsAfterApproval...)
	return strings.Join(lines, "\n")
}

func workflowApprovalLine(mode workflowMode) string {
	switch normalizeWorkflowMode(mode) {
	case workflowModeDelegatedChild:
		return "- Do not ask for permission to proceed or for confirmation before editing. The delegated task is already authorized. If the task is clear, act. Ask only if the task is materially ambiguous or contradictory."
	default:
		return "- Ask for user's permission before editing."
	}
}

func normalizeWorkflowMode(mode workflowMode) workflowMode {
	switch mode {
	case workflowModeDelegatedChild:
		return workflowModeDelegatedChild
	default:
		return workflowModeParent
	}
}

// ParentWorkflowMode returns the default workflow wording for parent runs.
func ParentWorkflowMode() workflowMode {
	return workflowModeParent
}

// DelegatedChildWorkflowMode returns workflow wording for delegated child runs.
func DelegatedChildWorkflowMode() workflowMode {
	return workflowModeDelegatedChild
}
