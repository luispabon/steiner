package prompt

import (
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
)

// ContextSource identifies the origin of an assembled context block.
type ContextSource string

const (
	// ContextSourcePreamble identifies the assembled system preamble block.
	ContextSourcePreamble ContextSource = "preamble"
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
	// ContextSourceToolSummary identifies summarized tool output blocks.
	ContextSourceToolSummary ContextSource = "tool_summary"
	// ContextSourceConversation identifies raw conversation message blocks.
	ContextSourceConversation ContextSource = "conversation"
	// ContextSourceToolResult identifies raw tool result blocks.
	ContextSourceToolResult ContextSource = "tool_result"
	// ContextSourceDelegationResult identifies delegated sub-agent result blocks.
	ContextSourceDelegationResult ContextSource = "delegation_result"
)

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
	PreambleBytes       int
	GlobalAgentsBytes   int
	ProjectAgentsBytes  int
	ProjectContextBytes int
	SkillBytes          int
	ToolResultBytes     int
	ToolSummaryBytes    int
}

// CompactionPolicy configures conversation summary budgets.
type CompactionPolicy struct {
	SummaryBytes int
}

// ToolSummaryPolicy configures tool-output summary budgets.
type ToolSummaryPolicy struct {
	MaxBytes int
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

// AssemblyPolicy configures prompt assembly budgets and summarization policies.
type AssemblyPolicy struct {
	Budgets     SourceBudgetModel
	Compaction  CompactionPolicy
	ToolSummary ToolSummaryPolicy
}

// DurableContextEntry stores one durable context item retained across turns.
type DurableContextEntry struct {
	Text   string `json:"text"`
	Source string `json:"source,omitempty"`
	Turn   int    `json:"turn,omitempty"`
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
	HomeDir                   string
	ProjectRoot               string
	GlobalAgentsPath          string
	ProjectAgentsPath         string
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
	ScratchpadEnabled         bool
	DelegationEnabled         bool
	Conversation              []provider.Message
	ToolResults               []provider.Message
	// CachedPreamble is the pre-built system preamble string. When non-empty it
	// is used directly, bypassing SystemPreamble. Both inputs to SystemPreamble
	// (PromptOverrides.System, ScratchpadEnabled, and DelegationEnabled) are
	// session-constants, so caching once per session is safe.
	CachedPreamble string
}

// Assembly is the rendered prompt plus its contributing context blocks.
type Assembly struct {
	Messages []provider.Message
	Blocks   []ContextBlock
}
