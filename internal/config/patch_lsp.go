package config

func applyLSPPatch(dst *LSPConfig, patch *lspPatch) {
	if patch.Enabled != nil {
		dst.Enabled = *patch.Enabled
	}
	if patch.IdleTimeout != nil {
		dst.IdleTimeout = *patch.IdleTimeout
	}
	if patch.RequestTimeout != nil {
		dst.RequestTimeout = *patch.RequestTimeout
	}
	if patch.ReadyTimeout != nil {
		dst.ReadyTimeout = *patch.ReadyTimeout
	}
	if patch.ReadyGracePeriod != nil {
		dst.ReadyGracePeriod = *patch.ReadyGracePeriod
	}
	if patch.DiagnosticsWindow != nil {
		dst.DiagnosticsWindow = *patch.DiagnosticsWindow
	}
	if patch.MaxResults != nil {
		dst.MaxResults = *patch.MaxResults
	}
	if patch.CacheDir != nil {
		dst.CacheDir = *patch.CacheDir
	}
	if patch.Servers != nil {
		if dst.Servers == nil {
			dst.Servers = make(map[string]LSPServerConfig)
		}
		for name, srv := range *patch.Servers {
			current := dst.Servers[name]
			applyLSPServerPatch(&current, &srv)
			dst.Servers[name] = current
		}
	}
}

func applyLSPServerPatch(dst *LSPServerConfig, patch *lspServerPatch) {
	if patch.Enabled != nil {
		dst.Enabled = *patch.Enabled
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
	if patch.FileExtensions != nil {
		dst.FileExtensions = *patch.FileExtensions
	}
	if patch.RootMarkers != nil {
		dst.RootMarkers = *patch.RootMarkers
	}
	if patch.InitializationOptions != nil {
		if dst.InitializationOptions == nil {
			dst.InitializationOptions = make(map[string]any)
		}
		for k, v := range *patch.InitializationOptions {
			dst.InitializationOptions[k] = v
		}
	}
}
