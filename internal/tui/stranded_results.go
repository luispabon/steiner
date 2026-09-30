package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/output"
)

// strandedBody returns the plain wording of the conversation row for
// unanswered sub-agent results: the headline and an optional second line.
func strandedBody(count int, reason string) (headline, detail string) {
	reason = firstLine(reason)
	noun := strandedNoun(count)
	if reason == "" {
		return fmt.Sprintf("not acted on · %d %s waiting — send a message to continue", count, noun), ""
	}
	verb := "is"
	if count != 1 {
		verb = "are"
	}
	return "not acted on · the orchestrator's turn failed: " + reason,
		fmt.Sprintf("%d %s %s waiting — send a message to continue", count, noun, verb)
}

func strandedNoun(count int) string {
	if count == 1 {
		return "sub-agent result"
	}
	return "sub-agent results"
}

// strandedActivityLabel is the activity-row warning while results are stranded.
func strandedActivityLabel(count int) string {
	return fmt.Sprintf("⚠ %d %s not acted on — send a message to continue", count, strandedNoun(count))
}

// appendSubAgentResultsUnansweredEvent adds the warning row to the transcript.
func (b *contentBuffer) appendSubAgentResultsUnansweredEvent(event output.Event) {
	payload, ok := event.Payload.(output.SubAgentResultsUnansweredEvent)
	if !ok || len(payload.AgentIDs) == 0 {
		return
	}
	b.finishStreaming()
	b.segments = append(b.segments, contentSegment{
		kind:         segmentStrandedResults,
		timestamp:    timeNow(),
		strandedData: &strandedResultsData{count: len(payload.AgentIDs), reason: payload.Reason},
		renderDirty:  true,
	})
}

// strandedResultsData is the payload of a segmentStrandedResults row.
type strandedResultsData struct {
	count  int
	reason string
}

func (b *contentBuffer) renderStrandedResultsSegment(segment contentSegment, width int) string {
	data := segment.strandedData
	if data == nil {
		return ""
	}
	headline, detail := strandedBody(data.count, data.reason)
	warn := b.styles.Warn
	timestamp := b.renderStatusTimestamp(segment, width)
	const prefix = "  "
	bodyWidth := max(1, width-len(prefix)-2)
	var sb strings.Builder
	if timestamp != "" {
		sb.WriteString("\n")
	}
	first := prefix + warn.Render("⚠ "+truncateRunes(headline, bodyWidth))
	if detail == "" {
		sb.WriteString(b.appendRightAlignedTimestamp(first, timestamp, width) + "\n")
		return sb.String()
	}
	sb.WriteString(first + "\n")
	second := prefix + "  " + b.styles.FgDim.Render(truncateRunes(detail, max(1, bodyWidth-lipgloss.Width(timestamp)-1)))
	sb.WriteString(b.appendRightAlignedTimestamp(second, timestamp, width) + "\n")
	return sb.String()
}

// applyStrandedResults records the stranded count so the activity row can warn
// until the user sends a message or the driver resumes generating.
func (m *Model) applyStrandedResults(payload output.SubAgentResultsUnansweredEvent) {
	m.strandedResults = len(payload.AgentIDs)
}

// clearStrandedResults drops the stranded-results warning.
func (m *Model) clearStrandedResults() {
	m.strandedResults = 0
}

// clearReplayActivity drops activity left over from a replayed session: a
// loaded transcript has no live tool calls, so a "running tool" footer is stale.
func (m *Model) clearReplayActivity() {
	m.activity = m.activity.clear()
	m.convLabelShown = false
}
