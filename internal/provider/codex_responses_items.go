package provider

import (
	"fmt"
	"sort"
	"strings"
)

func (state *responsesStreamState) currentCandidate(kind, id string, outputIndex *int, callID string) int {
	if state.current == nil {
		return -1
	}
	i := *state.current
	if i < 0 || i >= len(state.ledger) {
		return -1
	}
	e := state.ledger[i]
	if e.kind != kind {
		return -1
	}
	if id == "" && outputIndex == nil && callID == "" {
		return i
	}
	if kind == "message" && e.itemID == "" && e.outputIndex == nil {
		return i
	}
	return -1
}

func resolveAliasMatches(state *responsesStreamState, id string, outputIndex *int, callID string) ([]int, error) {
	matches := make([]int, 0, 1)
	for i := range state.ledger {
		e := &state.ledger[i]
		if (id != "" && e.itemID == id) || (outputIndex != nil && e.outputIndex != nil && *outputIndex == *e.outputIndex) || (callID != "" && e.callID == callID) {
			matches = append(matches, i)
		}
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("conflicting Codex item aliases resolve to separate ledger entries")
	}
	return matches, nil
}

// completedResponsesItems creates an authoritative ledger from a nonempty final output.
func completedResponsesItems(output []responsesItem, streamed []responsesLedgerEntry) ([]responsesLedgerEntry, error) {
	result := make([]responsesLedgerEntry, 0, len(output))
	used := make([]bool, len(streamed))
	for outputIndex, item := range output {
		var entry responsesLedgerEntry
		switch item.Type {
		case "message":
			entry = responsesLedgerEntry{kind: "message", itemID: item.ID, phase: item.Phase, parts: make(map[int]string)}
			for i, part := range item.Content {
				if part.Type == "output_text" || part.Type == "text" {
					entry.parts[i] = part.Text
					entry.partOrder = append(entry.partOrder, i)
				}
			}
			if entry.phase == "" {
				entry.phase = streamedPhase(streamed, used, item.ID, outputIndex)
			}
		case "function_call":
			call, err := responsesToolCall(item)
			if err != nil {
				return nil, err
			}
			entry = responsesLedgerEntry{kind: "function_call", itemID: item.ID, callID: item.CallID, call: &call, completed: true}
			if entry.callID == "" {
				entry.callID = call.ID
			}
		case "reasoning":
			continue
		default:
			continue
		}
		idx := outputIndex
		entry.outputIndex = &idx
		result = append(result, entry)
	}
	return result, nil
}

func streamedPhase(streamed []responsesLedgerEntry, used []bool, id string, index int) string {
	if id != "" {
		found := -1
		for i, entry := range streamed {
			if entry.kind == "message" && entry.itemID == id {
				if found >= 0 {
					return ""
				}
				found = i
			}
		}
		if found >= 0 {
			used[found] = true
			return streamed[found].phase
		}
	}
	for i, entry := range streamed {
		if !used[i] && entry.kind == "message" && entry.outputIndex != nil && *entry.outputIndex == index && (id == "" || entry.itemID == "" || entry.itemID == id) {
			used[i] = true
			return entry.phase
		}
	}
	return ""
}

func (state *responsesStreamState) projected() ([]CodexMessageBlock, []ToolCall, string) {
	order := make([]int, len(state.ledger))
	for i := range order {
		order[i] = i
	}
	indexedPositions := make([]int, 0, len(order))
	indexedEntries := make([]int, 0, len(order))
	for position, entryIndex := range order {
		if state.ledger[entryIndex].outputIndex != nil {
			indexedPositions = append(indexedPositions, position)
			indexedEntries = append(indexedEntries, entryIndex)
		}
	}
	sort.SliceStable(indexedEntries, func(i, j int) bool {
		return *state.ledger[indexedEntries[i]].outputIndex < *state.ledger[indexedEntries[j]].outputIndex
	})
	for i, position := range indexedPositions {
		order[position] = indexedEntries[i]
	}
	blocks := make([]CodexMessageBlock, 0, len(order))
	calls := make([]ToolCall, 0)
	var content strings.Builder
	for _, i := range order {
		entry := state.ledger[i]
		if entry.kind == "message" {
			var text strings.Builder
			parts := append([]int(nil), entry.partOrder...)
			sort.Ints(parts)
			for _, part := range parts {
				text.WriteString(entry.parts[part])
			}
			block := CodexMessageBlock{Kind: "message", Phase: entry.phase, Text: text.String()}
			blocks = append(blocks, block)
			content.WriteString(block.Text)
		} else if entry.kind == "function_call" && entry.completed && entry.call != nil {
			blocks = append(blocks, CodexMessageBlock{Kind: "function_call", CallID: entry.call.ID})
			calls = append(calls, *entry.call)
		}
	}
	return blocks, calls, content.String()
}
