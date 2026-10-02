package interactive

import "github.com/luispabon/steiner/internal/agent"

// toolPair is one tool call matched to the tool-result message that answers it.
type toolPair struct {
	call        replayOccurrenceKey
	callID      string
	resultIndex int
	// nameCompatible is false when the result names a different tool than the call.
	nameCompatible bool
}

// toolPairing is the outcome of pairing the calls and results in one message span.
type toolPairing struct {
	pairs []toolPair
	// unbalanced holds call IDs with a call or result left without a partner.
	unbalanced map[string]bool
}

// pairToolResults pairs assistant tool calls to tool-result messages inside
// msgs by ToolCallID. Each result answers the earliest still-unanswered call
// with the same ID that precedes it (per-ID FIFO). Calls and results with an
// empty ID never pair. Indices in the returned pairs are relative to msgs, so
// the caller chooses the span: the whole message list or one exchange.
func pairToolResults(msgs []agent.Message) toolPairing {
	pairing := toolPairing{unbalanced: make(map[string]bool)}
	queues := make(map[string][]replayOccurrenceKey)
	for messageIndex, msg := range msgs {
		switch msg.Role {
		case agent.MessageRoleAssistant:
			for callIndex, call := range msg.ToolCalls {
				if call.ID != "" {
					queues[call.ID] = append(queues[call.ID], replayOccurrenceKey{messageIndex: messageIndex, callIndex: callIndex})
				}
			}
		case agent.MessageRoleTool:
			if msg.ToolCallID == "" {
				continue
			}
			queue := queues[msg.ToolCallID]
			if len(queue) == 0 {
				pairing.unbalanced[msg.ToolCallID] = true
				continue
			}
			key := queue[0]
			queues[msg.ToolCallID] = queue[1:]
			callName := msgs[key.messageIndex].ToolCalls[key.callIndex].Name
			pairing.pairs = append(pairing.pairs, toolPair{
				call:           key,
				callID:         msg.ToolCallID,
				resultIndex:    messageIndex,
				nameCompatible: msg.Name == "" || msg.Name == callName,
			})
		}
	}
	for id, queue := range queues {
		if len(queue) > 0 {
			pairing.unbalanced[id] = true
		}
	}
	return pairing
}
