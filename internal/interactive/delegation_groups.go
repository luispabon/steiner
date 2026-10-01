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
		messages := append(append([]agent.Message(nil), generation.SummaryPrefix...), generation.Messages...)
		for i := 0; i < len(messages); i++ {
			if messages[i].Role != agent.MessageRoleAssistant {
				continue
			}
			end := i + 1
			for end < len(messages) && messages[end].Role == agent.MessageRoleTool {
				end++
			}
			admissions := pairLegacyAdmissions(messages[i], messages[i+1:end])
			for callIndex, call := range messages[i].ToolCalls {
				if !legacyDelegationTool(call.Name) {
					continue
				}
				if legacyCallGroup(call) == "" || admissions[callIndex] == tool.DelegationAdmissionRejected {
					continue
				}
				name := legacyCallGroup(call)
				names[name] = struct{}{}
			}
			i = end - 1
		}
	}

	out := agent.DelegationGroupLedger{Version: 1, Names: make([]string, 0, len(names))}
	for name := range names {
		out.Names = append(out.Names, name)
	}
	sort.Strings(out.Names)
	return out, nil
}

// pairLegacyAdmissions returns definitive statuses only for equally sized,
// name-compatible per-ID call/result queues in one immediate exchange.
func pairLegacyAdmissions(assistant agent.Message, results []agent.Message) map[int]string {
	callQueues := make(map[string][]int)
	resultQueues := make(map[string][]agent.Message)
	for i, call := range assistant.ToolCalls {
		if call.ID != "" {
			callQueues[call.ID] = append(callQueues[call.ID], i)
		}
	}
	for _, result := range results {
		if result.ToolCallID != "" {
			resultQueues[result.ToolCallID] = append(resultQueues[result.ToolCallID], result)
		}
	}
	statuses := make(map[int]string)
	for id, calls := range callQueues {
		outcomes := resultQueues[id]
		if len(calls) != len(outcomes) {
			continue
		}
		compatible := true
		for i, callIndex := range calls {
			resultName := outcomes[i].Name
			if resultName != "" && resultName != assistant.ToolCalls[callIndex].Name {
				compatible = false
				break
			}
		}
		if !compatible {
			continue
		}
		for i, callIndex := range calls {
			if outcomes[i].DelegationAdmission != nil {
				statuses[callIndex] = outcomes[i].DelegationAdmission.Status
			}
		}
	}
	return statuses
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
