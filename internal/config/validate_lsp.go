package config

import (
	"fmt"
	"sort"
	"strings"
)

func validateLSP(cfg LSPConfig) error {
	if !cfg.Enabled {
		return nil
	}

	var problems []string

	// Check durations and MaxResults are > 0
	problems = append(problems, validateDurations(cfg)...)
	if cfg.MaxResults <= 0 {
		problems = append(problems, "lsp.max_results must be greater than zero")
	}

	// Check servers
	problems = append(problems, validateServers(cfg)...)

	if len(problems) > 0 {
		return fmt.Errorf("validate lsp: %s", strings.Join(problems, "; "))
	}
	return nil
}

func validateDurations(cfg LSPConfig) []string {
	var problems []string
	if cfg.IdleTimeout.Duration() <= 0 {
		problems = append(problems, "lsp.idle_timeout must be greater than zero")
	}
	if cfg.RequestTimeout.Duration() <= 0 {
		problems = append(problems, "lsp.request_timeout must be greater than zero")
	}
	if cfg.ReadyTimeout.Duration() <= 0 {
		problems = append(problems, "lsp.ready_timeout must be greater than zero")
	}
	if cfg.ReadyGracePeriod.Duration() <= 0 {
		problems = append(problems, "lsp.ready_grace_period must be greater than zero")
	}
	if cfg.DiagnosticsWindow.Duration() <= 0 {
		problems = append(problems, "lsp.diagnostics_window must be greater than zero")
	}
	return problems
}

func validateServers(cfg LSPConfig) []string {
	var problems []string
	extensionMap := make(map[string]string)
	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		srv := cfg.Servers[name]

		if name == "" {
			problems = append(problems, "lsp.servers contains an empty server name")
			continue
		}

		if !srv.Enabled {
			continue
		}

		// Validate this server and check for duplicate extensions
		problems = append(problems, validateServer(name, srv)...)
		problems = append(problems, checkDuplicateExtensions(name, srv, extensionMap)...)
	}
	return problems
}

func validateServer(name string, srv LSPServerConfig) []string {
	var problems []string

	// Check command is non-empty
	if srv.Command == "" {
		problems = append(problems, fmt.Sprintf("lsp.servers.%s.command is required", name))
	}

	// Check file_extensions are non-empty and start with "."
	if len(srv.FileExtensions) == 0 {
		problems = append(problems, fmt.Sprintf("lsp.servers.%s.file_extensions must be non-empty", name))
	} else {
		for _, ext := range srv.FileExtensions {
			if !strings.HasPrefix(ext, ".") {
				problems = append(problems, fmt.Sprintf("lsp.servers.%s.file_extensions: %q must start with \".\"", name, ext))
			}
		}
	}

	// Check root_markers are non-empty
	if len(srv.RootMarkers) == 0 {
		problems = append(problems, fmt.Sprintf("lsp.servers.%s.root_markers must be non-empty", name))
	}

	return problems
}

func checkDuplicateExtensions(name string, srv LSPServerConfig, extensionMap map[string]string) []string {
	var problems []string
	for _, ext := range srv.FileExtensions {
		// Match on the lowercased extension: Manager.serverForExtension routes
		// case-insensitively, so ".go" and ".GO" claim the same files.
		key := strings.ToLower(ext)
		if existing, ok := extensionMap[key]; ok {
			problems = append(problems, fmt.Sprintf("lsp: file extension %q claimed by both %q and %q", ext, existing, name))
		} else {
			extensionMap[key] = name
		}
	}
	return problems
}
