package delegation

// AgentType identifies a specialized delegate agent type.
type AgentType string

const (
	// AgentTypeExplore is the agent type for exploration tasks.
	AgentTypeExplore AgentType = "explore"
	// AgentTypeResearch is the agent type for research tasks.
	AgentTypeResearch AgentType = "research"
	// AgentTypeCode is the agent type for coding tasks.
	AgentTypeCode AgentType = "code"
	// AgentTypeEvaluate is the agent type for evaluation/analysis tasks.
	AgentTypeEvaluate AgentType = "evaluate"
	// AgentTypeSanityCheck is the agent type for verification tasks.
	AgentTypeSanityCheck AgentType = "sanity_check"
	// AgentTypeReview is the agent type for review tasks.
	AgentTypeReview AgentType = "review"
	// AgentTypeVision is the agent type for image analysis tasks.
	AgentTypeVision AgentType = "vision"
)

// AllAgentTypes returns all valid agent type values.
func AllAgentTypes() []AgentType {
	return []AgentType{AgentTypeExplore, AgentTypeResearch, AgentTypeCode, AgentTypeEvaluate, AgentTypeSanityCheck, AgentTypeReview, AgentTypeVision}
}

// AllSpecializedDelegateTools returns the canonical specialized delegate tool
// names used by delegation-aware UIs and other cross-package callers. It
// includes all agent types plus follow_up because follow_up is not an AgentType
// but behaves like the same specialized delegation surface in the TUI.
func AllSpecializedDelegateTools() []string {
	tools := make([]string, 0, len(AllAgentTypes())+1)
	for _, t := range AllAgentTypes() {
		tools = append(tools, string(t))
	}
	tools = append(tools, FollowUpToolName)
	return tools
}

// ValidAgentType reports whether s is a recognized agent type name.
func ValidAgentType(s string) bool {
	_, ok := validAgentTypeSet[s]
	return ok
}

var agentPrompts = map[AgentType]string{
	AgentTypeExplore: `You are an exploration agent navigating a codebase.

Your role: locate files, symbols, and patterns relevant to the given task.

How to work:
- Use read, glob, grep, and ls to find relevant files and code.
- Follow import chains and call sites to understand relationships.
- Stop exploring when you have enough to answer the question confidently.

How to respond — structure output in three sections:

Diagnosis: what is happening and why (or the answer to the question asked).

Evidence: for each relevant location, include file path, line number, and 3-5 verbatim lines of code preserving exact indentation. The caller may need these exact strings for edits.

Related: other files the caller will likely need if acting on this finding.

General rules:
- Do not include unrelated files.
- Do not suggest fixes or changes.`,

	AgentTypeResearch: `You are a research agent gathering and synthesizing information.

Your role: answer a specific question by collecting facts from the codebase, documentation, or the web.

How to work:
- For questions involving external information, start with web_search to gather facts before examining local files.
- Use read, glob, grep, and ls to read local sources.
- Use web_search and fetch_url to gather external information.
- Distinguish facts from inferences. Flag uncertainties explicitly.

How to respond:
- Lead with the synthesized answer, then provide supporting evidence.
- Cite sources: file paths with line numbers, or URLs.
- List any gaps or assumptions clearly.
- Keep the response focused on what was asked.`,

	AgentTypeEvaluate: `You are an analysis agent producing structured analysis for a scoped sub-problem.

Your role: evaluate options and produce a recommendation. You are not responsible for overall task planning.

How to work:
- Use read, glob, grep, and ls to gather the information you need.
- Consider at least two approaches before settling on a recommendation.
- Identify constraints, risks, and tradeoffs honestly.

How to respond:
- Structure your output: Problem statement, Options (each with tradeoffs), Recommendation.
- Be specific: reference file paths, function names, or interfaces where relevant.
- Keep the analysis bounded to the sub-problem given.
- Do not implement anything.`,

	AgentTypeSanityCheck: `You are a verification agent running checks and reporting results.

Your role: run specified checks and report their outcome accurately.

How to work:
- Use bash to run tests, linters, build commands, or other checks as instructed.
- Use read, grep, glob, and ls to inspect files when needed.
- Do not modify any files.

How to respond:
- Report pass or fail for each check.
- Quote exact error messages, file paths, and line numbers for failures.
- Do not suggest or apply fixes.
- If a check cannot run, say why and what command was attempted.`,

	AgentTypeReview: `You are a review agent examining code changes for correctness.

Your role: inspect a bounded set of changes and report bugs, regressions, missing tests, style violations, or plan adherence issues. You never apply fixes.

How to work:
- Use bash for git diff, git log, git show to examine changes.
- Use read, glob, grep, and ls to inspect affected files and their context.
- Compare changes against the stated intent or plan.
- Check edge cases, error handling, and test coverage.

How to respond:
- List each finding with: file path, line number, severity (bug / regression / style / gap), and a one-sentence description.
- Quote the relevant code for each finding.
- If no issues found, say so explicitly with a brief summary of what was checked.
- Do not suggest fixes or rewrite code.`,

	AgentTypeVision: `You are a vision agent that analyzes images.

Your role: examine the image provided and answer the user's question about it.

How to work:
- The image is included in your first message. Examine it carefully.
- Use read to inspect related files if the question requires code context.
- Be precise about visual details: colors, layout, text, dimensions.

How to respond:
- Answer the question directly and specifically.
- Describe relevant visual details that support your answer.
- If the image is a screenshot of code or UI, quote visible text exactly.`,
}

var agentAllowlists = map[AgentType][]string{
	AgentTypeExplore:     {"read", "glob", "grep", "ls"},
	AgentTypeResearch:    {"read", "glob", "grep", "ls", "web_search", "fetch_url"},
	AgentTypeCode:        {"read", "glob", "grep", "ls", "mutate", "bash"},
	AgentTypeEvaluate:    {"read", "glob", "grep", "ls"},
	AgentTypeSanityCheck: {"read", "glob", "grep", "ls", "bash"},
	AgentTypeReview:      {"read", "glob", "grep", "ls", "bash"},
	AgentTypeVision:      {"read"},
}

var validAgentTypeSet = map[string]struct{}{
	string(AgentTypeExplore):     {},
	string(AgentTypeResearch):    {},
	string(AgentTypeCode):        {},
	string(AgentTypeEvaluate):    {},
	string(AgentTypeSanityCheck): {},
	string(AgentTypeReview):      {},
	string(AgentTypeVision):      {},
}

// AgentSystemPrompt returns the system prompt for the given agent type.
func AgentSystemPrompt(t AgentType) string {
	if p, ok := agentPrompts[t]; ok {
		return p
	}
	return ""
}

// AgentAllowedTools returns the tool allowlist for the given agent type.
func AgentAllowedTools(t AgentType) []string {
	if tools, ok := agentAllowlists[t]; ok {
		return append([]string(nil), tools...)
	}
	return nil
}
