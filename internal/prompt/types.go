package prompt

import (
	"io/fs"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
)

// ContextSource identifies the origin of an assembled context block.
type ContextSource string

const (
	// ContextSourcePreamble identifies the assembled system preamble block.
	ContextSourcePreamble ContextSource = "preamble"
	// ContextSourcePhasePrompt identifies the oneshot phase orchestration prompt.
	ContextSourcePhasePrompt ContextSource = "phase_prompt"
	// ContextSourceGlobalAgentsMD identifies the global AGENTS.md block.
	ContextSourceGlobalAgentsMD ContextSource = "global_agents_md"
	// ContextSourceProjectAgentsMD identifies the project AGENTS.md block.
	ContextSourceProjectAgentsMD ContextSource = "project_agents_md"
	// ContextSourceProjectContext identifies extra project context file blocks.
	ContextSourceProjectContext ContextSource = "project_context"
	// ContextSourceSkill identifies loaded skill content blocks.
	ContextSourceSkill ContextSource = "skill"
	// ContextSourceDurableContext identifies retained durable-context blocks.
	ContextSourceDurableContext ContextSource = "durable_context"
	// ContextSourceConversationSummary identifies summarized conversation blocks.
	ContextSourceConversationSummary ContextSource = "conversation_summary"
	// ContextSourceConversation identifies raw conversation message blocks.
	ContextSourceConversation ContextSource = "conversation"
)

// IsSystemZone reports whether the source belongs to the system prompt zone.
func (s ContextSource) IsSystemZone() bool {
	switch s {
	case ContextSourcePreamble, ContextSourcePhasePrompt, ContextSourceGlobalAgentsMD, ContextSourceProjectAgentsMD, ContextSourceConversationSummary:
		return true
	default:
		return false
	}
}

// ContextBlock is a rendered context fragment included in a request.
type ContextBlock struct {
	Source    ContextSource `json:"source"`
	Path      string        `json:"path,omitempty"`
	Content   string        `json:"content"`
	ByteSize  int           `json:"byte_size"`
	Truncated bool          `json:"truncated,omitempty"`
}

// SourceBudgetModel partitions byte budgets across prompt input sources.
type SourceBudgetModel struct {
	ProjectContextBytes int
	SkillBytes          int
}

// ModelTokenBudget describes model-specific token limits and reserves.
type ModelTokenBudget struct {
	ContextSize               int
	MaxCompletionTokens       int
	SafetyMarginTokens        int
	SummaryMaxTokens          int
	NormalSummaryMaxTokens    int
	EmergencySummaryMaxTokens int
}

// RequestTokenBudget is the result of fitting a request into a model budget.
type RequestTokenBudget struct {
	RawEstimatedPromptTokens int
	EstimatedPromptTokens    int
	PromptUsage              float64
	CompactionThreshold      float64
	HardLimitTokens          int
	ShouldCompact            bool
	ReservedCompletionTokens int
	SafetyMarginTokens       int
	TotalTokens              int
	ContextSize              int
	Fits                     bool
}

// AssemblyPolicy configures prompt assembly budgets.
type AssemblyPolicy struct {
	Budgets SourceBudgetModel
}

// DurableSummaryEntry stores a retained summary carried across compactions.
type DurableSummaryEntry struct {
	Title  string `json:"title,omitempty"`
	Text   string `json:"text"`
	Source string `json:"source,omitempty"`
	Turn   int    `json:"turn,omitempty"`
}

// DurableContextState carries retained context that survives compaction.
type DurableContextState struct {
	RetainedSummaries []DurableSummaryEntry `json:"retained_summaries,omitempty"`
}

// AssemblyOptions configures prompt assembly for a run.
type AssemblyOptions struct {
	HomeDir           string
	ProjectRoot       string
	GlobalAgentsPath  string
	ProjectAgentsPath string
	// SkillsBundledFS is the optional embedded filesystem for bundled skills
	// (e.g. skills.FS from the go:embed in the skills package).
	SkillsBundledFS fs.FS
	// SkillsRoots
	SkillsRoots               []string
	SkillNames                []string
	Tools                     []provider.ToolSpec
	ModelBudget               ModelTokenBudget
	PromptOverrides           config.ModelPrompts
	ProjectContextBudgetBytes int
	ProjectContextExtraFiles  []string
	ProjectContextIgnoreFiles []string
	Policy                    AssemblyPolicy
	ContextState              DurableContextState
	DelegationEnabled         bool
	AdvisorEnabled            bool
	LSPEnabled                bool
	SandboxEnabled            bool
	// SandboxWritableMounts lists host paths mounted writable in the sandbox,
	// rendered into the sandbox system preamble section when SandboxEnabled.
	SandboxWritableMounts []string
	// WorkflowMode selects the shared workflow wording for the system preamble.
	WorkflowMode workflowMode
	// PhasePrompt carries the oneshot phase orchestration prompt. Empty outside oneshot runs.
	PhasePrompt  string
	Conversation []provider.Message
	// CachedPreamble is the pre-built system preamble string. When non-empty it
	// is used directly, bypassing SystemPreamble. All inputs to SystemPreamble
	// are session-constants, so caching once per session is safe.
	CachedPreamble string

	// CachedStaticContext, when non-nil, memoizes the file-backed static sources
	// (AGENTS.md, project context files, skills) across assembles. Callers that
	// assemble repeatedly for one session should hold a single
	// *StaticContextCache and pass it on every AssemblyOptions; leaving it nil
	// makes Assemble use a throwaway cache that loads the sources every call.
	CachedStaticContext *StaticContextCache

	// StaticContextScope identifies the session that owns CachedStaticContext.
	// When it changes, the cache drops all file-backed partitions so a new
	// session reloads them. Leave empty when the cache itself is per-run.
	StaticContextScope string

	// CaveHuman makes the model speak tersely and avoid AI-writing tells.
	CaveHuman bool

	// SkipAgents skips the agentsStep during prompt assembly, omitting global and
	// project AGENTS.md. Used for agents that cannot read the repo.
	SkipAgents bool

	// SkipProjectContext skips only the projectContextStep during prompt
	// assembly, omitting user-configured project context files. AGENTS.md
	// delivery is controlled separately by SkipAgents.
	SkipProjectContext bool
}

// Assembly is the rendered prompt plus its contributing context blocks.
type Assembly struct {
	Messages []provider.Message
	Blocks   []ContextBlock
}
