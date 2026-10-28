package tui

import "github.com/luispabon/steiner/internal/delegation"

// isSpecializedDelegateTool reports whether tool is a specialized delegate tool.
// The set is owned by internal/delegation so the specialized delegate surface
// stays canonical across packages.
func isSpecializedDelegateTool(tool string) bool {
	return delegation.IsSpecializedDelegateTool(tool)
}
