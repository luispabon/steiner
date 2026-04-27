package prompt

import "github.com/luispabon/steiner/internal/provider"

type ContextSource string

const (
	ContextSourcePreamble            ContextSource = "preamble"
	ContextSourceGlobalAgentsMD      ContextSource = "global_agents_md"
	ContextSourceProjectAgentsMD     ContextSource = "project_agents_md"
	ContextSourceProjectContext      ContextSource = "project_context"
	ContextSourceSkill               ContextSource = "skill"
	ContextSourceDurableContext      ContextSource = "durable_context"
	ContextSourceConversationSummary ContextSource = "conversation_summary"
	ContextSourceToolSummary         ContextSource = "tool_summary"
	ContextSourceConversation        ContextSource = "conversation"
	ContextSourceToolResult          ContextSource = "tool_result"
	ContextSourceDelegationResult    ContextSource = "delegation_result"
)

type ContextBlock struct {
	Source    ContextSource `json:"source"`
	Path      string        `json:"path,omitempty"`
	Content   string        `json:"content"`
	ByteSize  int           `json:"byte_size"`
	Truncated bool          `json:"truncated,omitempty"`
}

type SourceBudgetModel struct {
	PreambleBytes       int
	GlobalAgentsBytes   int
	ProjectAgentsBytes  int
	ProjectContextBytes int
	SkillBytes          int
	DurableContextBytes int
	ToolResultBytes     int
	ToolSummaryBytes    int
}

type CompactionPolicy struct {
	SummaryBytes int
}

type ToolSummaryPolicy struct {
	MaxBytes int
}

type ModelTokenBudget struct {
	ContextSize         int
	MaxCompletionTokens int
	SafetyMarginTokens  int
	SummaryMaxTokens    int
}

type RequestTokenBudget struct {
	EstimatedPromptTokens    int
	ReservedCompletionTokens int
	SafetyMarginTokens       int
	TotalTokens              int
	ContextSize              int
	Fits                     bool
}

type AssemblyPolicy struct {
	Budgets     SourceBudgetModel
	Compaction  CompactionPolicy
	ToolSummary ToolSummaryPolicy
}

type DurableContextEntry struct {
	Text   string `json:"text"`
	Source string `json:"source,omitempty"`
	Turn   int    `json:"turn,omitempty"`
}

type DurableSummaryEntry struct {
	Title  string `json:"title,omitempty"`
	Text   string `json:"text"`
	Source string `json:"source,omitempty"`
	Turn   int    `json:"turn,omitempty"`
}

type DurableContextState struct {
	ActiveConstraints []DurableContextEntry `json:"active_constraints,omitempty"`
	UnresolvedWork    []DurableContextEntry `json:"unresolved_work,omitempty"`
	ActiveFocus       *DurableContextEntry  `json:"active_focus,omitempty"`
	RetainedSummaries []DurableSummaryEntry `json:"retained_summaries,omitempty"`
}

type AssemblyOptions struct {
	HomeDir                   string
	ProjectRoot               string
	GlobalAgentsPath          string
	ProjectAgentsPath         string
	SkillsRoot                string
	SkillNames                []string
	Tools                     []provider.ToolSpec
	ModelBudget               ModelTokenBudget
	ProjectContextBudgetBytes int
	ProjectContextExtraFiles  []string
	ProjectContextIgnoreFiles []string
	Policy                    AssemblyPolicy
	ContextState              DurableContextState
	Conversation              []provider.Message
	ToolResults               []provider.Message
}

type Assembly struct {
	Messages []provider.Message
	Blocks   []ContextBlock
}
