package main

import (
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/tool"
)

func TestBuildMCPExposure(t *testing.T) {
	t.Run("empty defs and empty config returns empty map", func(t *testing.T) {
		got := BuildMCPExposure(nil, nil)
		if len(got) != 0 {
			t.Fatalf("exposure = %v, want empty", got)
		}
	})

	t.Run("defs without MCP provenance contribute nothing", func(t *testing.T) {
		defs := []tool.ToolDef{
			{Name: "read", Description: "read"},
			{Name: "bash", Description: "bash"},
		}
		servers := map[string]config.MCPServerConfig{
			"notes": {SubAgents: []string{"research"}},
		}
		got := BuildMCPExposure(defs, servers)
		if len(got) != 0 {
			t.Fatalf("exposure = %v, want empty for non-MCP defs", got)
		}
	})

	t.Run("research sub_agents grants the tool to research only", func(t *testing.T) {
		defs := []tool.ToolDef{
			{Name: "mcp__notes__search", MCP: tool.MCPProvenance{Server: "notes", ToolName: "search"}},
		}
		servers := map[string]config.MCPServerConfig{
			"notes": {SubAgents: []string{"research"}},
		}
		got := BuildMCPExposure(defs, servers)
		if want := []string{"mcp__notes__search"}; !reflect.DeepEqual(got[delegation.AgentTypeResearch], want) {
			t.Fatalf("research exposure = %v, want %v", got[delegation.AgentTypeResearch], want)
		}
		for _, agentType := range delegation.AllAgentTypes() {
			if agentType == delegation.AgentTypeResearch {
				continue
			}
			if got, ok := got[agentType]; ok {
				t.Fatalf("%s exposure = %v, want absent", agentType, got)
			}
		}
	})

	t.Run("multiple servers with overlapping sub_agents", func(t *testing.T) {
		defs := []tool.ToolDef{
			{Name: "mcp__notes__search", MCP: tool.MCPProvenance{Server: "notes", ToolName: "search"}},
			{Name: "mcp__web__fetch", MCP: tool.MCPProvenance{Server: "web", ToolName: "fetch"}},
		}
		servers := map[string]config.MCPServerConfig{
			"notes": {SubAgents: []string{"research", "code"}},
			"web":   {SubAgents: []string{"research"}},
		}
		got := BuildMCPExposure(defs, servers)
		if want := []string{"mcp__notes__search", "mcp__web__fetch"}; !reflect.DeepEqual(got[delegation.AgentTypeResearch], want) {
			t.Fatalf("research exposure = %v, want %v", got[delegation.AgentTypeResearch], want)
		}
		if want := []string{"mcp__notes__search"}; !reflect.DeepEqual(got[delegation.AgentTypeCode], want) {
			t.Fatalf("code exposure = %v, want %v", got[delegation.AgentTypeCode], want)
		}
	})

	t.Run("deduplicates repeated registrations", func(t *testing.T) {
		defs := []tool.ToolDef{
			{Name: "mcp__notes__search", MCP: tool.MCPProvenance{Server: "notes", ToolName: "search"}},
			{Name: "mcp__notes__search", MCP: tool.MCPProvenance{Server: "notes", ToolName: "search"}},
		}
		servers := map[string]config.MCPServerConfig{
			"notes": {SubAgents: []string{"research", "research"}},
		}
		got := BuildMCPExposure(defs, servers)
		if want := []string{"mcp__notes__search"}; !reflect.DeepEqual(got[delegation.AgentTypeResearch], want) {
			t.Fatalf("research exposure = %v, want %v", got[delegation.AgentTypeResearch], want)
		}
	})

	t.Run("sorts exposure values", func(t *testing.T) {
		defs := []tool.ToolDef{
			{Name: "mcp__notes__zeta", MCP: tool.MCPProvenance{Server: "notes", ToolName: "zeta"}},
			{Name: "mcp__notes__alpha", MCP: tool.MCPProvenance{Server: "notes", ToolName: "alpha"}},
		}
		servers := map[string]config.MCPServerConfig{
			"notes": {SubAgents: []string{"research"}},
		}
		got := BuildMCPExposure(defs, servers)
		if want := []string{"mcp__notes__alpha", "mcp__notes__zeta"}; !reflect.DeepEqual(got[delegation.AgentTypeResearch], want) {
			t.Fatalf("research exposure = %v, want %v", got[delegation.AgentTypeResearch], want)
		}
	})

	t.Run("nil or empty sub_agents grants nothing", func(t *testing.T) {
		defs := []tool.ToolDef{
			{Name: "mcp__notes__search", MCP: tool.MCPProvenance{Server: "notes", ToolName: "search"}},
		}
		servers := map[string]config.MCPServerConfig{
			"nil-sub-agents":   {},
			"empty-sub-agents": {SubAgents: []string{}},
		}
		got := BuildMCPExposure(defs, servers)
		if len(got) != 0 {
			t.Fatalf("exposure = %v, want empty", got)
		}
	})

	t.Run("registered def for unknown server name is ignored", func(t *testing.T) {
		defs := []tool.ToolDef{
			{Name: "mcp__ghost__search", MCP: tool.MCPProvenance{Server: "ghost", ToolName: "search"}},
		}
		got := BuildMCPExposure(defs, map[string]config.MCPServerConfig{})
		if len(got) != 0 {
			t.Fatalf("exposure = %v, want empty", got)
		}
	})
}
