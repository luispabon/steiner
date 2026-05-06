package interactive

import (
	"context"
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

func TestRunManualCompactionEmitsLifecycleAndClearsControllerOnSuccess(t *testing.T) {
	var events []output.Event
	s, err := NewSession(Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
	})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	ctrl := s.ActiveRunController()

	result, err := s.runManualCompaction(context.Background(), "test-model", func(ctx context.Context) ([]agent.Message, error) {
		s.events.Emit(output.NewAssistantChunkEvent(1, "streamed chunk"))
		return []agent.Message{{Role: agent.MessageRoleAssistant, Content: "summary"}}, nil
	})
	if err != nil {
		t.Fatalf("runManualCompaction() error = %v, want nil", err)
	}
	if got, want := len(result), 1; got != want {
		t.Fatalf("result len = %d, want %d", got, want)
	}

	if ctrl.HasCancel() {
		t.Fatal("expected controller to be cleared after successful compaction")
	}

	if got, want := len(events), 4; got != want {
		t.Fatalf("event count = %d, want %d", got, want)
	}
	if got, want := events[0].Type, output.EventTypeRunStarted; got != want {
		t.Fatalf("events[0].Type = %q, want %q", got, want)
	}
	if got, want := events[1].Type, output.EventTypeContextDiagnostics; got != want {
		t.Fatalf("events[1].Type = %q, want %q", got, want)
	}
	if got, want := events[2].Type, output.EventTypeAssistantChunk; got != want {
		t.Fatalf("events[2].Type = %q, want %q", got, want)
	}
	if got, want := events[3].Type, output.EventTypeRunFinished; got != want {
		t.Fatalf("events[3].Type = %q, want %q", got, want)
	}

	started, ok := events[0].Payload.(output.RunStartedEvent)
	if !ok {
		t.Fatalf("events[0].Payload type = %T, want output.RunStartedEvent", events[0].Payload)
	}
	if got, want := started.Mode, "interactive"; got != want {
		t.Fatalf("run started mode = %q, want %q", got, want)
	}
	if got, want := started.Model, "test-model"; got != want {
		t.Fatalf("run started model = %q, want %q", got, want)
	}

	compacting, ok := events[1].Payload.(output.ContextDiagnosticsEvent)
	if !ok {
		t.Fatalf("events[1].Payload type = %T, want output.ContextDiagnosticsEvent", events[1].Payload)
	}
	if got, want := compacting.Kind, "compaction"; got != want {
		t.Fatalf("compaction kind = %q, want %q", got, want)
	}
	if got, want := compacting.Severity, "compacting"; got != want {
		t.Fatalf("compaction severity = %q, want %q", got, want)
	}

	finished, ok := events[3].Payload.(output.RunFinishedEvent)
	if !ok {
		t.Fatalf("events[3].Payload type = %T, want output.RunFinishedEvent", events[3].Payload)
	}
	if got, want := finished.Reason, "complete"; got != want {
		t.Fatalf("run finished reason = %q, want %q", got, want)
	}
	if got, want := finished.Error, ""; got != want {
		t.Fatalf("run finished error = %q, want %q", got, want)
	}
}

func TestRunManualCompactionEmitsRunFinishedAndClearsControllerOnError(t *testing.T) {
	var events []output.Event
	s, err := NewSession(Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
	})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	ctrl := s.ActiveRunController()

	_, compactErr := s.runManualCompaction(context.Background(), "test-model", func(context.Context) ([]agent.Message, error) {
		return nil, errors.New("boom")
	})
	if compactErr == nil {
		t.Fatal("runManualCompaction() error = nil, want non-nil")
	}
	if ctrl.HasCancel() {
		t.Fatal("expected controller to be cleared after failed compaction")
	}

	if got, want := len(events), 3; got != want {
		t.Fatalf("event count = %d, want %d", got, want)
	}
	if got, want := events[0].Type, output.EventTypeRunStarted; got != want {
		t.Fatalf("events[0].Type = %q, want %q", got, want)
	}
	if got, want := events[1].Type, output.EventTypeContextDiagnostics; got != want {
		t.Fatalf("events[1].Type = %q, want %q", got, want)
	}
	if got, want := events[2].Type, output.EventTypeRunFinished; got != want {
		t.Fatalf("events[2].Type = %q, want %q", got, want)
	}

	finished, ok := events[2].Payload.(output.RunFinishedEvent)
	if !ok {
		t.Fatalf("events[2].Payload type = %T, want output.RunFinishedEvent", events[2].Payload)
	}
	if got, want := finished.Reason, "error"; got != want {
		t.Fatalf("run finished reason = %q, want %q", got, want)
	}
	if got, want := finished.Error, "boom"; got != want {
		t.Fatalf("run finished error = %q, want %q", got, want)
	}
}

func TestRunManualCompactionCancelsAndClearsController(t *testing.T) {
	var events []output.Event
	s, err := NewSession(Dependencies{
		BaseEvents: output.SinkFunc(func(event output.Event) {
			events = append(events, event)
		}),
	})
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	ctrl := s.ActiveRunController()

	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		<-started
		ctrl.Interrupt()
		close(done)
	}()

	_, compactErr := s.runManualCompaction(context.Background(), "test-model", func(ctx context.Context) ([]agent.Message, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	<-done
	if !errors.Is(compactErr, context.Canceled) {
		t.Fatalf("runManualCompaction() error = %v, want context.Canceled", compactErr)
	}
	if ctrl.HasCancel() {
		t.Fatal("expected controller to be cleared after cancelled compaction")
	}

	if got, want := len(events), 3; got != want {
		t.Fatalf("event count = %d, want %d", got, want)
	}
	if got, want := events[0].Type, output.EventTypeRunStarted; got != want {
		t.Fatalf("events[0].Type = %q, want %q", got, want)
	}
	if got, want := events[1].Type, output.EventTypeContextDiagnostics; got != want {
		t.Fatalf("events[1].Type = %q, want %q", got, want)
	}
	if got, want := events[2].Type, output.EventTypeRunFinished; got != want {
		t.Fatalf("events[2].Type = %q, want %q", got, want)
	}

	finished, ok := events[2].Payload.(output.RunFinishedEvent)
	if !ok {
		t.Fatalf("events[2].Payload type = %T, want output.RunFinishedEvent", events[2].Payload)
	}
	if got, want := finished.Reason, "cancelled"; got != want {
		t.Fatalf("run finished reason = %q, want %q", got, want)
	}
	if got, want := finished.Error, context.Canceled.Error(); got != want {
		t.Fatalf("run finished error = %q, want %q", got, want)
	}
}
