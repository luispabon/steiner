package tool

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/diagnostics"
)

func TestDiagnosticsCaptureContext(t *testing.T) {
	if got := DiagnosticsCaptureFromContext(context.Background()); got != DiagnosticsCaptureOff {
		t.Errorf("default capture = %v, want Off", got)
	}
	ctx := WithDiagnosticsCapture(context.Background(), DiagnosticsCaptureBodies)
	if got := DiagnosticsCaptureFromContext(ctx); got != DiagnosticsCaptureBodies {
		t.Errorf("capture = %v, want Bodies", got)
	}
}

// TestExecutorInjectsDiagnosticsCapture asserts Execute stamps the handler
// context with the capture level implied by the writer: Off without a tool
// stream, Scalars with one, Bodies with capture_bodies.
func TestExecutorInjectsDiagnosticsCapture(t *testing.T) {
	cases := []struct {
		name       string
		withWriter bool
		bodies     bool
		want       DiagnosticsCapture
	}{
		{"no writer", false, false, DiagnosticsCaptureOff},
		{"scalars", true, false, DiagnosticsCaptureScalars},
		{"bodies", true, true, DiagnosticsCaptureBodies},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			var got DiagnosticsCapture
			reg := NewRegistry(ToolDef{
				Name: "read",
				Handler: func(ctx context.Context, _ map[string]any) (any, error) {
					got = DiagnosticsCaptureFromContext(ctx)
					return "ok", nil
				},
			})
			executor := NewExecutor(reg, rootOnlyConfig(), nil, root, "", Unsandboxed{})
			if tc.withWriter {
				writer, err := diagnostics.New(diagnostics.Options{Dir: t.TempDir(), Streams: diagnostics.Streams{Tool: true}, CaptureBodies: tc.bodies})
				if err != nil {
					t.Fatalf("diagnostics.New() error = %v", err)
				}
				t.Cleanup(func() { _ = writer.Close() })
				executor.WithDiagnostics(writer)
			}
			if _, err := executor.Execute(context.Background(), "read", "", map[string]any{}); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("handler capture = %v, want %v", got, tc.want)
			}
		})
	}
}
