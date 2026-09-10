package builtin

import (
	"fmt"
	"strings"
)

// maxDetailedMutateFailures bounds how many failures get full diagnostics
// (anchor context, normalized-match blocks) in a multi-failure mutate
// output. Beyond this, a batch with many bad operations would otherwise echo
// whole matched file regions repeatedly — the bounded-output invariant this
// package must keep.
const maxDetailedMutateFailures = 3

// mutateFailure is one operation's plan-phase failure, collected while the
// plan loop keeps going so a single mutate call can report every mistake in
// a batch instead of just the first.
type mutateFailure struct {
	index     int
	opType    string
	path      string
	err       error
	cascadeOp int // index of the earlier failure on the same path, or 0
}

// buildMultiFailureOutput renders every collected failure into one bounded
// message: full diagnostics for the first maxDetailedMutateFailures
// failures, one summary line each for the rest, then a total.
func buildMultiFailureOutput(failures []mutateFailure, total int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "mutate: %d of %d operations failed; nothing was applied. Fix all of them and resend the batch.\n", len(failures), total)

	detailed := failures
	var rest []mutateFailure
	if len(failures) > maxDetailedMutateFailures {
		detailed = failures[:maxDetailedMutateFailures]
		rest = failures[maxDetailedMutateFailures:]
	}

	for _, f := range detailed {
		b.WriteString("\noperation ")
		fmt.Fprintf(&b, "%d %s: %s\n", f.index, f.opType, f.err.Error())
		if f.cascadeOp != 0 {
			fmt.Fprintf(&b, "  note: operation %d on this file also failed — this may be a consequence of that failure rather than an independent problem.\n", f.cascadeOp)
		}
	}

	if len(rest) > 0 {
		fmt.Fprintf(&b, "\n%d more %s (summary only):\n", len(rest), pluralize("failure", len(rest)))
		for _, f := range rest {
			fmt.Fprintf(&b, "  operation %d %s %s: %s\n", f.index, f.opType, f.path, firstLine(f.err.Error()))
			if f.cascadeOp != 0 {
				fmt.Fprintf(&b, "    note: operation %d on this file also failed — this may be a consequence of that failure rather than an independent problem.\n", f.cascadeOp)
			}
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}
