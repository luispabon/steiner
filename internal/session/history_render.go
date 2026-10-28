package session

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/tool"
)

const (
	historyDivider       = "── compacted here ──"
	historyResultOmitted = "[sub-agent result omitted]"
	historyStatusOK      = "✓"
	historyStatusError   = "error"
	historyDisplayFile   = "display_file"
)

// RenderHistory renders the source messages of one compaction as archive
// markdown, oldest first. Each rendered message is a block, and blocks are
// separated by one blank line. Tool result bodies, sub-agent result bodies,
// reasoning, and display_file calls are omitted. It returns "" when nothing
// renders. The output is deterministic for identical input.
func RenderHistory(source []agent.Message) string {
	statuses := toolCallStatuses(source)
	var blocks []string
	for _, msg := range source {
		if block := renderHistoryMessage(msg, statuses); block != "" {
			blocks = append(blocks, block)
		}
	}
	return strings.Join(blocks, "\n\n")
}

// renderHistoryMessage renders one message as a markdown block. It returns ""
// when the message contributes no body text, so a bare heading is never emitted.
func renderHistoryMessage(msg agent.Message, statuses map[string]string) string {
	var heading string
	var body []string
	switch msg.Role {
	case agent.MessageRoleSummary:
		body = summaryParts(msg.Content)
	case agent.MessageRoleUser:
		heading = historyHeading(msg)
		body = userParts(msg)
	case agent.MessageRoleAssistant:
		heading = historyHeading(msg)
		body = assistantParts(msg, statuses)
	case agent.MessageRoleTool:
		// Tool results are not rendered. Only their images are kept.
	default:
		heading = historyHeading(msg)
		if text := strings.TrimSpace(msg.Content); text != "" {
			body = append(body, text)
		}
	}
	if images := imagePlaceholders(msg.Images); images != "" {
		body = append(body, images)
	}
	if len(body) == 0 {
		return ""
	}
	if heading != "" {
		body = append([]string{heading}, body...)
	}
	return strings.Join(body, "\n\n")
}

// historyHeading returns the "### <role> · turn N" heading, omitting the turn
// for messages with no turn number.
func historyHeading(msg agent.Message) string {
	if msg.Turn <= 0 {
		return "### " + string(msg.Role)
	}
	return fmt.Sprintf("### %s · turn %d", msg.Role, msg.Turn)
}

// summaryParts renders a compaction summary as a divider followed by its text
// with archive note lines removed.
func summaryParts(content string) []string {
	parts := []string{historyDivider}
	if text := stripHistoryNotes(content); text != "" {
		parts = append(parts, text)
	}
	return parts
}

// stripHistoryNotes removes every line whose trimmed form starts with
// agent.HistoryNoteMarker and trims the remaining text.
func stripHistoryNotes(content string) string {
	var kept []string
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), agent.HistoryNoteMarker) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// userParts renders a user message. Active skill envelopes become one
// "[skill: <name>]" line each, and sub-agent result envelopes become one
// "[sub-agent result omitted]" line. Remaining user text is kept, except in a
// sub-agent result batch, which shows only the marker.
func userParts(msg agent.Message) []string {
	blocks := prompt.SplitMessageBlocks(msg.Content)
	var markers []string
	for _, skill := range blocks.SkillBlocks {
		if skill.State == prompt.SkillBlockActive {
			markers = append(markers, "[skill: "+skill.Name+"]")
		}
	}
	fromResult := msg.Source == agent.MessageSourceSubAgentResult
	if fromResult || len(blocks.ResultEnvelopes) > 0 {
		markers = append(markers, historyResultOmitted)
	}
	var parts []string
	if len(markers) > 0 {
		parts = append(parts, strings.Join(markers, "\n"))
	}
	if text := strings.TrimSpace(blocks.Rest); text != "" && !fromResult {
		parts = append(parts, text)
	}
	return parts
}

// assistantParts renders assistant text followed by one list line per tool
// call, skipping display_file calls.
func assistantParts(msg agent.Message, statuses map[string]string) []string {
	var parts []string
	if text := strings.TrimSpace(msg.Content); text != "" {
		parts = append(parts, text)
	}
	var lines []string
	for _, call := range msg.ToolCalls {
		if strings.EqualFold(call.Name, historyDisplayFile) {
			continue
		}
		lines = append(lines, toolCallLine(call, statuses))
	}
	if len(lines) > 0 {
		parts = append(parts, strings.Join(lines, "\n"))
	}
	return parts
}

// toolCallLine renders a tool call as "- **<tool>** <summary> <status>". The
// summary comes from delegation.SummarizeToolArgs and is omitted when empty,
// as is the status when the call has no paired result.
func toolCallLine(call agent.ToolCall, statuses map[string]string) string {
	fields := []string{"- **" + call.Name + "**"}
	if summary := oneLine(delegation.SummarizeToolArgs(call.Name, call.Arguments)); summary != "" {
		fields = append(fields, summary)
	}
	if status := statuses[call.ID]; status != "" {
		fields = append(fields, status)
	}
	return strings.Join(fields, " ")
}

// oneLine collapses s to single-spaced words so a summary never spans lines.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// toolCallStatuses maps each tool call ID with a result in source to its status
// mark. The first result seen for an ID wins.
func toolCallStatuses(source []agent.Message) map[string]string {
	statuses := make(map[string]string)
	for _, msg := range source {
		if msg.Role != agent.MessageRoleTool || msg.ToolCallID == "" {
			continue
		}
		if _, seen := statuses[msg.ToolCallID]; seen {
			continue
		}
		statuses[msg.ToolCallID] = historyStatusOK
		if toolResultFailed(msg.Content) {
			statuses[msg.ToolCallID] = historyStatusError
		}
	}
	return statuses
}

// toolResultFailed reports whether a persisted tool result is a failure
// envelope. Results that are not envelopes, such as plain result JSON, count
// as successful, matching the replay decoder in internal/interactive.
func toolResultFailed(content string) bool {
	var envelope tool.JSONEnvelope
	return json.Unmarshal([]byte(content), &envelope) == nil && !envelope.OK && envelope.Error != nil
}

// imagePlaceholders renders one "[image: <id>]" line per image, or "" when
// there are none.
func imagePlaceholders(images []agent.ImageBlock) string {
	lines := make([]string, 0, len(images))
	for _, img := range images {
		lines = append(lines, "[image: "+img.ID+"]")
	}
	return strings.Join(lines, "\n")
}
