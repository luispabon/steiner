package prompt

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
	Source   ContextSource `json:"source"`
	Content  string        `json:"content"`
	ByteSize int           `json:"byte_size"`
}
