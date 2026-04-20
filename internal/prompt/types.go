package prompt

import "github.com/luispabon/steiner/internal/provider"

type ContextSource string

const (
	ContextSourcePreamble         ContextSource = "preamble"
	ContextSourceGlobalAgentsMD   ContextSource = "global_agents_md"
	ContextSourceProjectAgentsMD  ContextSource = "project_agents_md"
	ContextSourceProjectContext   ContextSource = "project_context"
	ContextSourceSkill            ContextSource = "skill"
	ContextSourceConversation     ContextSource = "conversation"
	ContextSourceToolResult       ContextSource = "tool_result"
	ContextSourceDelegationResult ContextSource = "delegation_result"
)

type ContextBlock struct {
	Source    ContextSource `json:"source"`
	Path      string        `json:"path,omitempty"`
	Content   string        `json:"content"`
	ByteSize  int           `json:"byte_size"`
	Truncated bool          `json:"truncated,omitempty"`
}

type AssemblyOptions struct {
	HomeDir                   string
	ProjectRoot               string
	GlobalAgentsPath          string
	ProjectAgentsPath         string
	SkillsRoot                string
	SkillNames                []string
	ProjectContextBudgetBytes int
	ProjectContextExtraFiles  []string
	ProjectContextIgnoreFiles []string
	Conversation              []provider.Message
	ToolResults               []provider.Message
}

type Assembly struct {
	Messages []provider.Message
	Blocks   []ContextBlock
}
