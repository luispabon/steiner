package provider

import "slices"

// claude_subscription sync mismatch checks reported in
// ClaudeSubCallDiagnostics.SyncMismatchCheck. Each value names the first
// comparison a live process's record failed against the request.
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
	// claudeSubCheckInterrupted: the last turn on the record was interrupted by a
	// cancelled call or by the drain of its stale tool calls, so the record's
	// tail is not a completed turn.
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

// claudeSubSyncCheckLabels is the one list of every check label a mismatch may
// carry, so the known-label set cannot drift from the labels reported.
var claudeSubSyncCheckLabels = []string{
	claudeSubCheckRole,
	claudeSubCheckDigest,
	claudeSubCheckImages,
	claudeSubCheckToolCalls,
	claudeSubCheckShape,
	claudeSubCheckLength,
	claudeSubCheckInterrupted,
	claudeSubCheckDrained,
	claudeSubCheckTainted,
	claudeSubCheckNotStarted,
}

// claudeSubSyncMismatchNoIndex is the index of a mismatch that names no request
// position. Its role is empty.
const claudeSubSyncMismatchNoIndex = -1

// claudeSubSyncMismatchMaxIndex bounds the reported request position so a
// diagnostic stays a small integer whatever the request holds.
const claudeSubSyncMismatchMaxIndex = 1 << 16

// claudeSubSyncMismatch is the first request position a live process's sync
// record failed to accept, and the check that failed there. It carries no
// message content, digest, identifier or image data: one bounded index, one
// allowed role and one fixed check label only.
type claudeSubSyncMismatch struct {
	// Index is the position in the request's non-system messages.
	Index int
	Role  MessageRole
	Check string
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
// with no role or at a position with a known role.
func claudeSubSyncMismatchValid(mismatch claudeSubSyncMismatch) bool {
	if !claudeSubSyncCheckKnown(mismatch.Check) {
		return false
	}
	if mismatch.Index < 0 {
		return mismatch.Role == ""
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
