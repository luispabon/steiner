package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/tool"
)

const (
	historyDivider       = "── compacted here ──"
	historyResultOmitted = "[sub-agent result omitted]"
	historyToolOmitted   = "[tool result omitted]"
	// historyToolRecallPointer and historyResultRecallPointer name the recall
	// file that holds an omitted body, so a later reader can fetch it.
	historyToolRecallPointer   = "[tool result omitted: recall %s]"
	historyResultRecallPointer = "[sub-agent result omitted: recall %s]"
	historyStatusOK            = "✓"
	historyStatusError         = "error"
)

// recallPresence reports whether the recall file named name exists as a
// regular file that is not a symlink. A nil recallPresence treats every name as
// missing, which renders the plain omitted markers.
type recallPresence func(name string) bool

// renderHistory renders the source messages of one compaction as archive
// markdown, oldest first. Each rendered message is a block, and blocks are
// separated by one blank line. Tool result bodies, sub-agent result bodies, and
// reasoning are omitted: a body whose recall file recall reports present is
// replaced by a pointer naming that file, and a body without one keeps a plain
// omitted marker. It returns "" when nothing renders. The output is
// deterministic for identical input and recall files.
func renderHistory(source []agent.Message, recall recallPresence) string {
	statuses := toolCallStatuses(source)
	var blocks []string
	for messageIndex, msg := range source {
		if block := renderHistoryMessage(msg, statuses, messageIndex, recall); block != "" {
			blocks = append(blocks, block)
		}
	}
	return strings.Join(blocks, "\n\n")
}

type toolCallPosition struct {
	messageIndex int
	callIndex    int
}

