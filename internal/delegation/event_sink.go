package delegation

import "github.com/luispabon/steiner/internal/output"

// scopedEventSink tags emitted child-run events with the child agent scope.
type scopedEventSink struct {
	sink    output.EventSink
	agentID string
}

func (s scopedEventSink) Emit(event output.Event) {
	if s.sink == nil {
		return
	}
	s.sink.Emit(output.WithAgentScope(event, s.agentID))
}

func withAgentScope(agentID string, sink output.EventSink) output.EventSink {
	if sink == nil || agentID == "" {
		return sink
	}
	return scopedEventSink{sink: sink, agentID: agentID}
}
