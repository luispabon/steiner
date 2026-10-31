package builtin

import (
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/tool"
)

// searchLexicalPath maps an entry the walker reached under walkRoot back to the
// lexical path the caller named. resolveSearchRoot replaces a symlink root with
// its target, so walk entries no longer share a prefix with the path the user
// asked for; joining the walk-relative part onto lexicalRoot restores that
// view. It returns path unchanged when both roots match or path falls outside
// walkRoot.
func searchLexicalPath(walkRoot, lexicalRoot, path string) string {
	if walkRoot == lexicalRoot || lexicalRoot == "" {
		return path
	}
	rel, err := filepath.Rel(walkRoot, path)
	if err != nil {
		return path
	}
	switch {
	case rel == ".":
		return lexicalRoot
	case rel == "..", strings.HasPrefix(rel, ".."+string(filepath.Separator)):
		return path
	}
	return filepath.Join(lexicalRoot, rel)
}

// excludeSearchEntry reports whether workspace exclusions hide the entry at
// path, which the walker reached under walkRoot. WorkspaceExcluder compares
// paths lexically, so a resolved symlink root would otherwise hide configured
// exclusions written against the symlink itself. Both the resolved entry and
// its lexical counterpart are validated, each against its own root: built-in
// directory names match walk-relative components only, so a built-in name in
// the ancestors of the lexical alias cannot hide an explicit root.
func excludeSearchEntry(excluder *tool.WorkspaceExcluder, policy *tool.PathPolicy, walkRoot, lexicalRoot, path string, isDir bool) bool {
	if excluder == nil {
		return false
	}
	walkRoot, lexicalRoot, path = searchExclusionPaths(policy, walkRoot, lexicalRoot, path)
	if excluder.ExcludeEntry(walkRoot, path, isDir) {
		return true
	}
	lexicalPath := searchLexicalPath(walkRoot, lexicalRoot, path)
	if lexicalPath == path {
		return false
	}
	// The lexical alias keeps the walk-relative components of the resolved
	// walk, so matching it against its own root only changes the root entry:
	// that entry has no component left to match a built-in name against.
	return excluder.ExcludeEntry(lexicalRoot, lexicalPath, isDir)
}

// searchExclusionPaths maps sandbox tmp storage paths back to the virtual /tmp
// paths a caller sees. Sandbox /tmp lives inside the workspace, so matching the
// internal storage path would let a user exclusion for that directory hide the
// sandbox tmp tree; anchoring exclusions at /tmp keeps that tree searchable and
// applies an explicit /tmp exclusion instead.
func searchExclusionPaths(policy *tool.PathPolicy, walkRoot, lexicalRoot, path string) (string, string, string) {
	if policy == nil {
		return walkRoot, lexicalRoot, path
	}
	if !policy.IsSandboxTmpPath(walkRoot) && !policy.IsSandboxTmpPath(lexicalRoot) {
		return walkRoot, lexicalRoot, path
	}
	return policy.DisplayPath(walkRoot), policy.DisplayPath(lexicalRoot), policy.DisplayPath(path)
}
