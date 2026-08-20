package delegation

import (
	"sync"

	"github.com/luispabon/steiner/internal/output"
)

type dispatchReleaseSink struct {
	inner   output.EventSink
	release func()
	once    sync.Once
}

func newDispatchReleaseSink(inner output.EventSink, release func()) *dispatchReleaseSink {
	return &dispatchReleaseSink{inner: inner, release: release}
}

func (s *dispatchReleaseSink) Emit(event output.Event) {
	if s.inner != nil {
		s.inner.Emit(event)
	}
	if event.Type == output.EventTypeThinkingChunk || event.Type == output.EventTypeAssistantChunk {
		s.once.Do(s.release)
	}
}
