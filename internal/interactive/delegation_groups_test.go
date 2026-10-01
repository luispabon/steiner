package interactive

import (
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/tool"
)

func TestResolveDelegationGroups(t *testing.T) {
	tests := []struct {
		name    string
		saved   *agent.DelegationGroupLedger
		want    []string
		wantErr bool
	}{
		{name: "saved ledger is authoritative", saved: &agent.DelegationGroupLedger{Version: 1, Names: []string{" z ", "a", "a"}}, want: []string{"a", "z"}},
		{name: "saved ledger canonicalized", saved: &agent.DelegationGroupLedger{Version: 1, Names: []string{"z", "a"}}, want: []string{"a", "z"}},
		{name: "explicit empty", saved: &agent.DelegationGroupLedger{Version: 1}, want: []string{}},
		{name: "unsupported saved version", saved: &agent.DelegationGroupLedger{Version: 9}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lineage := agent.ConversationLineage{Generations: []agent.ConversationGeneration{{Messages: []agent.Message{{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{Name: "sub_agent", Arguments: map[string]any{"group": "legacy"}}}}}}}}
			got, err := resolveDelegationGroups(tt.saved, lineage)
			if (err != nil) != tt.wantErr {
				t.Fatalf("resolveDelegationGroups error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && !reflect.DeepEqual(got.Names, tt.want) {
				t.Fatalf("names = %#v, want %#v", got.Names, tt.want)
			}
			if err == nil && got.Version != 1 {
				t.Fatalf("version = %d, want 1", got.Version)
			}
			if err == nil && tt.saved != nil && len(got.Names) > 0 {
				got.Names[0] = "changed"
				if tt.saved.Names[0] == "changed" {
					t.Fatal("resolved ledger aliases saved names")
				}
			}
		})
	}
}

func TestLegacyDelegationGroupsAcrossLineage(t *testing.T) {
	call := func(id, name string, args map[string]any, raw string) agent.Message {
		return agent.Message{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: id, Name: name, Arguments: args, RawArguments: raw}}}
	}
	outcome := func(id, status string) agent.Message {
		return agent.Message{Role: agent.MessageRoleTool, ToolCallID: id, DelegationAdmission: &tool.DelegationAdmission{Status: status}}
	}
	lineage := agent.ConversationLineage{Generations: []agent.ConversationGeneration{
		{SummaryPrefix: []agent.Message{call("prefix", "sub_agent", nil, `{"group":" prefix "}`), outcome("prefix", tool.DelegationAdmissionAccepted)}, Messages: []agent.Message{call("raw", "follow_up", nil, `{"group":"raw"}`)}},
		{SummaryPrefix: []agent.Message{call("duplicate", "sub_agent", map[string]any{"group": "retained"}, ""), outcome("duplicate", tool.DelegationAdmissionRejected)}, Messages: []agent.Message{
			call("mixed", "sub_agent", map[string]any{"group": "shared"}, ""), outcome("mixed", tool.DelegationAdmissionAccepted),
			call("mixed", "sub_agent", map[string]any{"group": "shared"}, ""), outcome("mixed", tool.DelegationAdmissionRejected),
			call("unknown", "follow_up", map[string]any{"group": "unknown"}, ""),
			call("", "sub_agent", map[string]any{"group": "unresolved"}, ""),
			call("not-group", "sub_agent", nil, `{"message":"mentions prose group only"}`),
			call("other", "ordinary_tool", map[string]any{"group": "ignored"}, ""),
		}},
	}}
	got, err := resolveDelegationGroups(nil, lineage)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"prefix", "raw", "shared", "unknown", "unresolved"}
	if !reflect.DeepEqual(got.Names, want) {
		t.Fatalf("names = %#v, want %#v", got.Names, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("migrated ledger invalid: %v", err)
	}
}

