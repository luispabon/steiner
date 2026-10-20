package output

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/provider"
)

// StopReasonEvent is the payload for EventTypeStopReason.
type StopReasonEvent struct {
	Reason  string `json:"reason"`
	Turn    int    `json:"turn,omitempty"`
	Error   string `json:"error,omitempty"`
	Summary string `json:"summary,omitempty"`
	Action  string `json:"action,omitempty"`
}

// ImageBlock mirrors agent.ImageBlock's shape without importing internal/agent
// (which itself imports internal/output — importing it back would cycle).
type ImageBlock struct {
	ID        string `json:"id,omitempty"`
	FilePath  string `json:"file_path,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	SizeBytes int    `json:"size_bytes,omitempty"`
}

// UserInputEvent captures user input forwarded into the event stream.
type UserInputEvent struct {
	Content string       `json:"content"`
	Mode    string       `json:"mode,omitempty"`
	Images  []ImageBlock `json:"images,omitempty"`
}

// RunStartedEvent marks the start of a top-level run or compaction flow.
type RunStartedEvent struct {
	Mode      string `json:"mode,omitempty"`
	Model     string `json:"model,omitempty"`
	Prompt    string `json:"prompt,omitempty"`
	MaxTurns  int    `json:"max_turns,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

// RunFinishedEvent records the terminal outcome of a run.
type RunFinishedEvent struct {
	Turn       int    `json:"turn,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Summary    string `json:"summary,omitempty"`
	NextAction string `json:"next_action,omitempty"`
	Error      string `json:"error,omitempty"`
}

// SteerReceivedEvent is emitted when a between-turn steering message is consumed.
type SteerReceivedEvent struct {
	Text   string       `json:"text"`
	Images []ImageBlock `json:"images,omitempty"`
}

// NewStopReasonEvent creates a new stop reason event.
func NewStopReasonEvent(turn int, reason string, err error) Event {
	payload := StopReasonEvent{
		Reason: reason,
		Turn:   turn,
	}
	if err != nil {
		payload.Error = err.Error()
	}
	payload.Summary, payload.Action = stopReasonSummary(reason, turn)
	if strings.TrimSpace(reason) == "usage_limit" {
		if ule, ok := provider.AsUsageLimit(err); ok && ule.HasReset() {
			payload.Action = usageLimitActionReset
		}
	}
	return newEvent(EventTypeStopReason, payload)
}

const (
	usageLimitActionReset   = "wait until the limit resets, then continue"
	usageLimitActionUnknown = "check your provider plan, billing, or budget, then continue"
)

func stopReasonSummary(reason string, turn int) (string, string) {
	switch strings.TrimSpace(reason) {
	case "complete":
		if turn > 0 {
			return fmt.Sprintf("run complete after %d turn%s", turn, PluralSuffix(turn, "", "s")), ""
		}
		return "run complete", ""
	case "workflow_handoff":
		return "workflow handoff accepted", "clear the current conversation and start the next workflow"
	case "max_turns":
		summary := "stopped after reaching the max turn limit"
		if turn > 0 {
			summary = fmt.Sprintf("stopped after %d turn%s: reached the max turn limit", turn, PluralSuffix(turn, "", "s"))
		}
		return summary, "increase limits.max_turns or continue in a new prompt"
	case "max_tokens":
		summary := "stopped after reaching the max token limit"
		if turn > 0 {
			summary = fmt.Sprintf("stopped at turn %d: reached the max token limit", turn)
		}
		return summary, "increase limits.max_tokens or reduce prompt and tool output size"
	case "cancelled":
		if turn > 0 {
			return fmt.Sprintf("run cancelled at turn %d", turn), "inspect /history for retained diagnostics or retry when you are ready to continue"
		}
		return "run cancelled", "inspect /history for retained diagnostics or retry when you are ready to continue"
	case "usage_limit":
		return "stopped: provider usage limit reached", usageLimitActionUnknown
	case "error":
		return "run failed", "inspect the reported error and retry"
	default:
		if strings.TrimSpace(reason) == "" {
			return "", ""
		}
		return "stopped: " + reason, ""
	}
}

// NewUserInputEvent creates a new user input event.
func NewUserInputEvent(content, mode string, images []ImageBlock) Event {
	return newEvent(EventTypeUserInput, UserInputEvent{
		Content: content,
		Mode:    mode,
		Images:  images,
	})
}

// NewRunStartedEvent creates a new run started event.
func NewRunStartedEvent(mode, model, prompt string, maxTurns, maxTokens int) Event {
	return newEvent(EventTypeRunStarted, RunStartedEvent{
		Mode:      mode,
		Model:     model,
		Prompt:    prompt,
		MaxTurns:  maxTurns,
		MaxTokens: maxTokens,
	})
}

// NewRunFinishedEvent creates a new run finished event.
func NewRunFinishedEvent(turn int, reason, summary, nextAction string, err error) Event {
	payload := RunFinishedEvent{
		Turn:       turn,
		Reason:     reason,
		Summary:    summary,
		NextAction: nextAction,
	}
	if err != nil {
		payload.Error = err.Error()
	}
	return newEvent(EventTypeRunFinished, payload)
}

// NewSteerReceivedEvent creates an Event for a consumed steer message.
func NewSteerReceivedEvent(text string, images []ImageBlock) Event {
	return newEvent(EventTypeSteerReceived, SteerReceivedEvent{Text: text, Images: images})
}
