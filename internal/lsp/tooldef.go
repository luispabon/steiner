package lsp

import (
	"context"
	"fmt"

	"github.com/luispabon/steiner/internal/tool"
)

// ToolDefs returns the seven LSP tool definitions in deterministic order: lsp_definitions, lsp_references, lsp_diagnostics, lsp_hover, lsp_symbols, lsp_implementations, lsp_type_definitions.
func ToolDefs(m *Manager) []tool.ToolDef {
	return []tool.ToolDef{
		definitionsTool(m),
		referencesTool(m),
		diagnosticsTool(m),
		hoverTool(m),
		symbolsTool(m),
		implementationsTool(m),
		typeDefinitionTool(m),
	}
}

//nolint:dupl // identical to implementationsTool and typeDefinitionTool; mechanical clone across protocol.DefinitionResult-aliased types
func definitionsTool(m *Manager) tool.ToolDef {
	return tool.ToolDef{
		Name:         "lsp_definitions",
		ParallelSafe: true,
		Description:  "Jump to symbol definitions. Address the position with line+column, or with symbol (optionally narrowed by line). Requires a configured language server for the file's extension; returns empty results if no server is enabled. Results may be incomplete if the language server's indexing has not finished.",
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
				"symbol": map[string]any{
					"type":        "string",
					"description": "Symbol name to search for at identifier boundaries; searches entire file if line is omitted, or just that line if line is specified",
				},
			},
			"required":             []string{"file"},
			"additionalProperties": false,
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			file, ok := input["file"].(string)
			if !ok {
				return nil, fmt.Errorf("lsp_definitions: missing or invalid file parameter")
			}

			line, lineOk := parseLineParameter(input)
			col, colOk := parseColumnParameter(input)
			symbol, symbolOk := parseSymbolParameter(input)

			lineResolved, colResolved, echoLine, resolveErr := resolvePosition(m, "lsp_definitions", line, lineOk, col, colOk, symbol, symbolOk, file)
			if resolveErr != "" {
				return resolveErr, nil
			}

			result, err := m.Definitions(ctx, file, lineResolved, colResolved)

			if unavailableMsg, goErr := handleNavigationError(m, file, err); unavailableMsg != nil {
				return unavailableMsg, nil
			} else if goErr != nil {
				return nil, goErr
			}

			output := formatLocations(m.workspace, result, m.cfg)
			if output == "" {
				output = "(no definitions found)"
			}

			if echoLine != "" {
				output = echoLine + "\n" + output
			}

			return output, nil
		},
	}
}

func referencesTool(m *Manager) tool.ToolDef {
	return tool.ToolDef{
		Name:         "lsp_references",
		ParallelSafe: true,
		Description:  "Find all references to a symbol. Address the position with line+column, or with symbol (optionally narrowed by line). Requires a configured language server for the file's extension; returns empty results if no server is enabled. Results may be incomplete if the language server's indexing has not finished. By default includes the symbol's declaration; set include_declaration to false to exclude it.",
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
				"symbol": map[string]any{
					"type":        "string",
					"description": "Symbol name to search for at identifier boundaries; searches entire file if line is omitted, or just that line if line is specified",
				},
				"include_declaration": map[string]any{
					"type":        "boolean",
					"description": "Include the symbol's declaration in results",
					"default":     true,
				},
			},
			"required":             []string{"file"},
			"additionalProperties": false,
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			file, ok := input["file"].(string)
			if !ok {
				return nil, fmt.Errorf("lsp_references: missing or invalid file parameter")
			}

			line, lineOk := parseLineParameter(input)
			col, colOk := parseColumnParameter(input)
			symbol, symbolOk := parseSymbolParameter(input)

			lineResolved, colResolved, echoLine, resolveErr := resolvePosition(m, "lsp_references", line, lineOk, col, colOk, symbol, symbolOk, file)
			if resolveErr != "" {
				return resolveErr, nil
			}

			includeDecl := true
			if v, ok := input["include_declaration"]; ok {
				if b, ok := v.(bool); ok {
					includeDecl = b
				}
			}

			result, err := m.References(ctx, file, lineResolved, colResolved, includeDecl)

			if unavailableMsg, goErr := handleNavigationError(m, file, err); unavailableMsg != nil {
				return unavailableMsg, nil
			} else if goErr != nil {
				return nil, goErr
			}

			output := formatLocations(m.workspace, result, m.cfg)
			if output == "" {
				output = "(no references found)"
			}

			if echoLine != "" {
				output = echoLine + "\n" + output
			}

			return output, nil
		},
	}
}

