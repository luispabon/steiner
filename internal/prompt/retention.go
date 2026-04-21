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

func retainRecentTurns(messages []provider.Message, recentTurns int) (dropped []conversationTurn, retained []conversationTurn) {
	turns := splitConversationTurns(messages)
	if len(turns) == 0 {
		return nil, nil
	}
	if recentTurns <= 0 || recentTurns >= len(turns) {
		return nil, turns
	}
	dropped = append(dropped, turns[:len(turns)-recentTurns]...)
	retained = append(retained, turns[len(turns)-recentTurns:]...)
	return dropped, retained
}

func flattenTurns(turns []conversationTurn) []provider.Message {
	if len(turns) == 0 {
		return nil
	}
	out := make([]provider.Message, 0)
	for _, turn := range turns {
		out = append(out, turn.Messages...)
	}
	return out
}
