package builtin

import (
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

// Env holds the environment for built-in tool execution.
type Env struct {
	WorkDir    string
	PathPolicy *tool.PathPolicy
	Excluder   *tool.PathExcluder
	// EventSink is the sink used to emit side-channel events (e.g. display_file).
	// When nil or a NoopSink, tools that require event emission will fail cleanly.
	EventSink output.EventSink
	// Interactive reports whether the session is running in interactive (TUI) mode.
	// Tools that require a live TUI must check this and return a bounded failure when false.
	Interactive bool
}
