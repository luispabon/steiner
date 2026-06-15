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
	// AgentTypePlan is the agent type for planning tasks.
	AgentTypePlan AgentType = "plan"
	// AgentTypeVerify is the agent type for verification tasks.
	AgentTypeVerify AgentType = "verify"
)

// AllAgentTypes returns all valid agent type values.
func AllAgentTypes() []AgentType {
	return []AgentType{AgentTypeExplore, AgentTypeResearch, AgentTypeCode, AgentTypePlan, AgentTypeVerify}
}

// ValidAgentType reports whether s is a recognized agent type name.
func ValidAgentType(s string) bool {
	for _, t := range AllAgentTypes() {
		if string(t) == s {
			return true
		}
	}
	return false
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
- Use read, glob, grep, and ls to read local sources.
- Use web_search and fetch_url to gather external information.
- Distinguish facts from inferences. Flag uncertainties explicitly.

How to respond:
- Synthesize findings into a structured answer.
- Cite sources: file paths with line numbers, or URLs.
- List any gaps or assumptions clearly.
- Keep the response focused on what was asked.`,

	AgentTypePlan: `You are an analysis agent producing structured analysis for a scoped sub-problem.

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

	AgentTypeVerify: `You are a verification agent running checks and reporting results.

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
}

var agentAllowlists = map[AgentType][]string{
	AgentTypeExplore:  {"read", "glob", "grep", "ls"},
	AgentTypeResearch: {"read", "glob", "grep", "ls", "web_search", "fetch_url"},
	AgentTypeCode:     {"read", "glob", "grep", "ls", "mutate", "bash"},
	AgentTypePlan:     {"read", "glob", "grep", "ls"},
	AgentTypeVerify:   {"read", "glob", "grep", "ls", "bash"},
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
