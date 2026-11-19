package main

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/provider"
)

// TestCLIRunnerCompactWithLineageUsesGivenOrRebuiltParentTools proves a manual
// compaction keeps the parent snapshot's tools when the process holds them, and
// rebuilds the same parent registry a run would send when it does not, as after
// a resume.
func TestCLIRunnerCompactWithLineageUsesGivenOrRebuiltParentTools(t *testing.T) {
	r, prov, _ := newCompactTestRunner(t)
	r.runtime.registry = runtimeRegistryWithSinkAndMode(registryTestConfig(), t.TempDir(), nil, false, nil, nil, nil, nil)
	summary := provider.ChatResponse{
		Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "summary"},
		FinishReason: "stop",
	}
	prov.responses = []provider.ChatResponse{summary, summary}
	conversation := []agent.Message{
		{Role: agent.MessageRoleUser, Content: "first request"}, {Role: agent.MessageRoleAssistant, Content: "first answer"},
		{Role: agent.MessageRoleUser, Content: "second request"}, {Role: agent.MessageRoleAssistant, Content: "second answer"},
	}

	snapshotTools := []provider.ToolSpec{{Function: provider.ToolFunctionSpec{Name: "snapshot_tool"}}}
	if _, _, _, err := r.CompactWithLineage(context.Background(), conversation, agent.ConversationLineage{}, agent.LastMainRequest{}, nil, snapshotTools, ""); err != nil {
		t.Fatalf("CompactWithLineage(with snapshot tools) error = %v", err)
	}
	if len(prov.requests) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(prov.requests))
	}
	if got := toolNames(prov.requests[0].Tools); !reflect.DeepEqual(got, []string{"snapshot_tool"}) {
		t.Fatalf("snapshot tools sent = %v, want the parent snapshot's", got)
	}

	if _, _, _, err := r.CompactWithLineage(context.Background(), conversation, agent.ConversationLineage{}, agent.LastMainRequest{}, nil, nil, ""); err != nil {
		t.Fatalf("CompactWithLineage(without snapshot tools) error = %v", err)
	}
	if len(prov.requests) != 2 {
		t.Fatalf("provider requests = %d, want 2", len(prov.requests))
	}
	rebuilt := toolNames(prov.requests[1].Tools)
	for _, want := range []string{"read", delegation.SubAgentToolName} {
		if !slices.Contains(rebuilt, want) {
			t.Fatalf("rebuilt tools %v missing %q", rebuilt, want)
		}
	}
}

func toolNames(tools []provider.ToolSpec) []string {
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Function.Name
	}
	return names
}
