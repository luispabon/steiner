package config

func applyMCPPatch(dst *MCPConfig, patch *mcpPatch) {
	if patch.Enabled != nil {
		dst.Enabled = *patch.Enabled
	}
	if patch.Servers != nil {
		if dst.Servers == nil {
			dst.Servers = make(map[string]MCPServerConfig)
		}
		for name, srv := range *patch.Servers {
			current := dst.Servers[name]
			applyMCPServerPatch(&current, &srv)
			dst.Servers[name] = current
		}
	}
}

func applyMCPServerPatch(dst *MCPServerConfig, patch *mcpServerPatch) {
	if patch.Enabled != nil {
		dst.Enabled = *patch.Enabled
	}
	if patch.Transport != nil {
		dst.Transport = *patch.Transport
	}
	if patch.Command != nil {
		dst.Command = *patch.Command
	}
	if patch.Args != nil {
		dst.Args = *patch.Args
	}
	if patch.Env != nil {
		if dst.Env == nil {
			dst.Env = make(map[string]string)
		}
		for k, v := range *patch.Env {
			dst.Env[k] = v
		}
	}
	if patch.URL != nil {
		dst.URL = *patch.URL
	}
	if patch.Headers != nil {
		if dst.Headers == nil {
			dst.Headers = make(map[string]string)
		}
		for k, v := range *patch.Headers {
			dst.Headers[k] = v
		}
	}
	if patch.Approval != nil {
		dst.Approval = *patch.Approval
	}
	if patch.TrustAnnotations != nil {
		dst.TrustAnnotations = *patch.TrustAnnotations
	}
	if patch.ConnectTimeout != nil {
		dst.ConnectTimeout = *patch.ConnectTimeout
	}
	applyMCPServerListPatches(dst, patch)
}

// applyMCPServerListPatches replaces the per-server tool filter and sub-agent
// lists when set. A nil patch field means "don't touch"; a non-nil pointer
// (even to an empty slice) replaces the destination list entirely.
func applyMCPServerListPatches(dst *MCPServerConfig, patch *mcpServerPatch) {
	if patch.AllowedTools != nil {
		dst.AllowedTools = *patch.AllowedTools
	}
	if patch.BlockedTools != nil {
		dst.BlockedTools = *patch.BlockedTools
	}
	if patch.SubAgents != nil {
		dst.SubAgents = *patch.SubAgents
	}
}
