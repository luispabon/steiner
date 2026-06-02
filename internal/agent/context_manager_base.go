package agent

import (
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/tool"
)

type baseContextManager struct {
	fileTracker           FileTracker
	readAnnotations       bool
	annotationsConfigured bool
	cachedPreamble        struct {
		content           string
		override          string
		delegationEnabled bool
		cavemanMode       bool
	}
	minVisibleTurn int
	events         output.EventSink
}

func (b *baseContextManager) CachedSystemPreamble(override string, delegationEnabled bool, cavemanMode bool) string {
	if b.cachedPreamble.content == "" ||
		b.cachedPreamble.override != override ||
		b.cachedPreamble.delegationEnabled != delegationEnabled ||
		b.cachedPreamble.cavemanMode != cavemanMode {
		b.cachedPreamble.content = prompt.SystemPreamble(override, delegationEnabled, cavemanMode).Content
		b.cachedPreamble.override = override
		b.cachedPreamble.delegationEnabled = delegationEnabled
		b.cachedPreamble.cavemanMode = cavemanMode
	}
	return b.cachedPreamble.content
}

func (b *baseContextManager) RecordMutation(path string) {
	b.fileTracker.RecordMutation(path)
}

func (b *baseContextManager) SetEventSink(sink output.EventSink) {
	b.events = sink
}

func (b *baseContextManager) observeToolResult(turn int, toolName string, _ map[string]any, content string) string {
	shaped := tool.ShapeIngestedToolResult(toolName, content)
	if strings.EqualFold(strings.TrimSpace(toolName), "read") {
		return b.observeReadToolResult(turn, shaped)
	}
	return shaped
}

func (b *baseContextManager) observeReadToolResult(turn int, shaped string) string {
	result, _ := parseReadResult(shaped)
	next, observation := b.observeReadToolResultWithObservation(turn, shaped)
	b.emitFileAnnotationDiagnostics(turn, result, observation, shaped, next)
	return next
}

func (b *baseContextManager) observeReadToolResultWithObservation(turn int, shaped string) (string, fileObservation) {
	next, observation := b.fileTracker.ObserveRead(turn, shaped, b.annotationsEnabled())
	if observation.Action == "annotated" {
		if observation.PreviousRead.LastTurn <= 0 {
			next = shaped
			observation.Action = "full"
			observation.Reason = "previous read turn not safely referenceable"
		} else if b.minVisibleTurn > 0 && observation.PreviousRead.LastTurn < b.minVisibleTurn {
			next = shaped
			observation.Action = "full"
			observation.Reason = "previous read no longer visible in context"
		}
	}
	return next, observation
}

func (b *baseContextManager) annotationsEnabled() bool {
	return b.readAnnotations
}

func (b *baseContextManager) emitFileAnnotationDiagnostics(turn int, result readResult, observation fileObservation, original, shaped string) {
	if b.events == nil {
		return
	}
	if strings.TrimSpace(result.Path) == "" {
		return
	}
	if strings.TrimSpace(observation.Action) == "" {
		return
	}
	if observation.Reason == "first read" {
		return
	}
	if observation.Reason == "annotations disabled" {
		return
	}

	notes := []string{result.rangeSummary()}
	notes[0] = "range=" + notes[0]
	if strings.TrimSpace(original) != strings.TrimSpace(shaped) {
		notes = append(notes, "annotation produced")
	}
	notes = append(notes, observation.Notes...)

	previousTurn := 0
	if observation.HadPrevious {
		previousTurn = observation.PreviousRead.LastTurn
	}
	emitEvent(b.events, output.NewFileAnnotationEvent(
		turn,
		strings.TrimSpace(result.Path),
		observation.Action,
		observation.Reason,
		previousTurn,
		notes...,
	))
}

func (b *baseContextManager) normalizeIngestedMessages(turn int, messages []Message, ingestor *ContextStateManager) []Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]Message, len(messages))
	for i, message := range messages {
		out[i] = b.normalizeIngestedMessage(turn, message, ingestor)
	}
	return out
}

func (b *baseContextManager) normalizeIngestedMessage(turn int, message Message, ingestor *ContextStateManager) Message {
	if message.Role != MessageRoleTool {
		return message
	}
	if strings.TrimSpace(message.Content) == "" {
		return message
	}
	messageTurn := message.Turn
	if messageTurn <= 0 {
		messageTurn = turn
	}
	if messageTurn <= 0 {
		return message
	}
	if ingestor != nil {
		message.Content = ingestor.ObserveToolResult(messageTurn, message.Name, nil, message.Content)
		return message
	}
	message.Content = b.observeToolResult(messageTurn, message.Name, nil, message.Content)
	return message
}
