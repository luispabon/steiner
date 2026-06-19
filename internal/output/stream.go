package output

import (
	"io"
	"sync"
)

// Subscriber receives events emitted by an EventStream.
type Subscriber interface {
	OnEvent(Event)
}

// EventStream emits events to a renderer and subscribed observers.
type EventStream struct {
	mu          sync.Mutex
	subscribers []Subscriber
	renderer    *PlainRenderer
}

// NewStream creates a new event stream that writes to w.
func NewStream(w io.Writer, options ...StreamOption) *EventStream {
	return &EventStream{
		renderer: NewPlainRenderer(w, options...),
	}
}

// NewEventStream creates a new event stream with the given subscribers.
func NewEventStream(subscribers ...Subscriber) *EventStream {
	stream := &EventStream{}
	stream.Subscribe(subscribers...)
	return stream
}

// Subscribe adds non-nil subscribers to the stream.
func (s *EventStream) Subscribe(subscribers ...Subscriber) {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, subscriber := range subscribers {
		if subscriber == nil {
			continue
		}
		s.subscribers = append(s.subscribers, subscriber)
	}
}

// Emit sends an event to the renderer and all current subscribers.
func (s *EventStream) Emit(event Event) {
	if s == nil {
		return
	}

	s.mu.Lock()
	subscribers := make([]Subscriber, 0, len(s.subscribers))
	subscribers = append(subscribers, s.subscribers...)
	renderer := s.renderer
	s.mu.Unlock()

	if renderer != nil {
		renderer.OnEvent(event)
	}
	for _, subscriber := range subscribers {
		subscriber.OnEvent(event)
	}
}

// Println writes a plain renderer line when a renderer is configured.
func (s *EventStream) Println(args ...any) {
	if s == nil || s.renderer == nil {
		return
	}
	s.renderer.Println(args...)
}

// Printf writes formatted plain renderer output when a renderer is configured.
func (s *EventStream) Printf(format string, args ...any) {
	if s == nil || s.renderer == nil {
		return
	}
	s.renderer.Printf(format, args...)
}

// Render writes a styled segment when a renderer is configured.
func (s *EventStream) Render(segment Segment) {
	if s == nil || s.renderer == nil {
		return
	}
	s.renderer.Render(segment)
}

// WriteAssistant writes assistant text when a renderer is configured.
func (s *EventStream) WriteAssistant(text string) {
	if s == nil || s.renderer == nil {
		return
	}
	s.renderer.WriteAssistant(text)
}

// WriteAssistantChunk writes a streamed assistant chunk when a renderer is configured.
func (s *EventStream) WriteAssistantChunk(text string) {
	if s == nil || s.renderer == nil {
		return
	}
	s.renderer.WriteAssistantChunk(text)
}

// FinishAssistant finishes the current assistant render when a renderer is configured.
func (s *EventStream) FinishAssistant() {
	if s == nil || s.renderer == nil {
		return
	}
	s.renderer.FinishAssistant()
}

// Themed applies channel theming when a renderer is configured.
func (s *EventStream) Themed(channel Channel, text string) string {
	if s == nil || s.renderer == nil {
		return text
	}
	return s.renderer.Themed(channel, text)
}
