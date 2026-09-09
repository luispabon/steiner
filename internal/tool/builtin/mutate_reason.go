package builtin

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/luispabon/steiner/internal/tool"
)

// Mutate failure reasons, aggregated into the tool diagnostics stream (issue
// #707 stage 4). Free-text error messages don't aggregate; this enum does.
const (
	ReasonNoMatch        = "no_match"
	ReasonAmbiguousMatch = "ambiguous_match"
	ReasonMissingFile    = "missing_file"
	ReasonPathPolicy     = "path_policy"
	ReasonApprovalDenied = "approval_denied"
	ReasonStaleRead      = "stale_read"
	ReasonIOError        = "io_error"
	ReasonOther          = "other"
)

// classifyMutateError maps a mutate planning or commit error to a bounded
// reason for the tool diagnostics stream. The planners in mutate_ops.go and
// mutate_planner.go build these errors with stable, deterministic wording —
// "no match"/"ambiguous match" (mutate_diagnostics.go), "does not exist",
// "file_hash mismatch", "not read this session" — so matching on that text
// is safe even though the errors are not sentinel-typed.
//
// ReasonApprovalDenied is not reachable from mutate's own error space: an
// approval denial happens in the execution pipeline before the mutate
// handler runs at all, so the whole call is classified there instead (see
// tool.classifyToolError) rather than as a mutate operation failure.
func classifyMutateError(err error) string {
	if err == nil {
		return ReasonOther
	}
	var policyErr *tool.PathPolicyError
	if errors.As(err, &policyErr) {
		return ReasonPathPolicy
	}
	if errors.Is(err, os.ErrNotExist) {
		return ReasonMissingFile
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return ReasonIOError
	}

	msg := err.Error()
	switch {
	case strings.Contains(msg, "no match for old_string"):
		return ReasonNoMatch
	case strings.Contains(msg, "ambiguous match for old_string"):
		return ReasonAmbiguousMatch
	case strings.Contains(msg, "does not exist"):
		return ReasonMissingFile
	case strings.Contains(msg, "file_hash mismatch"), strings.Contains(msg, "not read this session"):
		return ReasonStaleRead
	default:
		return ReasonOther
	}
}
