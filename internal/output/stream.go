package output

import (
	"io"
	"sync"
)

type Subscriber interface {
	OnEvent(Event)
}

type EventStream struct {
	mu          sync.Mutex
	subscribers []Subscriber
	renderer    *PlainRenderer
}

type Stream = EventStream

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

func (s *EventStream) Println(args ...any) {
	if s == nil || s.renderer == nil {
		return
	}
	s.renderer.Println(args...)
}

func (s *EventStream) Printf(format string, args ...any) {
	if s == nil || s.renderer == nil {
		return
	}
	s.renderer.Printf(format, args...)
}

func (s *EventStream) Render(segment Segment) {
	if s == nil || s.renderer == nil {
		return
	}
	s.renderer.Render(segment)
}

func (s *EventStream) WriteAssistant(text string) {
	if s == nil || s.renderer == nil {
		return
	}
	s.renderer.WriteAssistant(text)
}

func (s *EventStream) WriteAssistantChunk(text string) {
	if s == nil || s.renderer == nil {
		return
	}
	s.renderer.WriteAssistantChunk(text)
}

func (s *EventStream) FinishAssistant() {
	if s == nil || s.renderer == nil {
		return
	}
	s.renderer.FinishAssistant()
}

func (s *EventStream) Themed(channel Channel, text string) string {
	if s == nil || s.renderer == nil {
		return text
	}
	return s.renderer.Themed(channel, text)
}
