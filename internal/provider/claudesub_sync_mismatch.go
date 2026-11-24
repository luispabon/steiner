package provider

import "slices"

// claude_subscription sync mismatch checks reported in
// ClaudeSubCallDiagnostics.SyncMismatchCheck. Each value names the first
// comparison a live process's record failed against the request, and each is
// reported at the request position with that position's role. The
// record-state checks below name no position.
const (
	// claudeSubCheckRole: the request message at that position has another
	// role than the recorded entry.
	claudeSubCheckRole = "role"
	// claudeSubCheckDigest: its content digest differs from the recorded one.
	claudeSubCheckDigest = "digest"
	// claudeSubCheckImages: it claims images the record was never sent.
	claudeSubCheckImages = "images"
	// claudeSubCheckToolCalls: its tool-call ids differ from the recorded ones.
	claudeSubCheckToolCalls = "tool_calls"
	// claudeSubCheckShape: it cannot be appended at all, such as a role a fresh
	// transcript cannot start with or an assistant message the record cannot
	// take.
	claudeSubCheckShape = "shape"
	// claudeSubCheckLength: the request ends before the record does, or its
	// suffix ends before a message it needs.
	claudeSubCheckLength = "length"
)

// claude_subscription record-state refusals reported in
// ClaudeSubCallDiagnostics.SyncMismatchCheck when a summary cannot be appended
// to the live transcript. Each names the state of the record rather than a
// request position, so it is reported at claudeSubSyncMismatchNoIndex with no
// role.
const (
	// claudeSubCheckInterrupted: the drain of the record's stale tool calls
	// interrupted the last turn, and the interrupted assistant is not in the
	// retained tail, so a placed summary would cover it.
	claudeSubCheckInterrupted = "interrupted"
	// claudeSubCheckDrained: the record was driven to the aborted result of a
	// stopped delivery.
	claudeSubCheckDrained = "drained"
	// claudeSubCheckTainted: an in-place summary was already sent, so the record
	// no longer describes the CLI transcript.
	claudeSubCheckTainted = "tainted"
	// claudeSubCheckNotStarted: the record holds no delivered turn yet.
	claudeSubCheckNotStarted = "not_started"
)

// claudeSubSyncRecordStateChecks is the one list of the labels that name the
// record's state rather than a request position. Each is valid only at
// claudeSubSyncMismatchNoIndex with no role.
var claudeSubSyncRecordStateChecks = []string{
	claudeSubCheckInterrupted,
	claudeSubCheckDrained,
	claudeSubCheckTainted,
	claudeSubCheckNotStarted,
}

// claudeSubSyncCheckLabels is the one list of every check label a mismatch may
// carry, derived from the positional labels and the record-state labels, so the
// known-label set cannot drift from the labels reported.
var claudeSubSyncCheckLabels = append([]string{
	claudeSubCheckRole,
	claudeSubCheckDigest,
	claudeSubCheckImages,
	claudeSubCheckToolCalls,
	claudeSubCheckShape,
	claudeSubCheckLength,
}, claudeSubSyncRecordStateChecks...)

// claudeSubSyncMismatchNoIndex is the index of a mismatch that names no request
// position. Its role is empty.
const claudeSubSyncMismatchNoIndex = -1

// claudeSubSyncMismatchMaxIndex bounds the reported request position so a
// diagnostic stays a small integer whatever the request holds.
const claudeSubSyncMismatchMaxIndex = 1 << 16

// claudeSubSyncMismatch is the first comparison a live process's sync record
// failed: a request position it could not accept, with that position's role and
// the check that failed there, or a record-state refusal that names no position
// and no role. It carries no message content, digest, identifier or image data:
// one bounded index, one allowed role and one fixed check label only.
type claudeSubSyncMismatch struct {
	// Index is the position in the request's non-system messages, or
	// claudeSubSyncMismatchNoIndex when a record-state refusal names none.
	Index int
	Role  MessageRole
	Check string
}

// claudeSubRefusal records mismatch in *record when the caller wants
// diagnostics and returns the empty delta with err, the refusal the caller
// reports. record may be nil, and err stays the caller's own refusal error, so
// every caller keeps its error identity.
func claudeSubRefusal(record *claudeSubSyncMismatch, index int, role MessageRole, check string, err error) (claudeSubDelta, error) {
	if record != nil {
		*record = claudeSubSyncMismatch{Index: index, Role: role, Check: check}
	}
	return claudeSubDelta{}, err
}

// clampedIndex returns the position bounded to
// [claudeSubSyncMismatchNoIndex, claudeSubSyncMismatchMaxIndex]. An index below
// no-index means the mismatch names no position and reports none.
func (m claudeSubSyncMismatch) clampedIndex() int {
	if m.Index < 0 {
		return claudeSubSyncMismatchNoIndex
	}
	return min(m.Index, claudeSubSyncMismatchMaxIndex)
}

// claudeSubSyncMismatchValid reports whether mismatch carries only bounded
// values: one of the fixed check labels, reported either at no request position
// with no role or at a position with a known role. A record-state label names
// the record rather than a request position, so it is valid only at no position
// with no role.
func claudeSubSyncMismatchValid(mismatch claudeSubSyncMismatch) bool {
	if !claudeSubSyncCheckKnown(mismatch.Check) {
		return false
	}
	if mismatch.Index < 0 {
		return mismatch.Role == ""
	}
	if slices.Contains(claudeSubSyncRecordStateChecks, mismatch.Check) {
		return false
	}
	return claudeSubSyncRoleKnown(mismatch.Role)
}

func claudeSubSyncRoleKnown(role MessageRole) bool {
	switch role {
	case MessageRoleSystem, MessageRoleUser, MessageRoleAssistant, MessageRoleTool:
		return true
	}
	return false
}

// claudeSubSyncCheckKnown reports whether check is one of the labels
// claudeSubSyncCheckLabels names.
func claudeSubSyncCheckKnown(check string) bool {
	return slices.Contains(claudeSubSyncCheckLabels, check)
}
