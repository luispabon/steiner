package main

import (
	"os"
	"path/filepath"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

type runtimeRegistry struct {
	cfg     config.Config
	execPath string
}

func coreToolDefinitions(cfg config.Config, execPath string) []tool.ToolDef {
	coreBin := filepath.Join(filepath.Dir(execPath), "steiner-core-tools")
	return []tool.ToolDef{
		{
			Name:            "read",
			ExecPath:        coreBin,
			Subcommand:      "read",
			Description:     "Read a file from the project.",
			ParameterSchema: schemaObject(requiredStringProperty("path", "Project-relative file path to read.")),
			Timeout:         toolTimeout(cfg, "read"),
			Approval:        tool.ResolveApprovalMode(cfg, tool.ToolDef{Name: "read"}),
		},
		{
			Name:            "glob",
			ExecPath:        coreBin,
			Subcommand:      "glob",
			Description:     "Find files by glob pattern under the project.",
			ParameterSchema: schemaObject(requiredStringProperty("pattern", "Glob pattern such as \"cmd/**\" or \"*.go\".")),
			Timeout:         toolTimeout(cfg, "glob"),
			Approval:        tool.ResolveApprovalMode(cfg, tool.ToolDef{Name: "glob"}),
		},
		{
			Name:            "search",
			ExecPath:        coreBin,
			Subcommand:      "search",
			Description:     "Search text across project files.",
			ParameterSchema: schemaObject(requiredStringProperty("query", "Literal text to search for.")),
			Timeout:         toolTimeout(cfg, "search"),
			Approval:        tool.ResolveApprovalMode(cfg, tool.ToolDef{Name: "search"}),
		},
		{
			Name:        "write",
			ExecPath:    coreBin,
			Subcommand:  "write",
			Description: "Overwrite or create a file with complete contents.",
			ParameterSchema: schemaObject(
				requiredStringProperty("path", "Project-relative file path to write."),
				requiredStringProperty("contents", "Complete file contents to write."),
			),
			Timeout:  toolTimeout(cfg, "write"),
			Approval: tool.ResolveApprovalMode(cfg, tool.ToolDef{Name: "write"}),
		},
		{
			Name:        "edit",
			ExecPath:    coreBin,
			Subcommand:  "edit",
			Description: "Replace one exact snippet in a file.",
			ParameterSchema: schemaObject(
				requiredStringProperty("path", "Project-relative file path to edit."),
				requiredStringProperty("old", "Exact existing text to replace. Must match exactly once."),
				requiredStringProperty("new", "Replacement text."),
			),
			Timeout:  toolTimeout(cfg, "edit"),
			Approval: tool.ResolveApprovalMode(cfg, tool.ToolDef{Name: "edit"}),
		},
		{
			Name:        "bash",
			ExecPath:    coreBin,
			Subcommand:  "bash",
			Description: "Run a bash command inside the project root or a project subdirectory.",
			ParameterSchema: schemaObject(
				requiredStringProperty("command", "Shell command to run."),
				optionalStringProperty("cwd", "Optional project-relative working directory."),
			),
			Timeout:  toolTimeout(cfg, "bash"),
			Approval: tool.ResolveApprovalMode(cfg, tool.ToolDef{Name: "bash"}),
		},
	}
}

func toolTimeout(cfg config.Config, name string) time.Duration {
	if timeout, ok := cfg.Limits.ToolTimeouts[name]; ok && !timeout.IsZero() {
		return time.Duration(timeout.Duration())
	}
	if !cfg.Limits.ToolTimeoutDefault.IsZero() {
		return time.Duration(cfg.Limits.ToolTimeoutDefault.Duration())
	}
	return 30 * time.Second
}

func schemaObject(properties ...map[string]any) map[string]any {
	props := make(map[string]any, len(properties))
	required := make([]any, 0, len(properties))
	for _, property := range properties {
		name, _ := property["_name"].(string)
		if name == "" {
			continue
		}
		schema := tool.CloneJSONMap(property)
		delete(schema, "_name")
		delete(schema, "_required")
		props[name] = schema
		if req, _ := property["_required"].(bool); req {
			required = append(required, name)
		}
	}
	out := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func requiredStringProperty(name, description string) map[string]any {
	return map[string]any{
		"_name":       name,
		"_required":   true,
		"type":        "string",
		"description": description,
	}
}

func optionalStringProperty(name, description string) map[string]any {
	return map[string]any{
		"_name":       name,
		"_required":   false,
		"type":        "string",
		"description": description,
	}
}

func registryToolSpecs(registry *tool.Registry) []provider.ToolSpec {
	if registry == nil {
		return nil
	}
	defs := registry.Definitions()
	if len(defs) == 0 {
		return nil
	}

	specs := make([]provider.ToolSpec, 0, len(defs))
	for _, def := range defs {
		specs = append(specs, provider.ToolSpec{
			Type: "function",
			Function: provider.ToolFunctionSpec{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  tool.CloneJSONMap(def.ParameterSchema),
			},
		})
	}
	return specs
}

func runtimeRegistry(cfg config.Config) (*tool.Registry, error) {
	execPath, err := os.Executable()
	if err != nil {
		return nil, err
	}

	registry := tool.NewRegistry(coreToolDefinitions(cfg, execPath)...)
	for _, def := range tool.NewRegistryFromConfig(cfg).Definitions() {
		registry.Register(def)
	}
	return registry, nil
}
