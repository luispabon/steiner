package builtin

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/luispabon/steiner/internal/tool"
)

// searchSymlinkScope decides whether a search walk may treat a symlink entry as
// the regular file it points at. Directory links are never followed: the entry
// either resolves to a regular file the search may read, or it is skipped. The
// link name stays the path the caller sees; only the read path is resolved.
//
// roots holds the canonical directories a target must stay inside: the walk
// root and, when the policy names one, the workspace root.
type searchSymlinkScope struct {
	policy      *tool.PathPolicy
	excluder    *tool.WorkspaceExcluder
	walkRoot    string
	lexicalRoot string
	roots       []string
}

// newSearchSymlinkScope builds the scope for one walk, canonicalizing the
// containment roots once instead of per entry.
func newSearchSymlinkScope(policy *tool.PathPolicy, excluder *tool.WorkspaceExcluder, walkRoot, lexicalRoot string) searchSymlinkScope {
	roots := []string{canonicalSearchPath(walkRoot)}
	if policy != nil {
		if root := canonicalSearchPath(policy.Root()); root != "" {
			roots = append(roots, root)
		}
	}
	return searchSymlinkScope{
		policy:      policy,
		excluder:    excluder,
		walkRoot:    walkRoot,
		lexicalRoot: lexicalRoot,
		roots:       roots,
	}
}

// searchSymlinkEntry reports whether the walk entry is a symlink. WalkDir does
// not descend into links, so an entry typed as a link is always a candidate for
// target resolution.
func searchSymlinkEntry(d fs.DirEntry) bool {
	return d.Type()&fs.ModeSymlink != 0
}

// eligible reports whether a walk entry may be searched or listed: every entry
// that is not a symlink passes, and a symlink passes only when its target is an
// eligible regular file.
func (s searchSymlinkScope) eligible(d fs.DirEntry, path string) bool {
	if !searchSymlinkEntry(d) {
		return true
	}
	_, ok := s.target(path)
	return ok
}

// target resolves the symlink at path and reports the canonical regular file to
// read. It reports false for every entry the walk must not follow: a dangling
// link, a link to a directory or special file, a target outside the walk root
// and workspace, a target denied by blocked_paths, and a target hidden by
// configured or built-in exclusions. The link content is resolved through
// policy first, so the sandbox /tmp rewrite maps a link to /tmp/x onto the
// sandbox copy of x instead of the host file behind it.
func (s searchSymlinkScope) target(path string) (string, bool) {
	dest, err := os.Readlink(path)
	if err != nil {
		return "", false
	}
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(filepath.Dir(path), dest)
	}
	target := filepath.Clean(dest)
	if s.policy != nil {
		resolved, err := s.policy.ResolveReadPath(target)
		if err != nil {
			return "", false
		}
		target = resolved
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", false
	}
	// The canonical target can differ from the path policy checked: a link chain
	// or a symlinked parent can land somewhere blocked. Re-check so the walk
	// fails closed.
	if s.policy != nil && searchPathDenied(s.policy, resolved) {
		return "", false
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	if !s.contains(resolved) {
		return "", false
	}
	if excludeSearchEntry(s.excluder, s.policy, s.walkRoot, s.lexicalRoot, resolved, false) {
		return "", false
	}
	return resolved, true
}

// contains reports whether the canonical target stays inside one of the
// canonical containment roots. Containment is checked on the resolved target,
// so a link cannot escape the walk root or workspace through a parent symlink.
func (s searchSymlinkScope) contains(target string) bool {
	for _, root := range s.roots {
		if root != "" && tool.PathWithinRoot(root, target) {
			return true
		}
	}
	return false
}

// canonicalSearchPath resolves every symlink component of path so containment
// compares like with like. A path that cannot be resolved is returned unchanged.
func canonicalSearchPath(path string) string {
	if path == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}
