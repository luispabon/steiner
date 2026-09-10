package prompt

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/luispabon/steiner/internal/skill"
)

// StaticContextCache memoizes the file-backed static prompt sources: global and
// project AGENTS.md, project context files, and skill content. A caller that
// assembles repeatedly over one session passes a pointer via
// AssemblyOptions.CachedStaticContext so those files are read from disk once and
// the assembled prefix stays byte-stable across turns.
//
// Each partition is cached independently and keyed by the inputs that select its
// files, so changing the enabled skills reloads only the skills partition and
// leaves the AGENTS.md and project-context snapshots intact. Changing
// AssemblyOptions.StaticContextScope (the session identity) drops every partition
// and reloads on next use.
//
// The zero value is ready to use and safe for concurrent use.
type StaticContextCache struct {
	mu           sync.Mutex
	scopeSet     bool
	scope        string
	agentsCache  staticContextEntry
	projectCache staticContextEntry
	skillsCache  staticContextEntry
}

type staticContextEntry struct {
	loaded bool
	key    string
	blocks []ContextBlock
}

// beginScope drops cached partitions when the assembly scope changes so a new
// session reloads every file-backed source.
func (c *StaticContextCache) beginScope(scope string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.scopeSet && c.scope == scope {
		return
	}
	c.agentsCache = staticContextEntry{}
	c.projectCache = staticContextEntry{}
	c.skillsCache = staticContextEntry{}
	c.scope = scope
	c.scopeSet = true
}

func (c *StaticContextCache) agents(opts AssemblyOptions) ([]ContextBlock, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	globalPath, projectPath := agentPaths(opts)
	key := strings.Join([]string{globalPath, projectPath}, "\x00")
	return c.agentsCache.ensure(key, func() ([]ContextBlock, error) {
		return loadAgents(globalPath, projectPath)
	})
}

func (c *StaticContextCache) projectContext(opts AssemblyOptions, policy AssemblyPolicy) ([]ContextBlock, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := strings.Join([]string{
		opts.ProjectRoot,
		strconv.Itoa(policy.Budgets.ProjectContextBytes),
		strings.Join(opts.ProjectContextExtraFiles, "\x00"),
		strings.Join(opts.ProjectContextIgnoreFiles, "\x00"),
	}, "\x00")
	return c.projectCache.ensure(key, func() ([]ContextBlock, error) {
		return gatherProjectContext(ProjectContextOptions{
			Root:        opts.ProjectRoot,
			BudgetBytes: policy.Budgets.ProjectContextBytes,
			ExtraFiles:  opts.ProjectContextExtraFiles,
			IgnoreFiles: opts.ProjectContextIgnoreFiles,
		})
	})
}

// skills loads or returns the cached skill blocks. The skills key covers the
// skill roots and the enabled skill names but not SkillsBundledFS, which cannot
// be compared as an fs.FS: a cache instance must therefore be paired with a
// single SkillsBundledFS for the lifetime of its scope, and callers with a
// different bundled FS must use a separate cache.
func (c *StaticContextCache) skills(ctx context.Context, opts AssemblyOptions) ([]ContextBlock, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := strings.Join([]string{
		strings.Join(skillRoots(opts), "\x00"),
		strings.Join(opts.SkillNames, "\x00"),
	}, "\x00")
	return c.skillsCache.ensure(key, func() ([]ContextBlock, error) {
		return loadSkillBlocksForOptions(ctx, opts)
	})
}

// ensure returns cached blocks for key, loading once on empty or key change. A
// failed load is not cached.
func (e *staticContextEntry) ensure(key string, load func() ([]ContextBlock, error)) ([]ContextBlock, error) {
	if e.loaded && e.key == key {
		return e.blocks, nil
	}
	blocks, err := load()
	if err != nil {
		return nil, err
	}
	e.blocks = blocks
	e.key = key
	e.loaded = true
	return blocks, nil
}

func loadSkillBlocksForOptions(ctx context.Context, opts AssemblyOptions) ([]ContextBlock, error) {
	skillBlocks, err := loadSkillBlocks(ctx, skill.Loader{RootDirs: skillRoots(opts), BundledFS: opts.SkillsBundledFS}, opts.SkillNames)
	if err != nil {
		return nil, err
	}
	if len(skillBlocks) > 0 {
		skillBlocks = append([]ContextBlock{skillFramingBlock(opts.SkillNames)}, skillBlocks...)
	}
	return skillBlocks, nil
}
