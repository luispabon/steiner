package builtin

import "github.com/luispabon/steiner/internal/tool"

// Env holds the environment for built-in tool execution.
type Env struct {
	WorkDir    string
	PathPolicy *tool.PathPolicy
	Excluder   *tool.PathExcluder
}
