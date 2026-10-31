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
// its lexical counterpart are validated; built-in directory names stay
// walk-relative because the lexical form falls outside walkRoot.
func excludeSearchEntry(excluder *tool.WorkspaceExcluder, walkRoot, lexicalRoot, path string, isDir bool) bool {
	if excluder == nil {
		return false
	}
	if excluder.ExcludeEntry(walkRoot, path, isDir) {
		return true
	}
	lexicalPath := searchLexicalPath(walkRoot, lexicalRoot, path)
	if lexicalPath == path {
		return false
	}
	return excluder.ExcludeEntry(walkRoot, lexicalPath, isDir)
}
