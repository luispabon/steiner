package prompt

// specialist is one row of the orchestrator-facing roster rendered into the
// "## Your sub-agents" table in delegationInstructions.
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
		doNotUseFor: "questions that are answerable from the web or documentation—that is `research`",
	},
	{
		name:        "research",
		lane:        "Search the web, read documentation, and synthesize external sources (read-only)",
		doNotUseFor: "anything answerable from the repository alone—that is `explore`",
	},
	{
		name:        "code",
		lane:        "Implement a scoped change: one deliverable, exact files named, design pre-digested",
		doNotUseFor: "design decisions or work whose files have not been identified",
	},
	{
		name:        "evaluate",
		lane:        "Analyse a scoped sub-problem, weigh options, and recommend an approach",
		doNotUseFor: "task planning or questions with one obvious answer",
	},
	{
		name:        "sanity_check",
		lane:        "Run tests, lint, and builds; report pass or fail; make no changes",
		doNotUseFor: "anything that changes files",
	},
	{
		name:        "review",
		lane:        "Examine code changes for bugs, regressions, missing tests, and plan adherence; make no fixes",
		doNotUseFor: "broad “review the whole PR” scopes or applying fixes",
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

// specialistView is the template-facing projection of a specialist row, in
// roster order. The roster table in templates/delegation.md.tmpl ranges over
// it; text/template can only reach exported fields.
type specialistView struct {
	Name        string
	Lane        string
	DoNotUseFor string
}

// specialistViews returns the roster as template data, in table order.
func specialistViews() []specialistView {
	views := make([]specialistView, 0, len(specialists))
	for _, s := range specialists {
		views = append(views, specialistView{Name: s.name, Lane: s.lane, DoNotUseFor: s.doNotUseFor})
	}
	return views
}
