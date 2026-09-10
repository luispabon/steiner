package prompt

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestStaticContextCacheFreezesFileSources(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	projectRoot := t.TempDir()
	skillsRoot := t.TempDir()
	writeStaticContextFixture(t, homeDir, projectRoot, skillsRoot)

	opts := AssemblyOptions{
		HomeDir:                   homeDir,
		ProjectRoot:               projectRoot,
		SkillsRoots:               []string{skillsRoot},
		SkillNames:                []string{"codex"},
		ProjectContextBudgetBytes: 1024,
		ProjectContextExtraFiles:  []string{"README.md"},
		CachedStaticContext:       &StaticContextCache{},
		StaticContextScope:        "session-1",
	}

	first := mustRenderPlannedAssembly(t, opts)
	mustHaveBlockContent(t, first, ContextSourceGlobalAgentsMD, "global v1")
	mustHaveBlockContent(t, first, ContextSourceProjectAgentsMD, "project v1")
	mustHaveBlockContent(t, first, ContextSourceProjectContext, "readme v1")
	mustHaveBlockContent(t, first, ContextSourceSkill, "codex v1")

	mustWrite(t, filepath.Join(homeDir, ".config", "steiner"), "AGENTS.md", "global v2")
	mustWrite(t, projectRoot, "AGENTS.md", "project v2")
	mustWrite(t, projectRoot, "README.md", "readme v2")
	mustWrite(t, filepath.Join(skillsRoot, "codex"), "SKILL.md", "codex v2")

	second := mustRenderPlannedAssembly(t, opts)
	mustHaveBlockContent(t, second, ContextSourceGlobalAgentsMD, "global v1")
	mustHaveBlockContent(t, second, ContextSourceProjectAgentsMD, "project v1")
	mustHaveBlockContent(t, second, ContextSourceProjectContext, "readme v1")
	mustHaveBlockContent(t, second, ContextSourceSkill, "codex v1")
	mustNotHaveBlockContent(t, second, ContextSourceGlobalAgentsMD, "global v2")
	mustNotHaveBlockContent(t, second, ContextSourceProjectAgentsMD, "project v2")
	mustNotHaveBlockContent(t, second, ContextSourceProjectContext, "readme v2")
	mustNotHaveBlockContent(t, second, ContextSourceSkill, "codex v2")
}

func TestStaticContextCacheSkillSetChangeReloadsSkillsOnly(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	projectRoot := t.TempDir()
	skillsRoot := t.TempDir()
	writeStaticContextFixture(t, homeDir, projectRoot, skillsRoot)

	opts := AssemblyOptions{
		HomeDir:             homeDir,
		ProjectRoot:         projectRoot,
		SkillsRoots:         []string{skillsRoot},
		SkillNames:          []string{"codex"},
		CachedStaticContext: &StaticContextCache{},
		StaticContextScope:  "session-1",
	}

	mustRenderPlannedAssembly(t, opts)

	// Agent definitions change and a second skill appears, but only the skill
	// partition is keyed on SkillNames, so AGENTS.md stays frozen.
	mustWrite(t, projectRoot, "AGENTS.md", "project v2")
	mustWrite(t, filepath.Join(skillsRoot, "extra"), "SKILL.md", "extra skill content")

	opts.SkillNames = []string{"extra"}
	assembly := mustRenderPlannedAssembly(t, opts)

	mustHaveBlockContent(t, assembly, ContextSourceProjectAgentsMD, "project v1")
	mustNotHaveBlockContent(t, assembly, ContextSourceProjectAgentsMD, "project v2")
	mustHaveBlockContent(t, assembly, ContextSourceSkill, "extra skill content")
}

func TestStaticContextCacheProjectContextPartitionIndependent(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	mustWrite(t, projectRoot, "AGENTS.md", "agents v1")
	mustWrite(t, projectRoot, "README.md", "readme v1")

	opts := AssemblyOptions{
		ProjectRoot:               projectRoot,
		ProjectContextBudgetBytes: 1024,
		ProjectContextExtraFiles:  []string{"README.md"},
		CachedStaticContext:       &StaticContextCache{},
		StaticContextScope:        "session-1",
	}

	first := mustRenderPlannedAssembly(t, opts)
	mustHaveBlockContent(t, first, ContextSourceProjectContext, "readme v1")

	// Edit AGENTS.md (agents partition) and add a second project-context file
	// (project-context partition). Only the project-context key changes, so the
	// new context file loads while the agents partition stays frozen.
	mustWrite(t, projectRoot, "AGENTS.md", "agents v2")
	mustWrite(t, projectRoot, "NOTES.md", "notes v1")
	opts.ProjectContextExtraFiles = []string{"README.md", "NOTES.md"}

	second := mustRenderPlannedAssembly(t, opts)
	mustHaveBlockContent(t, second, ContextSourceProjectContext, "notes v1")
	mustHaveBlockContent(t, second, ContextSourceProjectAgentsMD, "agents v1")
	mustNotHaveBlockContent(t, second, ContextSourceProjectAgentsMD, "agents v2")
}

