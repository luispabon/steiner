package config

import (
	"fmt"
	"strings"
)

func validateMCPConfig(problems *[]string, cfg MCPConfig) {
	for name, srv := range cfg.Servers {
		if name == "" {
			*problems = append(*problems, "mcp.servers contains an empty server name")
			continue
		}
		if strings.Contains(name, "__") {
			*problems = append(*problems, fmt.Sprintf("mcp.servers.%s: server name must not contain \"__\" (the naming delimiter)", name))
		}
		if srv.Transport != "" && srv.Transport != "stdio" {
			*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.transport: %q is not supported (only \"stdio\")", name, srv.Transport))
		}
		if srv.Approval != "" && srv.Approval != "ask" && srv.Approval != "allow" && srv.Approval != "deny" {
			*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.approval: %q is invalid (must be ask, allow, or deny)", name, srv.Approval))
		}
		if srv.Command == "" {
			*problems = append(*problems, fmt.Sprintf("mcp.servers.%s.command is required", name))
		}
	}
}
