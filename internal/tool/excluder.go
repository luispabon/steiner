package tool

// Built-in heuristic exclusion patterns. These are always active and match
// against any path component.
var builtinExcludeEntries = []string{
	".git",
	"node_modules",
	".steiner",
	".claude",
	".git-worktrees",
	"vendor",
	".cache",
	"dist",
	"build",
	"out",
	"target",
	".next",
	".nuxt",
}

// FilePickerAlwaysInclude lists path components that are shown in the TUI
// file picker and file list overlays even when the built-in heuristic
// exclusion list would otherwise hide them. Used to surface project-metadata
// folders (such as .steiner) that are otherwise excluded by default.
//
// Matching is component-based, so a nested directory named .steiner at any
// depth is also re-included. Exact prefix exclusions such as
// FilePickerExcludePaths take precedence over this list.
var FilePickerAlwaysInclude = []string{
	".steiner",
}

// FilePickerExcludePaths lists exact relative prefixes hidden from the TUI
// file picker and file list overlays even when a parent directory is
// force-included. Keeps heavy .steiner subdirectories (tmp, worktrees) out
// of the picker while the rest of .steiner stays visible.
var FilePickerExcludePaths = []string{
	".steiner/tmp",
	".steiner/worktrees",
}