func TestStaticContextCacheAbsentStaysAbsent(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()

	opts := AssemblyOptions{
		ProjectRoot:         projectRoot,
		CachedStaticContext: &StaticContextCache{},
		StaticContextScope:  "session-1",
	}

	mustRenderPlannedAssembly(t, opts)

	// A file that was absent when the partition was first loaded stays absent
	// for the life of the cached scope.
	mustWrite(t, projectRoot, "AGENTS.md", "project agents added")

	second := mustRenderPlannedAssembly(t, opts)
	for _, block := range second.Blocks {
		if block.Source == ContextSourceProjectAgentsMD {
			t.Fatalf("unexpected project agents block after cache warm: %q", block.Content)
		}
	}
}

func TestStaticContextCacheScopeChangeReloads(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	mustWrite(t, projectRoot, "AGENTS.md", "agents v1")

	opts := AssemblyOptions{
		ProjectRoot:         projectRoot,
		CachedStaticContext: &StaticContextCache{},
		StaticContextScope:  "s1",
	}

	first := mustRenderPlannedAssembly(t, opts)
	mustHaveBlockContent(t, first, ContextSourceProjectAgentsMD, "agents v1")

	mustWrite(t, projectRoot, "AGENTS.md", "agents v2")

	opts.StaticContextScope = "s2"
	second := mustRenderPlannedAssembly(t, opts)
	mustHaveBlockContent(t, second, ContextSourceProjectAgentsMD, "agents v2")
}

func TestStaticContextNilCacheObservesEdits(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	mustWrite(t, projectRoot, "AGENTS.md", "agents v1")

	opts := AssemblyOptions{ProjectRoot: projectRoot}

	first := mustRenderPlannedAssembly(t, opts)
	mustHaveBlockContent(t, first, ContextSourceProjectAgentsMD, "agents v1")

	mustWrite(t, projectRoot, "AGENTS.md", "agents v2")

	second := mustRenderPlannedAssembly(t, opts)
	mustHaveBlockContent(t, second, ContextSourceProjectAgentsMD, "agents v2")
}

func TestStaticContextCacheConcurrentAssemble(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	projectRoot := t.TempDir()
	skillsRoot := t.TempDir()
	writeStaticContextFixture(t, homeDir, projectRoot, skillsRoot)

	opts := AssemblyOptions{
		HomeDir:                   homeDir,
		ProjectRoot:               projectRoot,
		SkillsRoots:               []string{skillsRoot},
		SkillNames:                []string{"codex"},
		ProjectContextBudgetBytes: 1024,
		ProjectContextExtraFiles:  []string{"README.md"},
		CachedStaticContext:       &StaticContextCache{},
		StaticContextScope:        "session-1",
	}

	const goroutines = 8
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			assembler, err := newAssembler(opts)
			if err != nil {
				errs[i] = err
				return
			}
			_, errs[i] = assembler.Assemble(context.Background())
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d error = %v", i, err)
		}
	}
}

// writeStaticContextFixture writes the base file-backed static sources used by
// the StaticContextCache tests.
func writeStaticContextFixture(t *testing.T, homeDir, projectRoot, skillsRoot string) {
	t.Helper()

	mustWrite(t, filepath.Join(homeDir, ".config", "steiner"), "AGENTS.md", "global v1")
	mustWrite(t, projectRoot, "AGENTS.md", "project v1")
	mustWrite(t, projectRoot, "README.md", "readme v1")
	mustWrite(t, filepath.Join(skillsRoot, "codex"), "SKILL.md", "codex v1")
}

func mustHaveBlockContent(t *testing.T, assembly Assembly, source ContextSource, substr string) {
	t.Helper()

	for _, block := range assembly.Blocks {
		if block.Source == source && strings.Contains(block.Content, substr) {
			return
		}
	}
	t.Fatalf("no %s block containing %q", source, substr)
}

func mustNotHaveBlockContent(t *testing.T, assembly Assembly, source ContextSource, substr string) {
	t.Helper()

	for _, block := range assembly.Blocks {
		if block.Source == source && strings.Contains(block.Content, substr) {
			t.Fatalf("%s block unexpectedly contains %q", source, substr)
		}
	}
}
