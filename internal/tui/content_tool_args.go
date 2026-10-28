package tui

import (
	"strings"

	"github.com/luispabon/steiner/internal/delegation"
)

// specializedDelegateTools is the set of tool names rendered as a single
// delegation box in the TUI. It is derived from internal/delegation so the
// specialized delegate surface stays canonical across packages.
var specializedDelegateTools = specializedDelegateToolSet()

func specializedDelegateToolSet() map[string]bool {
	names := delegation.AllSpecializedDelegateTools()
	tools := make(map[string]bool, len(names))
	for _, tool := range names {
		tools[strings.ToLower(strings.TrimSpace(tool))] = true
	}
	return tools
}

// isSpecializedDelegateTool reports whether tool is a specialized delegate tool.
func isSpecializedDelegateTool(tool string) bool {
	return specializedDelegateTools[strings.ToLower(strings.TrimSpace(tool))]
}
