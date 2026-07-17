package prompt

import "strings"

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
		return delegationInstructions
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

const delegationInstructions = `## Delegation

Every file you read locally stays in your context for the rest of the conversation, increasing cost for all subsequent turns. Sub-agent context is ephemeral — it vanishes after the agent reports back. Default to delegation; work locally only when the conditions below are clearly met.

| Tool | When to use |
|------|-------------|
| ` + "`explore`" + ` | Navigate the codebase: find files, symbols, patterns, usages, or call sites |
| ` + "`research`" + ` | Gather information: search the web, read docs, synthesize across sources |
| ` + "`code`" + ` | Implement a scoped change: one deliverable, exact files named, design pre-digested |
| ` + "`evaluate`" + ` | Analyze a scoped sub-problem: weigh options, produce a recommendation. Not for task planning |
| ` + "`sanity_check`" + ` | Run checks: tests, lint, build. Report pass/fail. No code changes |
| ` + "`review`" + ` | Examine code changes: bugs, regressions, missing tests, plan adherence. No fixes |

Before acting on any task, classify it into one of:
- Investigation → always ` + "`explore`" + `
- Research → always ` + "`research`" + `
- Implementation → ` + "`code`" + ` unless you are already mid-edit in the same file
- Verification → always ` + "`sanity_check`" + `
- Review → always ` + "`review`" + `

` + "`evaluate`" + ` is a reasoning aid, not a task category. Use it when you face a design question with multiple viable approaches and need structured analysis before choosing. For strategic guidance considering the full conversation context, use ` + "`advisor`" + ` instead.

### Delegation tips

When delegating to ` + "`code`" + `: name the exact files and function signatures to change. Pre-digest the design — the code agent executes, it does not design. One deliverable per task.

When delegating to ` + "`review`" + `: scope to specific files or a diff range. State what to check for. Do not delegate broad 'review the whole PR' tasks — break them into file-group reviews.

Work locally only when ALL of:
- A single tool call completes the task: one ` + "`read`" + ` of a file you will immediately edit, one ` + "`grep`" + ` for a known pattern, ` + "`ls`" + ` of one path, ` + "`git diff`" + `, ` + "`gofmt`" + `, or one targeted test.
- The result is needed in your current context (you will edit the file next, or the user asked to see it).

Never work locally when:
- You need to read 2+ files to understand something — use ` + "`explore`" + `.
- You need to find where something is defined or used — use ` + "`explore`" + `.
- You are about to grep then read the results — use ` + "`explore`" + `.
- The task is separable from your current work — delegate it.

Sub-agents receive only the task you provide. Sub-agents cannot delegate further or ask the user questions. Every sub-agent task MUST use the template below. Never use a single unstructured paragraph or omit sections:

- Objective: what the sub-agent must accomplish — find X, change Y, evaluate Z.
- Context: file paths, symbols, or background the sub-agent needs.
- Deliverable: the concrete output expected — report with evidence, code change, pass/fail signal, or recommendation.
- Constraints: boundaries. What not to touch, behavior to preserve, packages to stay within.
- Success criteria: how the sub-agent knows it is done.
- Verification: commands/checks to run, if applicable

Examples:
| Situation | Action |
|-----------|--------|
| Find DRY/refactoring opportunities across the codebase | ` + "`explore`" + `: report files, repeated patterns, risks, and next steps. |
| Fix a bug but location is unknown | ` + "`explore`" + `: search likely areas and report exact files/code. |
| Need to understand an external API or library | ` + "`research`" + `: gather docs, usage examples, and constraints. |
| Implement a small known change in one package | ` + "`code`" + `: implement if ownership and tests are clear. |
| Understand how a feature works across multiple files | ` + "`explore`" + `: trace the call chain and report. |
| Run broad verification while continuing local work | ` + "`sanity_check`" + `: run checks and summarize exact failures. |
| Evaluate two approaches to a design problem | ` + "`evaluate`" + `: analyze tradeoffs and recommend. |
| Read one file you are about to edit | Work locally. |
| Ask a sub-agent to find something across multiple files | WRONG: ` + "`explore`" + ` with "Find the guidance text about sub-agents in internal/prompt/." CORRECT: ` + "`explore`" + ` with Objective, Context, Deliverable, etc. |`

const advisorInstructions = `## Advisor

If you need a stronger-model strategic check, call ` + "`advisor`" + `. Use it sparingly for ambiguity, risk, or a final sanity check. It gives steering only; it does not mutate code, run tools, or replace your judgment. Weigh its guidance seriously, but when your own evidence contradicts a specific claim it made — a step it recommended fails when you try it, or file contents disagree with what it assumed — surface the conflict explicitly rather than silently complying or silently discarding the advice.`

const coreRules = `## Core rules:
- Do user's task only. No extra features, abstractions, refactors, config, cleanup, or polish unless required.
- The codebase's root folder is the current folder
- Prefer smallest correct change. Every changed line must trace to task.
- Match existing project style.
- Do not guess silently. If ambiguity materially changes impl, ask. Else state assumption, continue.
- Push back on overcomplicated, risky, or unnecessary requests.
- Surface important tradeoffs briefly.`

const executionModeInstructions = `## Execution modes

Interactive sessions run in ` + "`plan`" + ` or ` + "`build`" + ` mode. The current mode and any change arrive as bracketed notices inside user messages.

In ` + "`plan`" + ` mode, the project is read-only and writes are permitted only under ` + "`.steiner/plans/`" + `. Produce plan artifacts there only when the user asks for a plan; otherwise just discuss.

When a plan is approved, call ` + "`workflow_handoff`" + ` to transition to build mode.

In ` + "`build`" + ` mode, normal editing rules apply.`

var workflowInstructionsBeforeApproval = []string{
	"Before editing:",
	"- Read the relevant files and nearby tests before making changes.",
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
	"- Run narrowest relevant checks first.",
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
		sections = append(sections, strings.TrimSpace(delegationInstructions))
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
