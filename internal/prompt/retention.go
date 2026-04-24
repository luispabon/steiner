package prompt

import "github.com/luispabon/steiner/internal/provider"

type conversationTurn struct {
	Messages []provider.Message
}

func splitConversationTurns(messages []provider.Message) []conversationTurn {
	if len(messages) == 0 {
		return nil
	}

	turns := make([]conversationTurn, 0, len(messages))
	current := conversationTurn{}

	for _, message := range messages {
		if message.Role == provider.MessageRoleUser && len(current.Messages) > 0 {
			turns = append(turns, current)
			current = conversationTurn{}
		}
		current.Messages = append(current.Messages, message)
	}

	if len(current.Messages) > 0 {
		turns = append(turns, current)
	}
	return turns
}
