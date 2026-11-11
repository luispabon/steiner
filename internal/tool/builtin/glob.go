package builtin

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/tool"
)

// NewGlobTool creates a ToolDef for the glob tool backed by a custom
// filepath.WalkDir-based walker that respects path exclusions.
func NewGlobTool(env Env) tool.ToolDef {
	return tool.ToolDef{
		Name:            "glob",
		ParallelSafe:    true,
		Description:     "List files and directories under path matching a glob pattern. Directories end in \"/\". Use limit and offset to page through large result sets.",
		ParameterSchema: GlobSchema(),
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[GlobInput](input)
			if err != nil {
				return nil, fmt.Errorf("glob: %w", err)
			}

			normalizeGlob(&in)

			if in.Path == "" {
				in.Path = "."
			}

			policy := env.PathPolicy.WithVirtualTmpRequest(in.Path)
			absPath, err := policy.ResolveReadPath(in.Path)
			if err != nil {
				return nil, fmt.Errorf("glob: %w", err)
			}

			excluder := tool.WorkspaceExcluder{}
			if env.Excluder != nil {
				excluder = *env.Excluder
			}

			walkRoot, err := resolveSearchRoot(absPath, &policy)
			if err != nil {
				return nil, fmt.Errorf("glob: %w", err)
			}

			result, err := globWalk(ctx, globWalkParams{
				root:        walkRoot,
				lexicalRoot: absPath,
				pattern:     in.Pattern,
				excluder:    excluder,
				policy:      &policy,
				limit:       in.Limit,
				offset:      in.Offset,
			})
			if err != nil {
				return nil, fmt.Errorf("glob: %w", err)
			}
			return result, nil
		},
	}
}

// globWalkParams configures one glob walk.
type globWalkParams struct {
	// root is the resolved walk root and lexicalRoot the path the caller named,
	// so configured exclusions are validated against both forms when the root is
	// a symlink.
	root        string
	lexicalRoot string
	pattern     string
	excluder    tool.WorkspaceExcluder
	policy      *tool.PathPolicy
	limit       int
	offset      int
}

// globWalk pages through the directory tree rooted at root and returns the
// entries matching pattern (using gobwas/glob for full glob semantics including
// ** and {a,b} alternation). Files match on their root-relative slash path. A
// directory matches when a pattern matches its relative path or that path with
// a trailing slash, and is returned with the trailing slash. The walk root is
// never returned. Excluded directories are not traversed. Every entry is
// validated through PathPolicy before it is traversed or matched, so denied
// directories are pruned and denied entries fail closed. Listing never opens an
// entry: every symlink below the root is returned by its lexical name without a
// trailing slash and is never followed or descended, and special files such as
// devices, pipes, and sockets are returned by name. Only an entry whose own path
// or resolved symlink target is blocked is omitted, and a root passed as a
// symlink is walked through its target. Results follow filepath.WalkDir order.
// The walk stops once the page and its next-offset signal are settled.
func globWalk(ctx context.Context, p globWalkParams) (Result, error) {
	if p.limit <= 0 {
		p.limit = defaultGlobLimit
	}
	if p.offset < 0 {
		p.offset = 0
	}

	matcher, err := compileGlobMatcher(p.pattern)
	if err != nil {
		return Result{}, err
	}
	if err := globRootDirectory(p.root, p.lexicalRoot); err != nil {
		return Result{}, err
	}

	walker := &globWalker{
		params:  p,
		matcher: matcher,
		page:    make([]string, 0, p.limit),
	}

	if err := filepath.WalkDir(p.root, func(path string, d fs.DirEntry, err error) error {
		return walker.visit(ctx, path, d, err)
	}); err != nil {
		return Result{}, err
	}
	return Result{Output: strings.Join(walker.page, "\n"), NextOffset: walker.nextOffset}, nil
}

// globWalker holds the matching and paging state of one glob walk.
type globWalker struct {
	params     globWalkParams
	matcher    globMatcher
	page       []string
	matched    int
	nextOffset int
}

// visit is the filepath.WalkDir callback: it validates one entry, records a
// match, and decides whether the walk may stop or skip a subtree.
func (w *globWalker) visit(ctx context.Context, path string, d fs.DirEntry, err error) error {
	if err != nil {
		// Skip directories we don't have permission to enter, but propagate
		// other errors (e.g. nonexistent root).
		_, walkErr := walkDirEntryError(path, w.params.root, d, err)
		return walkErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	rel, err := filepath.Rel(w.params.root, path)
	if err != nil {
		return err
	}
	// The walk root is never matched or returned.
	if rel == "." {
		return nil
	}

	if w.denied(path, d) {
		return globSkipEntry(d)
	}

	isDir := d.IsDir()
	relSlash := filepath.ToSlash(rel)
	if line, ok := w.matcher.matchEntry(relSlash, isDir); ok && w.collect(line) {
		w.nextOffset = w.params.offset + w.params.limit
		return filepath.SkipAll
	}
	if isDir && w.matcher.prunes(relSlash) {
		return filepath.SkipDir
	}
	return nil
}

// denied reports whether exclusions or path policy hide the entry. Exclusions
// are workspace-anchored: configured paths and patterns compare against the
// workspace, and built-in directory names are matched relative to the walk root
// so ancestors of the root (e.g. /srv/build/proj, .steiner/worktrees/x) never
// blind the walk. A root reached through a symlink is validated in both its
// resolved and lexical form so configured exclusions keep applying. A blocked
// path hides the entry, including a symlink whose resolved target is blocked;
// special files are listed because listing never opens them.
func (w *globWalker) denied(path string, d fs.DirEntry) bool {
	if excludeSearchEntry(&w.params.excluder, w.params.policy, w.params.root, w.params.lexicalRoot, path, d.IsDir()) {
		return true
	}
	return searchListingDenied(w.params.policy, path)
}

// collect adds a match to the page and reports whether the walk can stop
// because the page is full and a following page exists. Entries before the
// requested offset are counted but not returned.
func (w *globWalker) collect(line string) bool {
	if w.matched >= w.params.offset {
		if len(w.page) >= w.params.limit {
			return true
		}
		w.page = append(w.page, line)
	}
	w.matched++
	return false
}

// globRootDirectory rejects a search root that is not a directory. A missing
// root keeps the filesystem error so callers see it as they did before.
func globRootDirectory(root, lexicalRoot string) error {
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return nil
	}
	display := lexicalRoot
	if display == "" {
		display = root
	}
	return fmt.Errorf("path %s is not a directory", display)
}

// globSkipEntry returns the walk outcome for an entry filtered out before the
// pattern match: an excluded or denied directory is pruned, and every other
// entry is skipped.
func globSkipEntry(d fs.DirEntry) error {
	if d.IsDir() {
		return filepath.SkipDir
	}
	return nil
}
