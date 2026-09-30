package delegation

// CancelCause records why a child was cancelled.
type CancelCause uint8

const (
	// CancelCauseNone indicates no cancellation.
	CancelCauseNone CancelCause = iota
	// CancelCauseUser indicates the user cancelled the child.
	CancelCauseUser
	// CancelCauseSystem indicates the system cancelled the child.
	CancelCauseSystem
)
