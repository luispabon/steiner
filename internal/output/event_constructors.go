package output

import "time"

// WithAgentScope attaches child-agent transcript scope metadata to an event.
func WithAgentScope(event Event, agentID string) Event {
	if agentID == "" {
		return event
	}
	event.Scope.AgentID = agentID
	return event
}

// WithAgentTypeScope attaches child-agent type metadata to an event.
func WithAgentTypeScope(event Event, agentType string) Event {
	if agentType == "" {
		return event
	}
	event.Scope.AgentType = agentType
	return event
}

func newEvent(eventType string, payload any) Event {
	return Event{
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}
