package tool

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/luispabon/steiner/internal/diagnostics"
)

// OpFailure is one failed operation surfaced from a tool result for the
// tool diagnostics stream. Path is redacted to its extension before it
// reaches the diagnostics writer; see Executor.recordDiagnostics.
type OpFailure struct {
	Op     string
	Reason string
	Path   string
}

// DiagnosticsDetail is implemented by tool results that break a single tool
// call into multiple operations (currently mutate). Executor uses it to
// build the tool stream's per-operation failures without importing
// internal/tool/builtin, which already imports this package and would
// otherwise cycle.
type DiagnosticsDetail interface {
	FailedOps() []OpFailure
}

// toolRecordPayload is the kind:"tool" diagnostics payload. Fields are fixed
// -size scalars only; Failures.PathExt never carries a path.
type toolRecordPayload struct {
	Tool       string                 `json:"tool"`
	Outcome    string                 `json:"outcome"`
	DurationMs int                    `json:"duration_ms"`
	OpsTotal   int                    `json:"ops_total"`
	OpsFailed  int                    `json:"ops_failed"`
	Failures   []toolOpFailurePayload `json:"failures,omitempty"`
}

// toolOpFailurePayload is one nested failed-operation entry.
type toolOpFailurePayload struct {
	Op      string `json:"op,omitempty"`
	Reason  string `json:"reason"`
	PathExt string `json:"path_ext,omitempty"`
}

// recordDiagnostics emits one kind:"tool" diagnostics record per Execute
// call, on every outcome — a failure rate needs a denominator of successes
// too. No-op when the tool stream is disabled or the executor has no writer.
func (e *Executor) recordDiagnostics(toolName string, input map[string]any, result any, err error, dur time.Duration) {
	if !e.diagnostics.Enabled(diagnostics.KindTool) {
		return
	}

	opsTotal := 1
	if ops, ok := input["operations"].([]any); ok {
		opsTotal = len(ops)
	}

	var failures []OpFailure
	if detail, ok := result.(DiagnosticsDetail); ok {
		failures = detail.FailedOps()
	} else if err != nil {
		failures = []OpFailure{{Reason: classifyToolError(err)}}
	}

	outcome := "ok"
	switch {
	case isPolicyDenied(err):
		outcome = "denied"
	case err != nil || len(failures) > 0:
		outcome = "error"
	}

	payload := toolRecordPayload{
		Tool:       toolName,
		Outcome:    outcome,
		DurationMs: int(dur.Milliseconds()),
		OpsTotal:   opsTotal,
		OpsFailed:  len(failures),
	}
	for _, f := range failures {
		payload.Failures = append(payload.Failures, toolOpFailurePayload{
			Op:      f.Op,
			Reason:  f.Reason,
			PathExt: pathExt(f.Path),
		})
	}

	e.diagnostics.Write(diagnostics.Record{Kind: diagnostics.KindTool, Payload: payload})
}

// pathExt returns path's extension, or "" for an extensionless name —
// including a dotfile like ".env", where filepath.Ext would otherwise return
// the whole basename and leak it into the diagnostics stream.
func pathExt(path string) string {
	ext := filepath.Ext(path)
	if ext == filepath.Base(path) {
		return ""
	}
	return ext
}

// isPolicyDenied reports whether err is a path-policy denial, so the record
// carries outcome "denied" rather than the generic "error".
func isPolicyDenied(err error) bool {
	var toolErr *ToolExecutionError
	return errors.As(err, &toolErr) && toolErr.Kind == "policy_denied"
}

// classifyToolError maps a non-mutate tool's execution error to a bounded
// reason for the tool diagnostics stream. Mutate's finer-grained
// classification lives in builtin.classifyMutateError; this covers the
// single-operation tools that only ever fail or succeed as a whole.
func classifyToolError(err error) string {
	var toolErr *ToolExecutionError
	if errors.As(err, &toolErr) {
		switch toolErr.Kind {
		case "policy_denied":
			return "path_policy"
		case "sandbox_wrapper_missing", "subprocess_failed", "invalid_json", "nonzero_exit":
			return "io_error"
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "io_error"
	}
	return "other"
}
