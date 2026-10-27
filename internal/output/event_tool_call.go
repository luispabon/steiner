package output

import "github.com/luispabon/steiner/internal/tool"

// ToolCallStartedEvent records a tool invocation before execution begins.
type ToolCallStartedEvent struct {
	Turn      int            `json:"turn"`
	Tool      string         `json:"tool,omitempty"`
	CallID    string         `json:"call_id,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// ToolCallQueuedEvent records a delegation tool invocation announced before it
// acquires a parallelism slot, so the UI can show it waiting. It mirrors
// ToolCallStartedEvent; the matching ToolCallStartedEvent follows once the call
// is actually dispatched.
type ToolCallQueuedEvent struct {
	Turn      int            `json:"turn"`
	Tool      string         `json:"tool,omitempty"`
	CallID    string         `json:"call_id,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// DelegationAdmission is UI and event metadata for one delegation call.
type DelegationAdmission = tool.DelegationAdmission

// ToolCallFinishedEvent records a completed tool invocation.
type ToolCallFinishedEvent struct {
	Turn                int                  `json:"turn"`
	Tool                string               `json:"tool,omitempty"`
	CallID              string               `json:"call_id,omitempty"`
	Result              string               `json:"result,omitempty"`
	Error               string               `json:"error,omitempty"`
	Preview             ToolPreview          `json:"-"`
	DelegationAdmission *DelegationAdmission `json:"delegation_admission,omitempty"`
}

// BashArtifactTruncatedEvent warns that a successful bash call's saved full
// output hit the artifact size cap and dropped bytes. FilePath is empty when no
// artifact path was recorded.
type BashArtifactTruncatedEvent struct {
	Turn     int    `json:"turn"`
	CallID   string `json:"call_id,omitempty"`
	FilePath string `json:"file_path,omitempty"`
	Message  string `json:"message"`
}

// NewToolCallStartedEvent creates a new tool call started event.
func NewToolCallStartedEvent(turn int, toolName, callID string, arguments map[string]any) Event {
	return newEvent(EventTypeToolCallStarted, ToolCallStartedEvent{
		Turn:      turn,
		Tool:      toolName,
		CallID:    callID,
		Arguments: arguments,
	})
}

// NewToolCallQueuedEvent creates a new tool call queued event. Emitted only for
// delegation-class calls that have not yet acquired a parallelism slot.
func NewToolCallQueuedEvent(turn int, toolName, callID string, arguments map[string]any) Event {
	return newEvent(EventTypeToolCallQueued, ToolCallQueuedEvent{
		Turn:      turn,
		Tool:      toolName,
		CallID:    callID,
		Arguments: arguments,
	})
}

// NewToolCallFinishedEvent creates a new tool call finished event.
func NewToolCallFinishedEvent(turn int, toolName, callID string, result string, err error) Event {
	return NewToolCallFinishedEventWithAdmission(turn, toolName, callID, result, err, ToolPreview{}, nil)
}

// NewToolCallFinishedEventWithAdmission creates a finished event with preview and optional admission metadata. A nil admission leaves DelegationAdmission unset.
func NewToolCallFinishedEventWithAdmission(turn int, toolName, callID, result string, err error, preview ToolPreview, admission *DelegationAdmission) Event {
	payload := ToolCallFinishedEvent{
		Turn:                turn,
		Tool:                toolName,
		CallID:              callID,
		Result:              result,
		Preview:             preview,
		DelegationAdmission: admission.Clone(),
	}
	if err != nil {
		payload.Error = err.Error()
	}
	return newEvent(EventTypeToolCallFinished, payload)
}

// NewBashArtifactTruncatedEvent creates a bash artifact truncation warning.
func NewBashArtifactTruncatedEvent(turn int, callID, filePath, message string) Event {
	return newEvent(EventTypeBashArtifactTruncated, BashArtifactTruncatedEvent{
		Turn:     turn,
		CallID:   callID,
		FilePath: filePath,
		Message:  message,
	})
}
