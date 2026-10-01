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
	for _, generation := range lineage.Generations {
		for name := range legacyGenerationGroups(generation) {
			names[name] = struct{}{}
		}
	}

	out := agent.DelegationGroupLedger{Version: 1, Names: make([]string, 0, len(names))}
	for name := range names {
		out.Names = append(out.Names, name)
	}
	sort.Strings(out.Names)
	return out, nil
}

func legacyGenerationGroups(generation agent.ConversationGeneration) map[string]struct{} {
	admissions := make(map[string][]string)
	messages := [][]agent.Message{generation.SummaryPrefix, generation.Messages}
	for _, group := range messages {
		for _, message := range group {
			if message.Role == agent.MessageRoleTool && message.ToolCallID != "" && message.DelegationAdmission != nil {
				admissions[message.ToolCallID] = append(admissions[message.ToolCallID], message.DelegationAdmission.Status)
			}
		}
	}
	seenCalls := make(map[string]map[string]struct{})
	names := make(map[string]struct{})
	for _, group := range messages {
		for _, message := range group {
			if message.Role != agent.MessageRoleAssistant {
				continue
			}
			for _, call := range message.ToolCalls {
				if !legacyDelegationTool(call.Name) {
					continue
				}
				name := legacyCallGroup(call)
				if name == "" || duplicateLegacyCall(seenCalls, call.ID, name) || allAdmissionsRejected(admissions[call.ID]) {
					continue
				}
				names[name] = struct{}{}
			}
		}
	}
	return names
}

func duplicateLegacyCall(seen map[string]map[string]struct{}, callID, name string) bool {
	if callID == "" {
		return false
	}
	if seen[callID] == nil {
		seen[callID] = make(map[string]struct{})
	}
	if _, exists := seen[callID][name]; exists {
		return true
	}
	seen[callID][name] = struct{}{}
	return false
}

func allAdmissionsRejected(statuses []string) bool {
	if len(statuses) == 0 {
		return false
	}
	for _, status := range statuses {
		if status != tool.DelegationAdmissionRejected {
			return false
		}
	}
	return true
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