func diagnosticsTool(m *Manager) tool.ToolDef {
	return tool.ToolDef{
		Name:         "lsp_diagnostics",
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
				return nil, fmt.Errorf("lsp_diagnostics: missing or invalid file parameter")
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

func hoverTool(m *Manager) tool.ToolDef {
	return tool.ToolDef{
		Name:         "lsp_hover",
		ParallelSafe: true,
		Description:  "Get hover information for a symbol at a position. Address the position with line+column, or with symbol (optionally narrowed by line). Requires a configured language server for the file's extension; returns empty results if no server is enabled. Results may be incomplete if the language server's indexing has not finished.",
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
				"symbol": map[string]any{
					"type":        "string",
					"description": "Symbol name to search for at identifier boundaries; searches entire file if line is omitted, or just that line if line is specified",
				},
			},
			"required":             []string{"file"},
			"additionalProperties": false,
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			file, ok := input["file"].(string)
			if !ok {
				return nil, fmt.Errorf("lsp_hover: missing or invalid file parameter")
			}

			line, lineOk := parseLineParameter(input)
			col, colOk := parseColumnParameter(input)
			symbol, symbolOk := parseSymbolParameter(input)

			lineResolved, colResolved, echoLine, resolveErr := resolvePosition(m, "lsp_hover", line, lineOk, col, colOk, symbol, symbolOk, file)
			if resolveErr != "" {
				return resolveErr, nil
			}

			result, err := m.Hover(ctx, file, lineResolved, colResolved)

			if unavailableMsg, goErr := handleNavigationError(m, file, err); unavailableMsg != nil {
				return unavailableMsg, nil
			} else if goErr != nil {
				return nil, goErr
			}

			output := formatHover(result, m.cfg)
			if output == "" {
				output = "(no hover information)"
			}

			if echoLine != "" {
				output = echoLine + "\n" + output
			}

			return output, nil
		},
	}
}

func symbolsTool(m *Manager) tool.ToolDef {
	return tool.ToolDef{
		Name:         "lsp_symbols",
		ParallelSafe: true,
		Description:  "Search for symbols by name across the workspace (query), or outline a file's symbols (file). Pass both to filter one file's outline by name. Requires at least one configured, enabled language server. Results may be incomplete if indexing has not finished.",
		ParameterSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Symbol name to search for (substring match)"},
				"file":  map[string]any{"type": "string", "description": "File path to outline (workspace-relative or absolute)"},
			},
			"required":             []string{},
			"additionalProperties": false,
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			query, _ := input["query"].(string)
			file, _ := input["file"].(string)
			if query == "" && file == "" {
				return nil, fmt.Errorf("lsp_symbols: at least one of query or file is required")
			}

			var result SymbolResult
			var err error
			if file != "" {
				result, err = m.DocumentSymbols(ctx, file, query)
			} else {
				result, err = m.WorkspaceSymbols(ctx, query)
			}

			if unavailableMsg, goErr := handleSymbolsError(m, file, err); unavailableMsg != nil {
				return unavailableMsg, nil
			} else if goErr != nil {
				return nil, goErr
			}

			output := formatSymbols(m.workspace, result, m.cfg)
			if output == "" {
				output = "(no symbols found)"
			}
			return output, nil
		},
	}
}

