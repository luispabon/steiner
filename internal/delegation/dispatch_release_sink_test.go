package delegation

import (
	"reflect"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
)

func TestDispatchReleaseSinkForwardsAndReleasesOnce(t *testing.T) {
	wantEvents := []output.Event{
		{Type: output.EventTypeModelCallStarted, Payload: "started"},
		{Type: output.EventTypeThinkingChunk, Payload: "thinking"},
		{Type: output.EventTypeAssistantChunk, Payload: "assistant"},
		{Type: output.EventTypeAssistantMessage, Payload: "message"},
	}
	var gotEvents []output.Event
	releaseCalls := 0
	sink := newDispatchReleaseSink(output.SinkFunc(func(event output.Event) {
		gotEvents = append(gotEvents, event)
	}), func() {
		releaseCalls++
	})

	for _, event := range wantEvents {
		sink.Emit(event)
	}

	if !reflect.DeepEqual(gotEvents, wantEvents) {
		t.Errorf("forwarded events = %#v, want %#v", gotEvents, wantEvents)
	}
	if releaseCalls != 1 {
		t.Errorf("release called %d times, want 1", releaseCalls)
	}
}

func TestDispatchReleaseSinkNilInnerStillReleases(t *testing.T) {
	releaseCalls := 0
	sink := newDispatchReleaseSink(nil, func() {
		releaseCalls++
	})

	sink.Emit(output.Event{Type: output.EventTypeAssistantChunk})
	if releaseCalls != 1 {
		t.Errorf("release called %d times, want 1", releaseCalls)
	}
}

func TestDispatchReleaseSinkReleaseRunsAfterForward(t *testing.T) {
	order := []string{}
	sink := newDispatchReleaseSink(output.SinkFunc(func(output.Event) {
		order = append(order, "forward")
	}), func() {
		order = append(order, "release")
	})

	sink.Emit(output.Event{Type: output.EventTypeThinkingChunk})
	want := []string{"forward", "release"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("call order = %v, want %v", order, want)
	}
}

func TestDispatchReleaseSinkNonMatchingEventsDoNotRelease(t *testing.T) {
	releaseCalls := 0
	sink := newDispatchReleaseSink(output.NoopSink{}, func() {
		releaseCalls++
	})

	sink.Emit(output.Event{Type: output.EventTypeModelCallStarted})
	sink.Emit(output.Event{Type: output.EventTypeAssistantMessage})
	if releaseCalls != 0 {
		t.Errorf("release called %d times, want 0", releaseCalls)
	}

	// Ensure matching-event release remains immediate after non-matching events.
	done := make(chan struct{})
	sink = newDispatchReleaseSink(nil, func() {
		close(done)
	})
	sink.Emit(output.Event{Type: output.EventTypeAssistantChunk})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("release did not run for matching event")
	}
}
