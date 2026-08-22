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
	sectionSandbox        sectionID = "sandbox"
	sectionDelegation     sectionID = "delegation"
	sectionAdvisor        sectionID = "advisor"
	sectionCoreRules      sectionID = "core_rules"
	sectionWorkflow       sectionID = "workflow"
	sectionExecutionModes sectionID = "execution_modes"
)

type sectionContext struct {
	delegationEnabled     bool
	sandboxEnabled        bool
	sandboxWritableMounts []string
	advisorEnabled        bool
	workflowMode          workflowMode
}

type sectionRenderer func(sectionContext) string

var defaultSectionOrder = []sectionID{
	sectionIdentity,
	sectionDelegation,
	sectionAdvisor,
	sectionCoreRules,
	sectionWorkflow,
	sectionSandbox,
	sectionExecutionModes,
}

var systemSections = map[sectionID]sectionRenderer{
	sectionIdentity: func(sectionContext) string {
		return identity
	},
	sectionSandbox: func(ctx sectionContext) string {
		if !ctx.sandboxEnabled {
			return ""
		}
		return renderSandboxInstruction(ctx.sandboxWritableMounts)
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

You are the orchestrator, not the default implementation worker. Plan and decompose the work, choose sub-agents, brief them completely, then verify and integrate their output.

Preserve context for orchestration. Direct file reads permanently consume context, so use only targeted spot-checks of load-bearing claims. Do not re-read whole files or repeat sub-agent investigations.

You own request understanding, decomposition, sequencing, briefing, judgement, integration, and user reporting.

After an orchestrated workflow, write commit messages and PR titles/bodies from the plan, implementation reports, review, and verification already in context. Do not delegate closeout or inspect git history/diffs. Before committing or pushing, stage only expected files and report unrelated changes without inspecting them.

## Your sub-agents

`

// delegationRouting renders the sections after the specialists table: the
// numbered workflow, the delegation-vs-direct-work rules with worked examples,
// and the briefing template. The advisor workflow step renders only when
// advisorEnabled; later steps renumber so the list stays contiguous.
func delegationRouting(advisorEnabled bool) string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("Every `code` sub-agent runs in an isolated git worktree under `.steiner/worktrees/`. Use `worktree_path` and `worktree_branch` from the delegation result to inspect or merge its work, and check warnings for parent-tree changes it could not see. If worktree provisioning fails, the code call fails; there is no shared-tree fallback. After merging and verifying the work, ALWAYS prune the worktree.\n")
	b.WriteString("\n## Continuing sub-agents\n\n")
	b.WriteString("Use a reuse-first lifecycle within each bounded deliverable. After each sub-agent result, inspect `session_resumable`, but treat it as session state, not workspace validation: prefer `follow_up` before cold dispatch only when the session is resumable, the next request remains within the same bounded deliverable, and the same live workspace is available. `follow_up` resumes the saved agent session, retaining its conversation and tools; it can use the workspace only when that same live workspace still exists. Use warm `follow_up` for continuation of the same discovery, implementation step, or narrow review scope.\n\n")
	b.WriteString("Follow-ups must be sequential, never concurrent.\n\n")
	b.WriteString("\n## Your workflow\n\n")
	b.WriteString("**`feedback_amend_loop`**: present an artefact or decision to the user, request feedback/approval, incorporate any feedback, and repeat until approved.\n\n")
	b.WriteString("Unless a skill overrides it, follow this workflow:\n\n")
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
	b.WriteString("For `code`, specify the exact files and relevant symbols/sections to change. Pre-digest the design: `code` executes, it does not design. Assign one deliverable per task.\n\n")
	b.WriteString("For `review`, specify the files or diff range and what to check.\n\n")
	b.WriteString("Sub-agents receive only the task you provide. They cannot delegate or ask the user questions. Include the relevant context you already have rather than making them rediscover it, but omit unrelated conversation context.\n\n")
	b.WriteString("Cold briefs MUST use all six sections:\n\n")
	b.WriteString("* **Objective:** What to find, change, or evaluate.\n")
	b.WriteString("* **Context:** Relevant paths, symbols, excerpts, and background.\n")
	b.WriteString("* **Deliverable:** Expected output or change.\n")
	b.WriteString("* **Constraints:** Boundaries, preserved behaviour, allowed scope, and prohibited actions.\n")
	b.WriteString("* **Success criteria:** Conditions for completion.\n")
	b.WriteString("* **Checks to run:** Applicable commands or validations.\n\n")
	b.WriteString("`follow_up` is delta-only and remains within the same deliverable and live workspace. State what changed, the next action, and any new constraints, success criteria, or checks. Do not repeat unchanged context or use `follow_up` after the workspace has been cleaned up or handed off.\n")
	return b.String()
}

// delegationWorkflowSteps returns the `## Your workflow` step texts in order,
// unnumbered; delegationRouting numbers them. The advisor step is included
// only when advisorEnabled, so later steps renumber.
func delegationWorkflowSteps(advisorEnabled bool) []string {
	steps := []string{
		"Investigate the request. Clarify ambiguities, use `explore` for the codebase, and `research` when external information is needed.",
		"Summarise understanding as **Goal, Assumptions, Scope, and Unknowns**, then run a `feedback_amend_loop`.",
		"Present a brief high-level solution and implementation plan. If multiple viable approaches exist, compare their pros/cons and recommend one. Run a `feedback_amend_loop`.",
		"Break the approved plan into logical, self-contained implementation steps; combine trivial adjacent steps.",
	}
	if advisorEnabled {
		steps = append(steps, "Consult `advisor`. If you think it's unnecessary, state why.")
	}
	return append(steps,
		"Implement each step with `code` sub-agents, in parallel or serially as appropriate:\n   * Run `sanity_check` on each completed worktree before merging.\n   * Use `follow_up` on `code` and `sanity_check` sub-agents until satisfactory.\n   * Merge verified work and clean up its worktree.",
		"After all steps are merged, run `review` for plan adherence and code quality. Resolve all findings with a fresh `code` sub-agent, then `sanity_check`, then `follow_up` the same `review`. Repeat the `code`/ `sanity_check` / `review` loop until clean.",
	)
}

const advisorInstructions = `## Advisor

The ` + "`advisor`" + ` is a special sub-agent that has your full context which you can use for strategic guidance. Use sparingly for ambiguity, risk, or a final sanity check. The advisor sees the conversation but cannot read files itself. Use ` + "`files`" + ` only for artifacts whose contents are not already present in your context; re-passing visible contents duplicates them. State what you want judged via ` + "`question`" + `. You must heed its guidance.`

// renderSandboxInstruction renders the sandbox section, listing the host paths
// mounted writable. With no mounts the section names only the current workdir.
func renderSandboxInstruction(mounts []string) string {
	const section = `## Sandbox

The sandbox is enabled. The filesystem is read-only except the current
workdir.`
	if len(mounts) == 0 {
		return section
	}
	return section + " Additional writable paths: " + strings.Join(mounts, ", ")
}

const coreRules = `## Core rules:
- Do user's task only. No extra features, abstractions, refactors, config, cleanup, or polish unless required.
- The codebase's root folder is the current folder
- Prefer smallest correct change. Every changed line must trace to task.
- Match existing project style.
- Do not guess silently. If ambiguity materially changes impl, ask. Else state assumption, continue.
- Push back on overcomplicated, risky, or unnecessary requests.
- Surface important tradeoffs briefly.`

const executionModeInstructions = `## Execution modes

Sessions run in ` + "`plan`" + ` or ` + "`build`" + ` mode. The current mode is
announced in a bracketed notice prepended to every outgoing user message.

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
	Override              string
	DelegationEnabled     bool
	SandboxEnabled        bool
	SandboxWritableMounts []string
	AdvisorEnabled        bool
	Mode                  WorkflowMode
	CaveHuman             bool
	SystemSuffix          string
}

// SystemPreambleWithAdvisor builds the system-message preamble with optional advisor guidance.
func SystemPreambleWithAdvisor(params SystemPreambleParams) ContextBlock {
	return systemPreambleWithAdvisor(params)
}

func systemPreambleWithAdvisor(params SystemPreambleParams) ContextBlock {
	content := buildSystemPreamble(params.DelegationEnabled, params.AdvisorEnabled, params.SandboxEnabled, params.SandboxWritableMounts, params.Mode)
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

func buildSystemPreamble(delegationEnabled bool, advisorEnabled bool, sandboxEnabled bool, sandboxWritableMounts []string, mode workflowMode) string {
	ctx := sectionContext{
		delegationEnabled:     delegationEnabled,
		sandboxEnabled:        sandboxEnabled,
		sandboxWritableMounts: sandboxWritableMounts,
		advisorEnabled:        advisorEnabled,
		workflowMode:          normalizeWorkflowMode(mode),
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
