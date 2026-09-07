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
	if cfg.MaxResults <= 0 {
		problems = append(problems, "lsp.max_results must be greater than zero")
	}

	// Check servers
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

		// Check for duplicate file extensions
		for _, ext := range srv.FileExtensions {
			if existing, ok := extensionMap[ext]; ok {
				problems = append(problems, fmt.Sprintf("lsp: file extension %q claimed by both %q and %q", ext, existing, name))
			} else {
				extensionMap[ext] = name
			}
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("validate lsp: %s", strings.Join(problems, "; "))
	}
	return nil
}
