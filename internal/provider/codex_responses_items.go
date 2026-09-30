package provider

import "strings"

// completedResponsesItems builds the authoritative ordered item ledger and
// carries streamed phase metadata forward only through item identity aliases.
//
//nolint:gocyclo // Build ordered message/call blocks while matching only explicit identities.
func completedResponsesItems(output []responsesItem, streamed []CodexMessageBlock, ids []string, indexes []*int) ([]CodexMessageBlock, []ToolCall, string, error) {
	blocks := make([]CodexMessageBlock, 0, len(output))
	calls := make([]ToolCall, 0)
	var content strings.Builder
	used := make([]bool, len(streamed))
	streamedCalls := make([]ToolCall, 0)
	for _, block := range streamed {
		if block.Kind == "function_call" {
			streamedCalls = append(streamedCalls, ToolCall{ID: block.CallID})
		}
	}
	usedCalls := make([]bool, len(streamedCalls))
	callPosition := 0
	for outputIndex, item := range output {
		switch item.Type {
		case "message":
			var text strings.Builder
			for _, part := range item.Content {
				if part.Type == "output_text" || part.Type == "text" {
					text.WriteString(part.Text)
				}
			}
			block := CodexMessageBlock{Kind: "message", Phase: item.Phase, Text: text.String()}
			for i, old := range streamed {
				if used[i] || old.Kind != "message" || i >= len(ids) {
					continue
				}
				matched := item.ID != "" && ids[i] == item.ID
				if !matched && i < len(indexes) && indexes[i] != nil && *indexes[i] == outputIndex {
					matched = true
				}
				if matched {
					used[i] = true
					if block.Phase == "" {
						block.Phase = old.Phase
					}
					break
				}
			}
			blocks = append(blocks, block)
			content.WriteString(block.Text)
		case "function_call":
			call, err := responsesToolCall(item)
			if err != nil {
				return nil, nil, "", err
			}
			for callPosition < len(streamedCalls) && usedCalls[callPosition] {
				callPosition++
			}
			if callPosition < len(streamedCalls) {
				old := streamedCalls[callPosition]
				if call.ID == "" || old.ID == "" || call.ID == old.ID {
					usedCalls[callPosition] = true
					callPosition++
				}
			}
			calls = append(calls, call)
			blocks = append(blocks, CodexMessageBlock{Kind: "function_call", CallID: call.ID})
		case "reasoning":
			// Reasoning remains governed by the existing reasoning policy.
		}
	}
	return blocks, calls, content.String(), nil
}
