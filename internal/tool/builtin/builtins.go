package builtin

import "github.com/luispabon/steiner/internal/tool"

// Builtins returns all built-in tool definitions for the given environment.
func Builtins(env Env) []tool.ToolDef {
	return []tool.ToolDef{
		NewReadTool(env),
		NewWriteTool(env),
		NewEditTool(env),
		NewGlobTool(env),
		NewGrepTool(env),
		NewLSTool(env),
		NewBashTool(env),
	}
}