//nolint:dupl // identical to definitionsTool and typeDefinitionTool; mechanical clone across protocol.DefinitionResult-aliased types
func implementationsTool(m *Manager) tool.ToolDef {
	return tool.ToolDef{
		Name:         "lsp_implementations",
		ParallelSafe: true,
		Description:  "Find the concrete implementations of an interface or interface method — the answer to \"what actually runs when this is called?\". Prefer this over lsp_definitions on an interface method: lsp_definitions returns only the interface declaration, while this returns the concrete types that satisfy it. Address the position with line+column, or with symbol (optionally narrowed by line). Requires a configured language server for the file's extension that supports textDocument/implementation; returns a message instead of results if no server is enabled or the server does not support it.",
		ParameterSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file":   map[string]any{"type": "string", "description": "File path to query (workspace-relative or absolute)"},
				"line":   map[string]any{"type": "integer", "description": "Line number (1-based)"},
				"column": map[string]any{"type": "integer", "description": "Column number (1-based)"},
				"symbol": map[string]any{"type": "string", "description": "Symbol name to search for at identifier boundaries; searches entire file if line is omitted, or just that line if line is specified"},
			},
			"required":             []string{"file"},
			"additionalProperties": false,
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			file, ok := input["file"].(string)
			if !ok {
				return nil, fmt.Errorf("lsp_implementations: missing or invalid file parameter")
			}

			line, lineOk := parseLineParameter(input)
			col, colOk := parseColumnParameter(input)
			symbol, symbolOk := parseSymbolParameter(input)

			lineResolved, colResolved, echoLine, resolveErr := resolvePosition(m, "lsp_implementations", line, lineOk, col, colOk, symbol, symbolOk, file)
			if resolveErr != "" {
				return resolveErr, nil
			}

			result, err := m.Implementations(ctx, file, lineResolved, colResolved)

			if unavailableMsg, goErr := handleNavigationError(m, file, err); unavailableMsg != nil {
				return unavailableMsg, nil
			} else if goErr != nil {
				return nil, goErr
			}

			output := formatLocations(m.workspace, result, m.cfg)
			if output == "" {
				output = "(no implementations found)"
			}
			if echoLine != "" {
				output = echoLine + "\n" + output
			}
			return output, nil
		},
	}
}

//nolint:dupl // identical to definitionsTool and implementationsTool; mechanical clone across protocol.DefinitionResult-aliased types
func typeDefinitionTool(m *Manager) tool.ToolDef {
	return tool.ToolDef{
		Name:         "lsp_type_definitions",
		ParallelSafe: true,
		Description:  "Jump to the type declaration of a variable, field, or parameter. Address the position with line+column, or with symbol (optionally narrowed by line). Requires a configured language server for the file's extension that supports textDocument/typeDefinition; returns a message instead of results if no server is enabled or the server does not support it.",
		ParameterSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file":   map[string]any{"type": "string", "description": "File path to query (workspace-relative or absolute)"},
				"line":   map[string]any{"type": "integer", "description": "Line number (1-based)"},
				"column": map[string]any{"type": "integer", "description": "Column number (1-based)"},
				"symbol": map[string]any{"type": "string", "description": "Symbol name to search for at identifier boundaries; searches entire file if line is omitted, or just that line if line is specified"},
			},
			"required":             []string{"file"},
			"additionalProperties": false,
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			file, ok := input["file"].(string)
			if !ok {
				return nil, fmt.Errorf("lsp_type_definitions: missing or invalid file parameter")
			}

			line, lineOk := parseLineParameter(input)
			col, colOk := parseColumnParameter(input)
			symbol, symbolOk := parseSymbolParameter(input)

			lineResolved, colResolved, echoLine, resolveErr := resolvePosition(m, "lsp_type_definitions", line, lineOk, col, colOk, symbol, symbolOk, file)
			if resolveErr != "" {
				return resolveErr, nil
			}

			result, err := m.TypeDefinitions(ctx, file, lineResolved, colResolved)

			if unavailableMsg, goErr := handleNavigationError(m, file, err); unavailableMsg != nil {
				return unavailableMsg, nil
			} else if goErr != nil {
				return nil, goErr
			}

			output := formatLocations(m.workspace, result, m.cfg)
			if output == "" {
				output = "(no type definition found)"
			}
			if echoLine != "" {
				output = echoLine + "\n" + output
			}
			return output, nil
		},
	}
}
