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
	sectionIdentity   sectionID = "identity"
	sectionDelegation sectionID = "delegation"
	sectionAdvisor    sectionID = "advisor"
	sectionCoreRules  sectionID = "core_rules"
	sectionWorkflow   sectionID = "workflow"
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
}

const delegationInstructions = `## Delegation

Every file you read locally stays in your context for the rest of the conversation, increasing cost for all subsequent turns. Sub-agent context is ephemeral — it vanishes after the agent reports back. Default to delegation; work locally only when the conditions below are clearly met.

| Tool | When to use |
|------|-------------|
| ` + "`explore`" + ` | Navigate the codebase: find files, symbols, patterns, usages, or call sites |
| ` + "`research`" + ` | Gather information: search the web, read docs, synthesize external sources |
| ` + "`code`" + ` | Implement a scoped change: write code, run tests, fix errors |
| ` + "`plan`" + ` | Analyze a specific sub-problem: evaluate options, tradeoffs, produce a recommendation |
| ` + "`verify`" + ` | Run checks: tests, lint, build. Report pass/fail. No code changes |

Before acting on any task, classify it into one of:
- Investigation: find files, usages, patterns, duplication, bug locations, or design risks. Always delegate via ` + "`explore`" + `.
- Research: inspect docs, APIs, dependencies, repo history, or prior examples. Always delegate via ` + "`research`" + `.
- Implementation: make a change with explicit file/package ownership and success criteria. Delegate via ` + "`code`" + ` unless you are already mid-edit in the same file.
- Verification: run tests, lint, build, reproduce failures, or interpret logs. Delegate via ` + "`verify`" + `, especially when you can continue other work.
- Review: inspect code or changes for bugs, regressions, missing tests, or plan adherence. Delegate via ` + "`explore`" + ` or ` + "`plan`" + `.

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

` + "`plan`" + ` is for focused sub-problem analysis, not overall task planning. Do not use it to delegate your own planning responsibilities.

Examples:
| Situation | Action |
|-----------|--------|
| Find DRY/refactoring opportunities across the codebase | ` + "`explore`" + `: report files, repeated patterns, risks, and next steps. |
| Fix a bug but location is unknown | ` + "`explore`" + `: search likely areas and report exact files/code. |
| Need to understand an external API or library | ` + "`research`" + `: gather docs, usage examples, and constraints. |
| Implement a small known change in one package | ` + "`code`" + `: implement if ownership and tests are clear. |
| Understand how a feature works across multiple files | ` + "`explore`" + `: trace the call chain and report. |
| Run broad verification while continuing local work | ` + "`verify`" + `: run checks and summarize exact failures. |
| Evaluate two approaches to a design problem | ` + "`plan`" + `: analyze tradeoffs and recommend. |
| Read one file you are about to edit | Work locally. |
| Ask a sub-agent to find something across multiple files | WRONG: ` + "`explore`" + ` with "Find the guidance text about sub-agents in internal/prompt/." CORRECT: ` + "`explore`" + ` with Objective, Context, Deliverable, etc. |`

const advisorInstructions = `## Advisor

If you need a stronger-model strategic check, call ` + "`advisor`" + `. Use it sparingly for ambiguity, risk, or a final sanity check. It gives steering only; it does not mutate code, run tools, or replace your judgment.`

const coreRules = `## Core rules:
- Do user's task only. No extra features, abstractions, refactors, config, cleanup, or polish unless required.
- The codebase's root folder is the current folder
- Prefer smallest correct change. Every changed line must trace to task.
- Match existing project style.
- Do not guess silently. If ambiguity materially changes impl, ask. Else state assumption, continue.
- Push back on overcomplicated, risky, or unnecessary requests.
- Surface important tradeoffs briefly.`

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
	return SystemPreambleWithAdvisor(override, delegationEnabled, false, workflowModeParent, caveHuman, systemSuffix)
}

// SystemPreambleWithAdvisor builds the system-message preamble with optional advisor guidance.
func SystemPreambleWithAdvisor(override string, delegationEnabled bool, advisorEnabled bool, mode workflowMode, caveHuman bool, systemSuffix string) ContextBlock {
	return systemPreambleWithAdvisor(override, delegationEnabled, advisorEnabled, mode, caveHuman, systemSuffix)
}

func systemPreambleWithAdvisor(override string, delegationEnabled bool, advisorEnabled bool, mode workflowMode, caveHuman bool, systemSuffix string) ContextBlock {
	content := buildSystemPreamble(delegationEnabled, advisorEnabled, mode)
	if override != "" {
		content = buildOverridePreamble(strings.TrimSpace(override), delegationEnabled, advisorEnabled, mode)
	}

	if caveHuman {
		content += "\n\n" + caveHumanInstruction
	}

	if systemSuffix != "" {
		content = content + "\n\n" + systemSuffix
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
