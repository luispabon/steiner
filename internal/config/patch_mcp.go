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
}
