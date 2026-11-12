package prompt

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/skill"
	bundledskills "github.com/luispabon/steiner/skills"
)

// TestDefaultSkillBudgetCoversBundledSkills guards the default skill budget
// against static assembly of the bundled skill corpus: the active-skills
// framing block plus every bundled skill body must fit the default policy's
// skill budget, and every body must fit the per-skill content cap. The budget
// bounds this static stack, not interactive skill activation envelopes, which
// carry their own framing text per activated skill.
func TestDefaultSkillBudgetCoversBundledSkills(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loader := skill.Loader{BundledFS: bundledskills.FS}
	discovered, err := loader.Discover(ctx)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(discovered) == 0 {
		t.Fatal("Discover() found no bundled skills")
	}

	names := make([]string, 0, len(discovered))
	for _, discoveredSkill := range discovered {
		names = append(names, discoveredSkill.Name)
	}

	loaded, err := loader.LoadMany(ctx, names)
	if err != nil {
		t.Fatalf("LoadMany() error = %v", err)
	}

	framingBytes := len(skillFramingBlock(names).Content)
	bodyBytes := 0
	for _, loadedSkill := range loaded {
		if loadedSkill.ByteSize > SkillContentCapBytes {
			t.Errorf("bundled skill %q body is %d bytes, over the %d-byte content cap",
				loadedSkill.Name, loadedSkill.ByteSize, SkillContentCapBytes)
		}
		bodyBytes += len(loadedSkill.Content)
	}
	total := framingBytes + bodyBytes

	budget := DefaultAssemblyPolicy().Budgets.SkillBytes
	if budget != defaultSkillBudgetBytes {
		t.Fatalf("default policy skill budget = %d, want defaultSkillBudgetBytes = %d", budget, defaultSkillBudgetBytes)
	}
	if total > budget {
		t.Fatalf("bundled skills total %d bytes (framing %d + bodies %d), skill budget %d, deficit %d; raise defaultSkillBudgetBytes",
			total, framingBytes, bodyBytes, budget, total-budget)
	}

	// Cross-check the real assembler against the same options the bundled-skill
	// fit check uses, so this guard tracks production accounting.
	assembly, err := Assemble(ctx, AssemblyOptions{
		SkillsBundledFS: bundledskills.FS,
		SkillNames:      names,
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	assembledBytes := 0
	for _, block := range assembly.Blocks {
		if block.Source == ContextSourceSkill {
			assembledBytes += block.ByteSize
		}
	}
	if assembledBytes != total {
		t.Fatalf("assembled skill bytes = %d, want %d (framing plus every bundled skill body)", assembledBytes, total)
	}
}
