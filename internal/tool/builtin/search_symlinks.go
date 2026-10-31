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
// walkCanonical and workspace are the canonical directories a target must stay
// inside, while workspaceLexical is the workspace path exclusions were
// configured against. workspace is empty when the policy names no root, and
// virtualTmp records that the caller named the virtual /tmp tree.
type searchSymlinkScope struct {
	policy           *tool.PathPolicy
	excluder         *tool.WorkspaceExcluder
	walkRoot         string
	lexicalRoot      string
	walkCanonical    string
	workspace        string
	workspaceLexical string
	virtualTmp       bool
}

// newSearchSymlinkScope builds the scope for one walk, canonicalizing the
// containment roots once instead of per entry.
func newSearchSymlinkScope(policy *tool.PathPolicy, excluder *tool.WorkspaceExcluder, walkRoot, lexicalRoot string) searchSymlinkScope {
	scope := searchSymlinkScope{
		policy:        policy,
		excluder:      excluder,
		walkRoot:      walkRoot,
		lexicalRoot:   lexicalRoot,
		walkCanonical: canonicalSearchPath(walkRoot),
	}
	if policy != nil {
		scope.workspace = canonicalSearchPath(policy.Root())
		scope.workspaceLexical = policy.Root()
		scope.virtualTmp = policy.VirtualTmpRequest()
	}
	return scope
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
	if s.excluded(resolved) {
		return "", false
	}
	return resolved, true
}

// excluded reports whether exclusions hide the resolved link target. A target
// inside the walk root is matched against the walk root's own view, so an
// explicit root below a built-in directory name stays searchable. A target
// outside the walk root is reachable only through the link, and matching it
// against the walk root leaves no relative path for built-in directory names
// such as node_modules or .git to match, so those targets are matched against
// the workspace view instead.
func (s searchSymlinkScope) excluded(resolved string) bool {
	if excludeSearchEntry(s.excluder, s.policy, s.walkRoot, s.lexicalRoot, resolved, false) {
		return true
	}
	if s.workspace == "" || pathWithinCanonicalRoot(s.walkCanonical, resolved) {
		return false
	}
	base, target := s.externalExclusionPath(resolved)
	return excludeSearchEntry(s.excluder, s.policy, base, base, target, false)
}

// externalExclusionPath returns the base and path an out-of-walk target is
// matched against. A virtual /tmp request anchors at the virtual /tmp root: the
// sandbox storage tree sits inside the workspace, and the workspace view leaves
// a /tmp target no relative path for configured patterns to match. Otherwise
// the canonical target is mapped back onto the lexical workspace path, because
// configured exclusions are anchored where the caller configured them and that
// path is not canonical when the workspace root is itself a symlink.
func (s searchSymlinkScope) externalExclusionPath(resolved string) (string, string) {
	if s.virtualTmp && s.policy.IsSandboxTmpPath(resolved) {
		return virtualTmpRoot, s.policy.DisplayPath(resolved)
	}
	if rel, ok := relativeToCanonicalRoot(s.workspace, resolved); ok && s.workspaceLexical != "" {
		return s.workspaceLexical, filepath.Join(s.workspaceLexical, rel)
	}
	return s.workspace, resolved
}

// relativeToCanonicalRoot returns the relative path from root to target when
// target is contained by root.
func relativeToCanonicalRoot(root, target string) (string, bool) {
	if !pathWithinCanonicalRoot(root, target) {
		return "", false
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", false
	}
	return rel, true
}

// contains reports whether the canonical target stays inside one of the
// canonical containment roots. Containment is checked on the resolved target,
// so a link cannot escape the walk root or workspace through a parent symlink.
func (s searchSymlinkScope) contains(target string) bool {
	return pathWithinCanonicalRoot(s.walkCanonical, target) || pathWithinCanonicalRoot(s.workspace, target)
}

// virtualTmpRoot is the caller-visible path the sandbox /tmp rewrite maps onto.
const virtualTmpRoot = "/tmp"

// pathWithinCanonicalRoot reports whether target is inside root, treating an
// empty root as no root at all.
func pathWithinCanonicalRoot(root, target string) bool {
	return root != "" && tool.PathWithinRoot(root, target)
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
