package prompt

import "strings"

// specialist is one row of the orchestrator-facing roster rendered into the
// "## Your specialists" table in delegationInstructions.
type specialist struct {
	name        string
	lane        string
	doNotUseFor string
}

// specialists is the orchestrator-facing sub-agent roster. Order is
// significant: it is the row order of the rendered table.
//
// This slice is the single source of the roster. The table in
// delegationInstructions is rendered from it, so the two cannot diverge.
// internal/delegation asserts these names match its registered AgentType
// constants (see SpecialistNames); that direction is still test-detected
// rather than derived, because internal/prompt cannot import
// internal/delegation without creating an import cycle.
var specialists = []specialist{
	{
		name:        "explore",
		lane:        "Navigate the codebase: find files, symbols, patterns, usages, or call sites",
		doNotUseFor: "questions answerable from the web or docs — that is `research`",
	},
	{
		name:        "research",
		lane:        "Gather information: search the web, read docs, synthesize across sources (read-only)",
		doNotUseFor: "anything answerable from the repo alone — that is `explore`",
	},
	{
		name:        "code",
		lane:        "Implement a scoped change: one deliverable, exact files named, design pre-digested",
		doNotUseFor: "design decisions, or work whose files you have not identified",
	},
	{
		name:        "evaluate",
		lane:        "Analyze a scoped sub-problem: weigh options, produce a recommendation. Not for task planning",
		doNotUseFor: "task planning, or questions with one obvious answer",
	},
	{
		name:        "sanity_check",
		lane:        "Run checks: tests, lint, build. Report pass/fail. No code changes",
		doNotUseFor: "anything that changes files",
	},
	{
		name:        "review",
		lane:        "Examine code changes: bugs, regressions, missing tests, plan adherence. No fixes",
		doNotUseFor: "broad 'review the whole PR' scope, or applying fixes",
	},
}

// SpecialistNames returns the orchestrator-facing sub-agent names in the canon
// roster, in table order.
//
// Exported solely so internal/delegation can assert the roster matches its
// registered AgentType constants. internal/prompt cannot import
// internal/delegation to derive the roster directly, because
// internal/delegation/bootstrap.go imports internal/prompt.
func SpecialistNames() []string {
	names := make([]string, 0, len(specialists))
	for _, s := range specialists {
		names = append(names, s.name)
	}
	return names
}

// renderSpecialistTable renders the roster as a markdown table, header and
// separator included, with a trailing newline on every row.
func renderSpecialistTable() string {
	var b strings.Builder
	b.WriteString("| Agent | Lane | Do not use for |\n")
	b.WriteString("|-------|------|-----------------|\n")
	for _, s := range specialists {
		b.WriteString("| `" + s.name + "` | " + s.lane + " | " + s.doNotUseFor + " |\n")
	}
	return b.String()
}
