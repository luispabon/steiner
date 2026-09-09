package tool

import (
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/diagnostics"
)

func TestExecutorWithDiagnostics(t *testing.T) {
	writer, err := diagnostics.New(diagnostics.Options{Dir: t.TempDir(), Streams: diagnostics.Streams{Tool: true}})
	if err != nil {
		t.Fatalf("diagnostics.New() error = %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	executor := NewExecutor(NewRegistry(), config.Config{}, nil, t.TempDir(), "", Unsandboxed{})
	if executor.diagnostics != nil {
		t.Fatalf("diagnostics = %v on a fresh executor, want nil", executor.diagnostics)
	}
	if got := executor.WithDiagnostics(writer); got != executor {
		t.Errorf("WithDiagnostics() = %v, want the same executor for chaining", got)
	}
	if executor.diagnostics != writer {
		t.Errorf("diagnostics = %v, want the supplied writer", executor.diagnostics)
	}
	executor.WithDiagnostics(nil)
	if executor.diagnostics != nil {
		t.Errorf("diagnostics = %v after clearing, want nil", executor.diagnostics)
	}
}
