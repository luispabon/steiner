package prompt

import (
	"context"
	"path/filepath"

	"github.com/luispabon/steiner/internal/skill"
)

// DefaultSkillsRoot returns the default skill installation directory.
func DefaultSkillsRoot(homeDir string) string {
	if homeDir == "" {
		return ""
	}
	return filepath.Join(homeDir, ".config", "steiner", "skills")
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
