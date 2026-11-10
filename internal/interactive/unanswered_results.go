package interactive

import (
	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
)

// unansweredBoundary returns the index of the first message after the last
// complete assistant message. An interrupted partial response is not an answer,
// so an Incomplete assistant message is skipped when locating the boundary: a
// few streamed words must not hide results that still need handling.
func unansweredBoundary(conv []agent.Message) int {
	for i := len(conv) - 1; i >= 0; i-- {
		if conv[i].Role == agent.MessageRoleAssistant && !conv[i].Incomplete {
			return i + 1
		}
	}
	return 0
}

// unansweredResultEnvelopes returns the sub-agent result envelopes carried by
// user messages after the last complete assistant message: results delivered to
// the model that it never answered (a failed or interrupted run, or a session
// saved mid-turn).
func unansweredResultEnvelopes(conv []agent.Message) []agent.ParsedSubAgentResult {
	start := unansweredBoundary(conv)
	var out []agent.ParsedSubAgentResult
	for _, msg := range conv[start:] {
		if msg.Role != agent.MessageRoleUser {
			continue
		}
		for _, raw := range prompt.SplitMessageBlocks(msg.Content).ResultEnvelopes {
			if parsed, ok := agent.ParseSubAgentResultEnvelope(raw); ok {
				out = append(out, parsed)
			}
		}
	}
	return out
}

// interruptedUnansweredReason replaces the run error when the tail that left the
// results waiting is an interrupted partial response. The warning then says the
// response was cut off rather than implying the model handled the results.
const interruptedUnansweredReason = "the response was interrupted before these results were handled"

func unansweredResultsReason(conv []agent.Message, reason string) string {
	for _, msg := range conv[unansweredBoundary(conv):] {
		if msg.Role == agent.MessageRoleAssistant && msg.Incomplete {
			return interruptedUnansweredReason
		}
	}
	return reason
}

// emitUnansweredResults emits the stranded-result event for the envelopes
// unanswered in conv, if any.
func (s *Session) emitUnansweredResults(conv []agent.Message, reason string) {
	envelopes := unansweredResultEnvelopes(conv)
	if len(envelopes) == 0 {
		return
	}
	reason = unansweredResultsReason(conv, reason)
	ids := make([]string, len(envelopes))
	for i, e := range envelopes {
		ids[i] = e.AgentID
	}
	s.events.Emit(output.NewSubAgentResultsUnansweredEvent(ids, reason))
}
