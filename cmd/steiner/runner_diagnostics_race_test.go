package main

import (
	"sync"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

// TestRetainDiagnosticEventsConcurrentEmit proves the retained-diagnostics
// slice tolerates concurrent emission, which parallel tool execution produces:
// without the append mutex this races and drops or corrupts entries.
func TestRetainDiagnosticEventsConcurrentEmit(t *testing.T) {
	t.Parallel()

	events, diagnostics := retainDiagnosticEvents(output.SinkFunc(func(output.Event) {}))

	const goroutines = 8
	const perGoroutine = 50

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				events.Emit(output.NewStopReasonEvent(i, "concurrent", nil))
				events.Emit(output.NewModeChangedEvent("build"))
			}
		}()
	}
	wg.Wait()

	if got, want := len(*diagnostics), goroutines*perGoroutine; got != want {
		t.Fatalf("retained diagnostics = %d, want %d", got, want)
	}
	for i, event := range *diagnostics {
		if event.Type != output.EventTypeStopReason {
			t.Fatalf("retained[%d].Type = %q, want %q", i, event.Type, output.EventTypeStopReason)
		}
	}
}
