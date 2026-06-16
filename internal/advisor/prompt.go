package advisor

import "github.com/luispabon/steiner/internal/provider"

const (
	advisorSystemPrompt = "You are Steiner's internal advisor. Review the conversation and give concise strategic guidance for the main agent's next response. Do not call tools, do not invent tool results, and do not address the end user directly."
	advisorUserPrompt   = "Analyze the conversation above and return a short advisory note for the main agent. Focus on risks, missing reasoning, and the most useful next move."
)

func buildMessages(snapshot []provider.Message) []provider.Message {
	messages := make([]provider.Message, 0, len(snapshot)+2)
	messages = append(messages, provider.Message{
		Role:    provider.MessageRoleSystem,
		Content: advisorSystemPrompt,
	})
	messages = append(messages, provider.CloneMessages(snapshot)...)
	messages = append(messages, provider.Message{
		Role:    provider.MessageRoleUser,
		Content: advisorUserPrompt,
	})
	return messages
}
