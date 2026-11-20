package provider

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
// [0, claudeSubSyncMismatchMaxIndex].
func (m claudeSubSyncMismatch) clampedIndex() int {
	return min(max(m.Index, 0), claudeSubSyncMismatchMaxIndex)
}

// claudeSubSyncMismatchValid reports whether mismatch carries only bounded
// values: a known role and one of the fixed check labels.
func claudeSubSyncMismatchValid(mismatch claudeSubSyncMismatch) bool {
	return claudeSubSyncRoleKnown(mismatch.Role) && claudeSubSyncCheckKnown(mismatch.Check)
}

func claudeSubSyncRoleKnown(role MessageRole) bool {
	switch role {
	case MessageRoleSystem, MessageRoleUser, MessageRoleAssistant, MessageRoleTool:
		return true
	}
	return false
}

func claudeSubSyncCheckKnown(check string) bool {
	switch check {
	case claudeSubCheckRole, claudeSubCheckDigest, claudeSubCheckImages,
		claudeSubCheckToolCalls, claudeSubCheckShape, claudeSubCheckLength:
		return true
	}
	return false
}
