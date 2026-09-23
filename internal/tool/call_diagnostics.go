package tool

import (
	"context"

	"github.com/luispabon/steiner/internal/diagnostics"
)

// CallDiagnostics carries per-call attribution for the tool diagnostics
// stream: the agent-loop turn that issued the call and the backend model that
// produced it. It rides the execution context, so the executor can stamp the
// record it writes without the caller passing the values explicitly.
type CallDiagnostics struct {
	Turn  int
	Model string
}

// callDiagnosticsKey is the context key carrying CallDiagnostics into tool
// handlers, mirroring fileObservedCheckerKey.
type callDiagnosticsKey struct{}

// WithCallDiagnostics returns a context carrying d so the executor can stamp
// the tool-stream record with the issuing turn and model.
func WithCallDiagnostics(ctx context.Context, d CallDiagnostics) context.Context {
	return context.WithValue(ctx, callDiagnosticsKey{}, d)
}

// CallDiagnosticsFromContext returns the CallDiagnostics attached by
// WithCallDiagnostics, and false when absent.
func CallDiagnosticsFromContext(ctx context.Context) (CallDiagnostics, bool) {
	d, ok := ctx.Value(callDiagnosticsKey{}).(CallDiagnostics)
	return d, ok
}

// diagnosticsScope tags every tool-stream record an executor writes with its
// caller: the top-level parent, or a sub-agent's ID and type. Set once at
// construction via WithDiagnosticsScope.
type diagnosticsScope struct {
	source    diagnostics.Source
	agentID   string
	agentType string
}

// DiagnosticsCapture is the level of detail the tool diagnostics stream may
// record for one call. It rides the execution context so the executor can tell
// a handler whether it may compute features, and whether it may retain raw
// content.
type DiagnosticsCapture int

const (
	// DiagnosticsCaptureOff means the tool stream is disabled: compute nothing.
	DiagnosticsCaptureOff DiagnosticsCapture = iota
	// DiagnosticsCaptureScalars means features only, no raw content.
	DiagnosticsCaptureScalars
	// DiagnosticsCaptureBodies means features plus raw samples (stage D).
	DiagnosticsCaptureBodies
)

// diagnosticsCaptureKey is the context key carrying DiagnosticsCapture into
// tool handlers, mirroring callDiagnosticsKey.
type diagnosticsCaptureKey struct{}

// WithDiagnosticsCapture returns a context carrying c so a handler can decide
// how much match-failure detail to compute.
func WithDiagnosticsCapture(ctx context.Context, c DiagnosticsCapture) context.Context {
	return context.WithValue(ctx, diagnosticsCaptureKey{}, c)
}

// DiagnosticsCaptureFromContext returns the capture level attached by
// WithDiagnosticsCapture, or DiagnosticsCaptureOff when absent.
func DiagnosticsCaptureFromContext(ctx context.Context) DiagnosticsCapture {
	c, _ := ctx.Value(diagnosticsCaptureKey{}).(DiagnosticsCapture)
	return c
}