func TestLegacyDelegationGroupsRequireUnambiguousLocalPairing(t *testing.T) {
	call := func(id, _ string, arguments map[string]any, raw string) agent.ToolCall {
		return agent.ToolCall{ID: id, Name: "sub_agent", Arguments: arguments, RawArguments: raw}
	}
	result := func(id, name, status string) agent.Message {
		message := agent.Message{Role: agent.MessageRoleTool, ToolCallID: id, Name: name}
		if status != "" {
			message.DelegationAdmission = &tool.DelegationAdmission{Status: status}
		}
		return message
	}
	tests := []struct {
		name  string
		calls []agent.ToolCall
		tools []agent.Message
		want  []string
	}{
		{
			name:  "identical new same-id call remains unresolved",
			calls: []agent.ToolCall{call("same", "g", map[string]any{"group": "g", "objective": "same"}, ""), call("same", "g", map[string]any{"group": "g", "objective": "same"}, "")},
			tools: []agent.Message{result("same", "", tool.DelegationAdmissionRejected)}, want: []string{"g"},
		},
		{
			name:  "same raw but decoded arguments differ",
			calls: []agent.ToolCall{call("same", "g", map[string]any{"group": "g"}, `{"group":"g"}`), call("same", "new", map[string]any{"group": "new"}, `{"group":"new"}`)},
			tools: []agent.Message{result("same", "", tool.DelegationAdmissionRejected)}, want: []string{"g", "new"},
		},
		{
			name:  "unknown result consumes first position",
			calls: []agent.ToolCall{call("same", "unknown", map[string]any{"group": "unknown"}, ""), call("same", "rejected", map[string]any{"group": "rejected"}, "")},
			tools: []agent.Message{result("same", "", "unknown-status"), result("same", "", tool.DelegationAdmissionRejected)}, want: []string{"unknown"},
		},
		{
			name:  "extra accepted result makes queue ambiguous",
			calls: []agent.ToolCall{call("same", "g", map[string]any{"group": "g"}, "")},
			tools: []agent.Message{result("same", "", tool.DelegationAdmissionRejected), result("same", "", tool.DelegationAdmissionAccepted)}, want: []string{"g"},
		},
		{
			name:  "different groups with reused id are both ambiguous",
			calls: []agent.ToolCall{call("same", "first", map[string]any{"group": "first"}, ""), call("same", "second", map[string]any{"group": "second"}, "")},
			tools: []agent.Message{result("same", "", tool.DelegationAdmissionRejected)}, want: []string{"first", "second"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := []agent.Message{{Role: agent.MessageRoleAssistant, ToolCalls: tt.calls}}
			messages = append(messages, tt.tools...)
			got, err := resolveDelegationGroups(nil, agent.ConversationLineage{Generations: []agent.ConversationGeneration{{Messages: messages}}})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Names, tt.want) {
				t.Fatalf("names = %#v, want %#v", got.Names, tt.want)
			}
		})
	}
}

func TestLegacyDelegationGroupsDoNotTransferEvidenceAcrossGenerations(t *testing.T) {
	call := agent.Message{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "retained", Name: "sub_agent", Arguments: map[string]any{"group": "g"}}}}
	rejected := agent.Message{Role: agent.MessageRoleTool, ToolCallID: "retained", DelegationAdmission: &tool.DelegationAdmission{Status: tool.DelegationAdmissionRejected}}
	lineage := agent.ConversationLineage{Generations: []agent.ConversationGeneration{{
		Messages: []agent.Message{call, rejected},
	}, {
		SummaryPrefix: []agent.Message{call},
	}}}
	got, err := resolveDelegationGroups(nil, lineage)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Names, []string{"g"}) {
		t.Fatalf("call-only summary prefix names = %#v, want [g]", got.Names)
	}
}

func TestLegacyDelegationGroupsExcludeCompleteRejectedExchanges(t *testing.T) {
	call := agent.Message{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "rejected", Name: "sub_agent", Arguments: map[string]any{"group": "rejected"}}}}
	rejected := agent.Message{Role: agent.MessageRoleTool, ToolCallID: "rejected", Name: "sub_agent", DelegationAdmission: &tool.DelegationAdmission{Status: tool.DelegationAdmissionRejected}}
	lineage := agent.ConversationLineage{Generations: []agent.ConversationGeneration{
		{Messages: []agent.Message{call, rejected}},
		{Messages: []agent.Message{call, rejected}},
	}}
	got, err := resolveDelegationGroups(nil, lineage)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Names) != 0 {
		t.Fatalf("complete rejected exchange names = %#v, want empty", got.Names)
	}

	acceptedCall := agent.Message{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: "later", Name: "sub_agent", Arguments: map[string]any{"group": "later"}}}}
	acceptedResult := agent.Message{Role: agent.MessageRoleTool, ToolCallID: "later", Name: "sub_agent", DelegationAdmission: &tool.DelegationAdmission{Status: tool.DelegationAdmissionAccepted}}
	lineage.Generations[1].Messages = append(lineage.Generations[1].Messages, acceptedCall, acceptedResult)
	got, err = resolveDelegationGroups(nil, lineage)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Names, []string{"later"}) {
		t.Fatalf("names after later accepted call = %#v, want [later]", got.Names)
	}
}

func TestLegacyDelegationGroupsDoNotInferFromSummaryProse(t *testing.T) {
	lineage := agent.ConversationLineage{Generations: []agent.ConversationGeneration{{SummaryPrefix: []agent.Message{{Role: agent.MessageRoleSummary, Content: "group=mentioned"}}}}}
	got, err := resolveDelegationGroups(nil, lineage)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || len(got.Names) != 0 {
		t.Fatalf("ledger = %#v, want explicit empty v1", got)
	}
}
