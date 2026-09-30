package interactive

import (
	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
)

// unansweredResultEnvelopes returns the sub-agent result envelopes carried by
// user messages after the last assistant message: results delivered to the
// model that it never answered (a failed or interrupted run, or a session
// saved mid-turn).
func unansweredResultEnvelopes(conv []agent.Message) []agent.ParsedSubAgentResult {
	start := 0
	for i := len(conv) - 1; i >= 0; i-- {
		if conv[i].Role == agent.MessageRoleAssistant {
			start = i + 1
			break
		}
	}
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

// emitUnansweredResults emits the stranded-result event for the envelopes
// unanswered in conv, if any.
func (s *Session) emitUnansweredResults(conv []agent.Message, reason string) {
	envelopes := unansweredResultEnvelopes(conv)
	if len(envelopes) == 0 {
		return
	}
	ids := make([]string, len(envelopes))
	for i, e := range envelopes {
		ids[i] = e.AgentID
	}
	s.events.Emit(output.NewSubAgentResultsUnansweredEvent(ids, reason))
}
