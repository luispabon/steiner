package tui

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
)

const (
	delegationOperationPreviewMaxRunes = 60
)

func (dd *delegationDisplayState) appendOrMergeThinkingEntry(text string, source output.ChunkSource) *delegationTranscriptEntry {
	if last := dd.lastEntry(); last != nil &&
		last.kind == delegationTranscriptEntryThinking &&
		last.source == source {
		last.body += text
		return last
	}
	idx := dd.appendTranscriptEntry(delegationTranscriptEntry{
		kind:   delegationTranscriptEntryThinking,
		body:   text,
		source: source,
	})
	return &dd.entries[idx]
}

func (dd *delegationDisplayState) appendOrMergeAssistantEntry(content string) *delegationTranscriptEntry {
	if last := dd.lastEntry(); last != nil && last.kind == delegationTranscriptEntryAssistant {
		last.body += content
		return last
	}
	idx := dd.appendTranscriptEntry(delegationTranscriptEntry{
		kind: delegationTranscriptEntryAssistant,
		body: content,
	})
	return &dd.entries[idx]
}

func (dd *delegationDisplayState) appendTranscriptEntry(entry delegationTranscriptEntry) int {
	dd.entries = append(dd.entries, entry)
	dd.trimTranscriptEntries()
	return len(dd.entries) - 1
}

func (dd *delegationDisplayState) trimTranscriptEntries() {
	if len(dd.entries) <= delegationTranscriptLimit {
		return
	}
	drop := len(dd.entries) - delegationTranscriptLimit
	dd.entries = append([]delegationTranscriptEntry(nil), dd.entries[drop:]...)
	if len(dd.childToolEntries) == 0 {
		return
	}
	for callID, idx := range dd.childToolEntries {
		idx -= drop
		if idx < 0 || idx >= len(dd.entries) {
			delete(dd.childToolEntries, callID)
			continue
		}
		dd.childToolEntries[callID] = idx
	}
}

func (dd *delegationDisplayState) ensureChildToolEntries() {
	if dd.childToolEntries == nil {
		dd.childToolEntries = make(map[string]int)
	}
}

func (dd *delegationDisplayState) lastEntry() *delegationTranscriptEntry {
	if len(dd.entries) == 0 {
		return nil
	}
	return &dd.entries[len(dd.entries)-1]
}

func (dd *delegationDisplayState) findChildToolEntry(callID string) (int, bool) {
	if callID == "" {
		return 0, false
	}
	if len(dd.childToolEntries) == 0 {
		return 0, false
	}
	idx, ok := dd.childToolEntries[callID]
	if !ok || idx < 0 || idx >= len(dd.entries) {
		return 0, false
	}
	entry := dd.entries[idx]
	if entry.kind != delegationTranscriptEntryTool || entry.callID != callID {
		return 0, false
	}
	return idx, true
}

func formatElapsed(startNano, endNano int64) string {
	ns := endNano - startNano
	if ns < 0 {
		ns = 0
	}
	ms := ns / 1_000_000
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	s := ms / 1000
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm%ds", s/60, s%60)
}

func previewDelegationOperation(tool, args string) string {
	head := strings.TrimSpace(tool)
	tail := normalizeDelegationText(args)
	switch {
	case head == "" && tail == "":
		return ""
	case tail == "":
		return previewDelegationText(head)
	case head == "":
		return previewDelegationText(tail)
	default:
		return previewDelegationText(head + ": " + tail)
	}
}

func previewDelegationText(text string) string {
	normalized := normalizeDelegationText(text)
	runes := []rune(normalized)
	if len(runes) <= delegationOperationPreviewMaxRunes {
		return normalized
	}
	return string(runes[:delegationOperationPreviewMaxRunes-3]) + "..."
}

func normalizeDelegationText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

// extractFollowUpAgentID extracts the agent_id from follow_up tool arguments.
func extractFollowUpAgentID(args map[string]any) string {
	if args == nil {
		return ""
	}
	agentID, ok := args["agent_id"].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(agentID)
}

// extractFollowUpMessage extracts the message from follow_up tool arguments.
func extractFollowUpMessage(args map[string]any) string {
	if args == nil {
		return ""
	}
	msg, ok := args["message"].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(msg)
}

// summarizeFollowUpArgs returns a human-readable summary of follow_up arguments.
func summarizeFollowUpArgs(args map[string]any) string {
	msg := extractFollowUpMessage(args)
	if msg != "" {
		return msg
	}
	return "follow up"
}
