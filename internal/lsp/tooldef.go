package lsp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/tool"
)

// ToolDefs returns the three LSP tool definitions in deterministic order: definitions, references, diagnostics.
func ToolDefs(m *Manager) []tool.ToolDef {
	return []tool.ToolDef{
		definitionsTool(m),
		referencesTool(m),
		diagnosticsTool(m),
	}
}

func definitionsTool(m *Manager) tool.ToolDef {
	return tool.ToolDef{
		Name:         "definitions",
		ParallelSafe: true,
		Description:  "Jump to symbol definitions. Requires a configured language server for the file's extension; returns empty results if no server is enabled. Results may be incomplete if the language server's indexing has not finished.",
		ParameterSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file": map[string]any{
					"type":        "string",
					"description": "File path to query (workspace-relative or absolute)",
				},
				"line": map[string]any{
					"type":        "integer",
					"description": "Line number (1-based)",
				},
				"column": map[string]any{
					"type":        "integer",
					"description": "Column number (1-based)",
				},
			},
			"required":             []string{"file", "line", "column"},
			"additionalProperties": false,
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			file, ok := input["file"].(string)
			if !ok {
				return nil, fmt.Errorf("definitions: missing or invalid file parameter")
			}

			line, ok := input["line"].(float64)
			if !ok {
				return nil, fmt.Errorf("definitions: missing or invalid line parameter")
			}

			col, ok := input["column"].(float64)
			if !ok {
				return nil, fmt.Errorf("definitions: missing or invalid column parameter")
			}

			result, err := m.Definitions(ctx, file, int(line), int(col))

			if unavailableMsg, goErr := handleNavigationError(m, file, err); unavailableMsg != nil {
				return unavailableMsg, nil
			} else if goErr != nil {
				return nil, goErr
			}

			output := formatLocations(m.workspace, result, m.cfg)
			if output == "" {
				output = "(no definitions found)"
			}

			return output, nil
		},
	}
}

func referencesTool(m *Manager) tool.ToolDef {
	return tool.ToolDef{
		Name:         "references",
		ParallelSafe: true,
		Description:  "Find all references to a symbol. Requires a configured language server for the file's extension; returns empty results if no server is enabled. Results may be incomplete if the language server's indexing has not finished. By default includes the symbol's declaration; set include_declaration to false to exclude it.",
		ParameterSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file": map[string]any{
					"type":        "string",
					"description": "File path to query (workspace-relative or absolute)",
				},
				"line": map[string]any{
					"type":        "integer",
					"description": "Line number (1-based)",
				},
				"column": map[string]any{
					"type":        "integer",
					"description": "Column number (1-based)",
				},
				"include_declaration": map[string]any{
					"type":        "boolean",
					"description": "Include the symbol's declaration in results",
					"default":     true,
				},
			},
			"required":             []string{"file", "line", "column"},
			"additionalProperties": false,
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			file, ok := input["file"].(string)
			if !ok {
				return nil, fmt.Errorf("references: missing or invalid file parameter")
			}

			line, ok := input["line"].(float64)
			if !ok {
				return nil, fmt.Errorf("references: missing or invalid line parameter")
			}

			col, ok := input["column"].(float64)
			if !ok {
				return nil, fmt.Errorf("references: missing or invalid column parameter")
			}

			includeDecl := true
			if v, ok := input["include_declaration"]; ok {
				if b, ok := v.(bool); ok {
					includeDecl = b
				}
			}

			result, err := m.References(ctx, file, int(line), int(col), includeDecl)

			if unavailableMsg, goErr := handleNavigationError(m, file, err); unavailableMsg != nil {
				return unavailableMsg, nil
			} else if goErr != nil {
				return nil, goErr
			}

			output := formatLocations(m.workspace, result, m.cfg)
			if output == "" {
				output = "(no references found)"
			}

			return output, nil
		},
	}
}

func diagnosticsTool(m *Manager) tool.ToolDef {
	return tool.ToolDef{
		Name:         "diagnostics",
		ParallelSafe: true,
		Description:  "Get diagnostics (errors, warnings, etc.) for a file. Requires a configured language server for the file's extension; returns empty results if no server is enabled. Results represent the server's state at the time of the query and may be provisional if the server is still indexing.",
		ParameterSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file": map[string]any{
					"type":        "string",
					"description": "File path to query (workspace-relative or absolute)",
				},
			},
			"required":             []string{"file"},
			"additionalProperties": false,
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			file, ok := input["file"].(string)
			if !ok {
				return nil, fmt.Errorf("diagnostics: missing or invalid file parameter")
			}

			result, err := m.Diagnostics(ctx, file)

			if unavailableMsg, goErr := handleDiagnosticsError(m, file, err); unavailableMsg != nil {
				return unavailableMsg, nil
			} else if goErr != nil {
				return nil, goErr
			}

			output := formatDiagnostics(m.workspace, result, m.cfg)
			return output, nil
		},
	}
}

// handleNavigationError processes errors from Definitions or References, converting
// unavailability cases to readable result strings and returning an error for genuine failures.
func handleNavigationError(m *Manager, file string, err error) (any, error) {
	if err == nil {
		return nil, nil
	}

	if errors.Is(err, errNoServer) {
		ext := strings.ToLower(filepath.Ext(file))
		msg := fmt.Sprintf("No language server is configured for %s. Configure one under `lsp.servers` to enable this tool.", ext)
		return msg, nil
	}

	if errors.Is(err, errServerExited) {
		serverName := findServerNameForFile(m, file)
		if serverName == "" {
			return "Language server exited unexpectedly.", nil
		}
		return fmt.Sprintf("Language server %s exited unexpectedly.", serverName), nil
	}

	failedServer := findFailedServer(m, file)
	if failedServer != nil {
		return fmt.Sprintf("Language server %s failed to start: %v.", failedServer.Name, failedServer.Err), nil
	}

	return nil, err
}

// handleDiagnosticsError processes errors from Diagnostics, converting unavailability
// cases to readable result strings and returning an error for genuine failures.
func handleDiagnosticsError(m *Manager, file string, err error) (any, error) {
	if err == nil {
		return nil, nil
	}

	if errors.Is(err, errNoServer) {
		ext := strings.ToLower(filepath.Ext(file))
		msg := fmt.Sprintf("No language server is configured for %s. Configure one under `lsp.servers` to enable this tool.", ext)
		return msg, nil
	}

	if errors.Is(err, errServerExited) {
		serverName := findServerNameForFile(m, file)
		if serverName == "" {
			return "Language server exited unexpectedly.", nil
		}
		return fmt.Sprintf("Language server %s exited unexpectedly.", serverName), nil
	}

	failedServer := findFailedServer(m, file)
	if failedServer != nil {
		return fmt.Sprintf("Language server %s failed to start: %v.", failedServer.Name, failedServer.Err), nil
	}

	return nil, err
}

// findServerNameForFile returns the server name for the given file's extension,
// or an empty string if no server is configured.
func findServerNameForFile(m *Manager, file string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.serverForExtension(file)
}

// findFailedServer scans ServerStates for an entry whose status is Failed and
// whose server handles the file's extension. Returns nil if none found.
func findFailedServer(m *Manager, file string) *ServerState {
	m.mu.Lock()
	serverName := m.serverForExtension(file)
	m.mu.Unlock()

	if serverName == "" {
		return nil
	}

	states := m.ServerStates()
	for i := range states {
		if states[i].Name == serverName && states[i].Status == ServerStatusFailed {
			return &states[i]
		}
	}

	return nil
}
