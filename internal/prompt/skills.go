package prompt

import (
	"context"
	"path/filepath"

	"github.com/luispabon/steiner/internal/skill"
)

// SkillRoots returns the skill discovery directories in search order.
// Returns [projectRoot+"/.steiner/skills", homeDir+"/.config/steiner/skills", homeDir+"/.agents/skills"].
// Only non-empty paths are included. projectRoot can be empty (skipped if so).
func SkillRoots(homeDir, projectRoot string) []string {
	var roots []string
	if projectRoot != "" {
		roots = append(roots, filepath.Join(projectRoot, ".steiner", "skills"))
	}
	if homeDir != "" {
		roots = append(roots, filepath.Join(homeDir, ".config", "steiner", "skills"))
		roots = append(roots, filepath.Join(homeDir, ".agents", "skills"))
	}
	return roots
}

func loadSkillBlocks(ctx context.Context, loader skill.Loader, names []string) ([]ContextBlock, error) {
	if len(names) == 0 {
		return nil, nil
	}

	skills, err := loader.LoadMany(ctx, names)
	if err != nil {
		return nil, err
	}

	blocks := make([]ContextBlock, 0, len(skills))
	for _, loaded := range skills {
		blocks = append(blocks, ContextBlock{
			Source:   ContextSourceSkill,
			Path:     loaded.Path,
			Content:  loaded.Content,
			ByteSize: loaded.ByteSize,
		})
	}
	return blocks, nil
}