// renderHistoryMessage renders one message as a markdown block. It returns ""
// when the message contributes no body text, so a bare heading is never emitted.
func renderHistoryMessage(msg agent.Message, statuses map[toolCallPosition]string, messageIndex int, recall recallPresence) string {
	var heading string
	var body []string
	switch msg.Role {
	case agent.MessageRoleSummary:
		body = summaryParts(msg.Content)
	case agent.MessageRoleUser:
		heading = historyHeading(msg)
		body = userParts(msg, recall)
	case agent.MessageRoleAssistant:
		heading = historyHeading(msg)
		body = assistantParts(msg, statuses, messageIndex)
	case agent.MessageRoleTool:
		// Tool result bodies are not rendered. A pointer names the recall file
		// that holds the body, or a plain marker stands in when it is missing.
		body = append(body, toolResultMarker(msg, recall))
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
// with archive note lines removed, then the verbatim latest request fenced under
// its own heading.
func summaryParts(content string) []string {
	parts := []string{historyDivider}
	body, request, ok := agent.SplitRequestBlock(content)
	if text := agent.StripHistoryNotes(body); text != "" {
		parts = append(parts, text)
	}
	if ok {
		parts = append(parts, "Latest request (verbatim):", FenceVerbatim(request))
	}
	return parts
}

// FenceVerbatim returns text as a fenced code block. The fence is one backtick
// longer than the longest backtick run in text, and never shorter than three, so
// markdown in the text renders as typed instead of closing the block. The
// opening fence carries no info string.
func FenceVerbatim(text string) string {
	fence := strings.Repeat("`", verbatimFenceLength(text))
	return fence + "\n" + text + "\n" + fence
}

// verbatimFenceLength returns the fence length FenceVerbatim needs: one more
// backtick than the longest run in text, and never fewer than three.
func verbatimFenceLength(text string) int {
	longest, run := 0, 0
	for _, r := range text {
		if r != '`' {
			run = 0
			continue
		}
		run++
		if run > longest {
			longest = run
		}
	}
	return max(longest+1, 3)
}

// userParts renders a user message. Active skill envelopes become one
// "[skill: <name>]" line each. Each sub-agent result envelope becomes its own
// recall pointer when its file exists and "[sub-agent result omitted]"
// otherwise. Remaining user text is kept, except in a sub-agent result batch,
// which shows only the markers.
func userParts(msg agent.Message, recall recallPresence) []string {
	blocks := prompt.SplitMessageBlocks(msg.Content)
	var markers []string
	for _, skill := range blocks.SkillBlocks {
		if skill.State == prompt.SkillBlockActive {
			markers = append(markers, "[skill: "+skill.Name+"]")
		}
	}
	fromResult := msg.Source == agent.MessageSourceSubAgentResult
	for _, raw := range blocks.ResultEnvelopes {
		markers = append(markers, subAgentResultMarker(raw, recall))
	}
	if fromResult && len(blocks.ResultEnvelopes) == 0 {
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

// subAgentResultMarker returns the marker for one sub-agent result envelope: a
// pointer to its recall file when that file exists, otherwise the omitted
// marker. The entry must match the one agent.collectRecallEntries builds, or
// the pointer names a file recall never wrote.
func subAgentResultMarker(raw string, recall recallPresence) string {
	parsed, ok := agent.ParseSubAgentResultEnvelope(raw)
	if !ok {
		return historyResultOmitted
	}
	entry := agent.RecallEntry{Kind: "agent", Tool: parsed.AgentType, CallID: parsed.CallID, Body: parsed.Inner}
	name, ok := recallFileName(entry, recall)
	if !ok {
		return historyResultOmitted
	}
	return fmt.Sprintf(historyResultRecallPointer, name)
}

// toolResultMarker returns the pointer to a tool result body's recall file, or
// the plain omitted marker when there is no recall file. The entry must match
// the one agent.collectRecallEntries builds.
func toolResultMarker(msg agent.Message, recall recallPresence) string {
	entry := agent.RecallEntry{Kind: "tool", Tool: msg.Name, CallID: msg.ToolCallID, Body: msg.Content}
	name, ok := recallFileName(entry, recall)
	if !ok {
		return historyToolOmitted
	}
	return fmt.Sprintf(historyToolRecallPointer, name)
}

// recallFileName returns the recall file name of entry when recall reports it
// present.
func recallFileName(entry agent.RecallEntry, recall recallPresence) (string, bool) {
	if recall == nil {
		return "", false
	}
	name := agent.RecallName(entry)
	return name, recall(name)
}

// historyHasResultBodies reports whether source holds a body compaction
// archives for recall: a tool message or a sub-agent result envelope. Note uses
// it to mention the recall directory before those files exist.
func historyHasResultBodies(source []agent.Message) bool {
	for _, msg := range source {
		switch msg.Role {
		case agent.MessageRoleTool:
			return true
		case agent.MessageRoleUser:
			if len(prompt.SplitMessageBlocks(msg.Content).ResultEnvelopes) > 0 {
				return true
			}
		}
	}
	return false
}

// assistantParts renders assistant text followed by one list line per tool
// call.
func assistantParts(msg agent.Message, statuses map[toolCallPosition]string, messageIndex int) []string {
	var parts []string
	if text := strings.TrimSpace(msg.Content); text != "" {
		parts = append(parts, text)
	}
	var lines []string
	for callIndex, call := range msg.ToolCalls {
		lines = append(lines, toolCallLine(call, statuses, toolCallPosition{messageIndex: messageIndex, callIndex: callIndex}))
	}
	if len(lines) > 0 {
		parts = append(parts, strings.Join(lines, "\n"))
	}
	return parts
}

// toolCallLine renders a tool call as "- **<tool>** <summary> <status>". The
// summary comes from delegation.SummarizeToolArgs and is omitted when empty,
// as is the status when the call has no paired result.
func toolCallLine(call agent.ToolCall, statuses map[toolCallPosition]string, position toolCallPosition) string {
	fields := []string{"- **" + oneLine(call.Name) + "**"}
	if summary := oneLine(delegation.SummarizeToolArgs(call.Name, call.Arguments)); summary != "" {
		fields = append(fields, summary)
	}
	if status := statuses[position]; status != "" {
		fields = append(fields, status)
	}
	return strings.Join(fields, " ")
}

// oneLine collapses s to single-spaced words so a summary never spans lines.
// Control characters, including terminal escape bytes, count as separators.
func oneLine(s string) string {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(clean), " ")
}

// toolCallStatuses pairs each tool result with the oldest preceding unanswered
// call with the same ID. Results without a preceding call are ignored.
func toolCallStatuses(source []agent.Message) map[toolCallPosition]string {
	statuses := make(map[toolCallPosition]string)
	pending := make(map[string][]toolCallPosition)
	for messageIndex, msg := range source {
		if msg.Role == agent.MessageRoleAssistant {
			for callIndex, call := range msg.ToolCalls {
				if call.ID == "" {
					continue
				}
				position := toolCallPosition{messageIndex: messageIndex, callIndex: callIndex}
				pending[call.ID] = append(pending[call.ID], position)
			}
			continue
		}
		if msg.Role != agent.MessageRoleTool || msg.ToolCallID == "" {
			continue
		}
		positions := pending[msg.ToolCallID]
		if len(positions) == 0 {
			continue
		}
		position := positions[0]
		if len(positions) == 1 {
			delete(pending, msg.ToolCallID)
		} else {
			pending[msg.ToolCallID] = positions[1:]
		}
		statuses[position] = historyStatusOK
		if toolResultFailed(msg.Name, msg.Content) {
			statuses[position] = historyStatusError
		}
	}
	return statuses
}

// toolResultFailed reports whether a persisted tool result is a failure. A
// failure envelope (tool.JSONEnvelope with ok false and an error) fails any
// tool. Results of delegation tools also fail when they carry a failed
// projection, matching replayToolFinished in internal/interactive.
func toolResultFailed(name, content string) bool {
	var envelope tool.JSONEnvelope
	if json.Unmarshal([]byte(content), &envelope) == nil && !envelope.OK && envelope.Error != nil {
		return true
	}
	return delegation.IsDelegationTool(strings.ToLower(name)) && delegationResultFailed(content)
}

// delegationResultFailed reports whether a delegation result is a projected
// agent.DelegationResultEnvelope with a failed, cancelled, or lost status and a
// reason. Partial and acknowledgement results are not failures, matching replay.
func delegationResultFailed(content string) bool {
	var projection agent.DelegationResultEnvelope
	if json.Unmarshal([]byte(content), &projection) != nil || projection.Reason == "" {
		return false
	}
	switch projection.Status {
	case "failed", "cancelled", "lost":
		return true
	}
	return false
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
