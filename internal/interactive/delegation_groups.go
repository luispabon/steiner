package interactive

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/tool"
)

// resolveDelegationGroups returns the saved ledger when present, or conservatively
// migrates group names from legacy delegation calls in the saved lineage.
func resolveDelegationGroups(saved *agent.DelegationGroupLedger, lineage agent.ConversationLineage) (agent.DelegationGroupLedger, error) {
	if saved != nil {
		if saved.Version != 1 {
			return agent.DelegationGroupLedger{}, fmt.Errorf("resolve delegation groups: unsupported delegation group ledger version %d", saved.Version)
		}
		cloned := saved.Clone()
		if cloned.Names == nil {
			cloned.Names = []string{}
		}
		return cloned, nil
	}

	names := make(map[string]struct{})
	var previous []agent.Message
	previousEvidence := make(map[string]string)
	for _, generation := range lineage.Generations {
		current := generationMessages(generation)
		evidence := lineageCallEvidence(current, previous, previousEvidence)
		occurrences := make(map[string]int)
		for _, message := range current {
			if message.Role != agent.MessageRoleAssistant {
				continue
			}
			for _, call := range message.ToolCalls {
				if !legacyDelegationTool(call.Name) {
					continue
				}
				key := legacyCallKey(call)
				if key == "" {
					continue
				}
				occurrences[key]++
				key = fmt.Sprintf("%s\x00%d", key, occurrences[key])
				if evidence[key] == tool.DelegationAdmissionRejected {
					continue
				}
				if name := legacyCallGroup(call); name != "" {
					names[name] = struct{}{}
				}
			}
		}
		previous, previousEvidence = current, evidence
	}

	out := agent.DelegationGroupLedger{Version: 1, Names: make([]string, 0, len(names))}
	for name := range names {
		out.Names = append(out.Names, name)
	}
	sort.Strings(out.Names)
	return out, nil
}

func generationMessages(generation agent.ConversationGeneration) []agent.Message {
	messages := make([]agent.Message, 0, len(generation.SummaryPrefix)+len(generation.Messages))
	messages = append(messages, generation.SummaryPrefix...)
	return append(messages, generation.Messages...)
}

// lineageCallEvidence pairs outcomes with calls in occurrence order by call ID.
// Exact fingerprint and ordinal matches inherit outcomes across retained copies.
func lineageCallEvidence(messages, previous []agent.Message, previousEvidence map[string]string) map[string]string {
	calls, callKeys := legacyCallOccurrences(messages)
	previousKeys := make(map[string]string)
	_, keys := legacyCallOccurrences(previous)
	for _, key := range keys {
		previousKeys[key] = key
	}
	out := make(map[string]string)
	positions := make(map[string]int)
	for _, message := range messages {
		if message.Role != agent.MessageRoleTool || message.ToolCallID == "" || message.DelegationAdmission == nil {
			continue
		}
		queue := calls[message.ToolCallID]
		position := positions[message.ToolCallID]
		if position >= len(queue) {
			continue
		}
		key := queue[position]
		positions[message.ToolCallID] = position + 1
		status := message.DelegationAdmission.Status
		if existing, ok := out[key]; ok && existing != status {
			out[key] = "ambiguous"
		} else {
			out[key] = status
		}
	}
	for _, key := range callKeys {
		if _, retained := previousKeys[key]; !retained {
			continue
		}
		prior, hasPrior := previousEvidence[key]
		current, hasCurrent := out[key]
		if hasPrior && (!hasCurrent || current == tool.DelegationAdmissionRejected) {
			out[key] = prior
		}
	}
	return out
}

func legacyCallOccurrences(messages []agent.Message) (map[string][]string, []string) {
	calls := make(map[string][]string)
	counts := make(map[string]int)
	var ordered []string
	for _, message := range messages {
		if message.Role != agent.MessageRoleAssistant {
			continue
		}
		for _, call := range message.ToolCalls {
			if !legacyDelegationTool(call.Name) || call.ID == "" {
				continue
			}
			fingerprint := call.Name + "\x00" + call.ID + "\x00" + callArguments(call)
			counts[fingerprint]++
			key := fmt.Sprintf("%s\x00%d", fingerprint, counts[fingerprint])
			calls[call.ID] = append(calls[call.ID], key)
			ordered = append(ordered, key)
		}
	}
	return calls, ordered
}

func legacyCallKey(call agent.ToolCall) string {
	group := legacyCallGroup(call)
	if group == "" {
		return ""
	}
	return call.Name + "\x00" + call.ID + "\x00" + callArguments(call)
}

func callArguments(call agent.ToolCall) string {
	if call.RawArguments != "" {
		return strings.TrimSpace(call.RawArguments)
	}
	encoded, _ := json.Marshal(call.Arguments)
	return string(encoded)
}

func legacyDelegationTool(name string) bool {
	switch name {
	case "sub_agent", "follow_up":
		return true
	default:
		return false
	}
}

func legacyCallGroup(call agent.ToolCall) string {
	if value, ok := call.Arguments["group"].(string); ok {
		if group := strings.TrimSpace(value); group != "" {
			return group
		}
	}
	var args map[string]any
	if json.Unmarshal([]byte(call.RawArguments), &args) == nil {
		if value, ok := args["group"].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
