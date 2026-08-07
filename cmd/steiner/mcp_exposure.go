package main

import (
	"slices"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/tool"
)

// BuildMCPExposure returns a sorted, deduplicated projection of MCP tool names
// that each sub-agent type should receive, derived from the configured
// sub_agents fields on MCP server configs. Only registered MCP tools (those
// that survived filtering and denial) contribute: filtered and denied tools
// have no tool.ToolDef, so they are naturally absent.
//
// The returned map is keyed by delegation.AgentType and holds sorted,
// deduplicated registered tool names. Agent types with no exposed tools are
// absent from the map, so a nil or empty result grants no extra tools (child
// MCP access defaults closed).
func BuildMCPExposure(defs []tool.ToolDef, mcpServers map[string]config.MCPServerConfig) map[delegation.AgentType][]string {
	exposure := make(map[delegation.AgentType][]string)
	for _, def := range defs {
		if def.MCP.Server == "" {
			continue
		}
		serverCfg, ok := mcpServers[def.MCP.Server]
		if !ok {
			continue
		}
		for _, agentType := range serverCfg.SubAgents {
			exposure[delegation.AgentType(agentType)] = append(exposure[delegation.AgentType(agentType)], def.Name)
		}
	}
	for agentType, names := range exposure {
		slices.Sort(names)
		exposure[agentType] = slices.Compact(names)
	}
	return exposure
}
