package interactive

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/tool"
)

// resolveDelegationGroups returns the saved ledger when present, or conservatively
// migrates group names from legacy delegation calls in the saved lineage.
func resolveDelegationGroups(saved *agent.DelegationGroupLedger, lineage agent.ConversationLineage) (agent.DelegationGroupLedger, error) {
	if saved != nil {
		if saved.Version != agent.DelegationGroupLedgerVersion {
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
				if !delegation.IsDelegationTool(call.Name) {
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

	out := agent.DelegationGroupLedger{Version: agent.DelegationGroupLedgerVersion, Names: make([]string, 0, len(names))}
	for name := range names {
		out.Names = append(out.Names, name)
	}
	sort.Strings(out.Names)
	return out, nil
}

// pairLegacyAdmissions returns definitive statuses only for call IDs whose
// calls and results balance and whose names are compatible, within one
// immediate exchange (the assistant message and its tool-result run).
func pairLegacyAdmissions(assistant agent.Message, results []agent.Message) map[int]string {
	span := append([]agent.Message{assistant}, results...)
	pairing := pairToolResults(span)
	bad := pairing.unbalanced
	for _, pair := range pairing.pairs {
		if !pair.nameCompatible {
			bad[pair.callID] = true
		}
	}
	statuses := make(map[int]string)
	for _, pair := range pairing.pairs {
		if bad[pair.callID] {
			continue
		}
		if admission := span[pair.resultIndex].DelegationAdmission; admission != nil {
			statuses[pair.call.callIndex] = admission.Status
		}
	}
	return statuses
}

func legacyCallGroup(call agent.ToolCall) string {
	rawGroup, _ := call.Arguments["group"].(string)
	if group := agent.NormalizeDelegationGroup(rawGroup); group != "" {
		return group
	}
	var args map[string]any
	if json.Unmarshal([]byte(call.RawArguments), &args) == nil {
		if value, ok := args["group"].(string); ok {
			return agent.NormalizeDelegationGroup(value)
		}
	}
	return ""
}
