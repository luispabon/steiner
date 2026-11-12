package interactive

import (
	"time"

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

// unansweredResultEnvelope is one stranded sub-agent result envelope and the
// user message that carried it.
type unansweredResultEnvelope struct {
	result agent.ParsedSubAgentResult
	// timestamp is the carrying message's time; zero when the session predates
	// per-message timestamps.
	timestamp time.Time
}

// unansweredResultEnvelopes returns the sub-agent result envelopes carried by
// user messages after the last complete assistant message: results delivered to
// the model that it never answered (a failed or interrupted run, or a session
// saved mid-turn).
func unansweredResultEnvelopes(conv []agent.Message) []unansweredResultEnvelope {
	start := unansweredBoundary(conv)
	var out []unansweredResultEnvelope
	for _, msg := range conv[start:] {
		if msg.Role != agent.MessageRoleUser {
			continue
		}
		for _, raw := range prompt.SplitMessageBlocks(msg.Content).ResultEnvelopes {
			if parsed, ok := agent.ParseSubAgentResultEnvelope(raw); ok {
				out = append(out, unansweredResultEnvelope{result: parsed, timestamp: msg.Timestamp})
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
// unanswered in conv, if any. The event carries the time of the newest message
// that carried an envelope, or a zero time when the session predates
// per-message timestamps: replay must not substitute the replay clock.
func (s *Session) emitUnansweredResults(conv []agent.Message, reason string) {
	envelopes := unansweredResultEnvelopes(conv)
	if len(envelopes) == 0 {
		return
	}
	reason = unansweredResultsReason(conv, reason)
	ids := make([]string, len(envelopes))
	for i, e := range envelopes {
		ids[i] = e.result.AgentID
	}
	timestamp := envelopes[len(envelopes)-1].timestamp
	s.events.Emit(eventWithTimestamp(output.NewSubAgentResultsUnansweredEvent(ids, reason), timestamp))
}
