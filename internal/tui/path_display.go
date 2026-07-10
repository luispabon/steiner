package tui

import (
	"path/filepath"
	"strings"
)

// shortenAttachedImagePath renders filePath for display in the images-attached
// list: workspace-relative first, then ~-relative, then middle-ellipsis if still long.
func shortenAttachedImagePath(filePath, workingDir, homeDir string, maxWidth int) string {
	// Step 1: Try workspace-relative form
	if workingDir != "" && filepath.IsAbs(workingDir) && filepath.IsAbs(filePath) {
		rel, err := filepath.Rel(workingDir, filePath)
		if err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			result := rel
			if len([]rune(result)) > maxWidth {
				return fitTextMiddle(result, maxWidth)
			}
			return result
		}
	}

	// Step 2: Fall back to home-relative form
	result := homeRelativePath(filePath, homeDir)

	// Step 3: Apply middle-ellipsis if still too long
	if len([]rune(result)) > maxWidth {
		return fitTextMiddle(result, maxWidth)
	}

	return result
}
