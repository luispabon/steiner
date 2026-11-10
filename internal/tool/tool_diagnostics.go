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
	// Match carries derived scalar features for an eligible failed mutate
	// replace. Nil when the tool stream is off or the operation is ineligible.
	Match *MatchFailure
	// Sample carries bounded raw evidence for an eligible failed mutate
	// replace, recorded only under capture_bodies. Nil otherwise.
	Sample *MatchSample
}

// MatchFailure is the scalar feature set recorded for a failed mutate replace
// (no_match, ambiguous_match, stale_read). Every field is a fixed-size scalar
// or a short enum; nothing here carries file content or paths. The field
// definitions live in internal/tool/builtin/mutate_match_features.go.
type MatchFailure struct {
	OldBytes         int    `json:"old_bytes"`
	OldLines         int    `json:"old_lines"`
	NonBlankLines    int    `json:"nonblank_lines"`
	LinesFound       int    `json:"lines_found"`
	LongestRun       int    `json:"longest_run"`
	ExactPrefixLines int    `json:"exact_prefix_lines"`
	TrimPrefixLines  int    `json:"trim_prefix_lines"`
	WhitespaceKind   string `json:"ws_kind"`
	IndentDeltaMax   int    `json:"indent_delta_max"`
	TabsVsSpaces     bool   `json:"tabs_vs_spaces"`
	CRLFMismatch     bool   `json:"crlf_mismatch"`
	UnescapeMatches  bool   `json:"unescape_matches"`
	LinePrefix       bool   `json:"line_prefix"`
	MatchesOriginal  bool   `json:"matches_original"`
	MatchCount       int    `json:"match_count"`
	ReadState        string `json:"read_state"`
	TurnsSinceRead   int    `json:"turns_since_read"`
	InReadRange      string `json:"in_read_range"`
	LocusLine        int    `json:"locus_line"`
	FileLines        int    `json:"file_lines"`
	FileHashSupplied bool   `json:"file_hash_supplied"`
	Truncated        bool   `json:"truncated"`
}

// MatchSample is bounded raw evidence for a failed mutate replace. It is
// recorded only when diagnostics.capture_bodies is on, which already declares
// that the stream may carry content.
type MatchSample struct {
	Path            string `json:"path"`
	OldString       string `json:"old_string"`
	OldTruncated    bool   `json:"old_truncated"`
	Region          string `json:"region"`
	RegionStartLine int    `json:"region_start_line"`
	RegionTruncated bool   `json:"region_truncated"`
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
	Tool       string `json:"tool"`
	Model      string `json:"model,omitempty"`
	Outcome    string `json:"outcome"`
	DurationMs int    `json:"duration_ms"`
	OpsTotal   int    `json:"ops_total"`
	OpsFailed  int    `json:"ops_failed"`
	// OpsRequested counts the operation types the call asked for, keyed by the
	// tool's fixed operation labels. It is a pointer so an absent field and an
	// empty count map ({}), meaning zero operations requested, stay distinct.
	OpsRequested *opsRequested          `json:"ops_requested,omitempty"`
	Failures     []toolOpFailurePayload `json:"failures,omitempty"`
}

// opsRequested is the ops_requested payload: fixed operation labels mapped to
// requested counts. Nothing in it comes from input values.
type opsRequested map[string]int

// Bounds on a tool's ops_requested contribution, so a faulty extractor cannot
// grow the record without limit.
const (
	maxRequestedOpLabels = 16
	maxOpLabelLen        = 24
)

// toolOpFailurePayload is one nested failed-operation entry.
type toolOpFailurePayload struct {
	Op      string        `json:"op,omitempty"`
	Reason  string        `json:"reason"`
	PathExt string        `json:"path_ext,omitempty"`
	Match   *MatchFailure `json:"match,omitempty"`
	Sample  *MatchSample  `json:"sample,omitempty"`
}

// recordDiagnostics emits one kind:"tool" diagnostics record per Execute
// call, on every outcome — a failure rate needs a denominator of successes
// too. No-op when the tool stream is disabled or the executor has no writer.
func (e *Executor) recordDiagnostics(ctx context.Context, toolName string, input map[string]any, result any, err error, dur time.Duration) {
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
		Tool:         toolName,
		Outcome:      outcome,
		DurationMs:   int(dur.Milliseconds()),
		OpsTotal:     opsTotal,
		OpsFailed:    len(failures),
		OpsRequested: e.requestedOps(toolName, input),
	}
	for _, f := range failures {
		payload.Failures = append(payload.Failures, toolOpFailurePayload{
			Op:      f.Op,
			Reason:  f.Reason,
			PathExt: pathExt(f.Path),
			Match:   f.Match,
			Sample:  f.Sample,
		})
	}

	record := diagnostics.Record{
		Kind:      diagnostics.KindTool,
		Source:    e.scope.source,
		AgentID:   e.scope.agentID,
		AgentType: e.scope.agentType,
	}
	if call, ok := CallDiagnosticsFromContext(ctx); ok {
		record.Turn = call.Turn
		payload.Model = call.Model
	}
	record.Payload = payload

	e.diagnostics.Write(record)
}

// requestedOps returns the tool's requested per-operation-type counts for one
// call, or nil when the record must omit ops_requested. It reads the raw call
// input, so counts survive policy denials and handler failures, and it only
// runs once recordDiagnostics has confirmed the tool stream is enabled. Nothing
// it returns can break the call: a nil or panicking extractor, or one that
// returns labels which are not fixed identifiers, yields nil rather than a
// malformed record. ops_total is computed separately and never changes.
func (e *Executor) requestedOps(toolName string, input map[string]any) (counts *opsRequested) {
	if e.registry == nil {
		return nil
	}
	// Read the stored definition directly: Registry.Get clones the parameter
	// schema, which this lookup never needs.
	def, ok := e.registry.defs[toolName]
	if !ok || def.OpsRequested == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			counts = nil
		}
	}()
	return sanitizeRequestedOps(def.OpsRequested(input))
}

// sanitizeRequestedOps bounds an extractor result to fixed operation labels.
// Labels that are not lowercase identifiers, negative counts, and oversized
// label sets are dropped; when nothing usable remains the field is omitted.
func sanitizeRequestedOps(raw map[string]int) *opsRequested {
	if raw == nil || len(raw) > maxRequestedOpLabels {
		return nil
	}
	out := make(opsRequested, len(raw))
	for label, count := range raw {
		if count < 0 || !fixedOpLabel(label) {
			continue
		}
		out[label] = count
	}
	if len(raw) > 0 && len(out) == 0 {
		return nil
	}
	return &out
}

// fixedOpLabel reports whether label has the shape of a fixed operation name
// (lowercase ASCII, digits, underscores; leading letter; bounded length). Model
// text, paths, and file content do not pass it.
func fixedOpLabel(label string) bool {
	if label == "" || len(label) > maxOpLabelLen {
		return false
	}
	for i := 0; i < len(label); i++ {
		c := label[i]
		switch {
		case c >= 'a' && c <= 'z':
		case i > 0 && c >= '0' && c <= '9':
		case i > 0 && c == '_':
		default:
			return false
		}
	}
	return true
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
		case "invalid_tool_input":
			return "invalid_input"
		case "sandbox_wrapper_missing", "subprocess_failed", "invalid_json", "nonzero_exit":
			return "io_error"
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "io_error"
	}
	return "other"
}
