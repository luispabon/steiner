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
