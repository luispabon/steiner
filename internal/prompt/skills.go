package prompt

import (
	"context"
	"path/filepath"
	"strings"

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

func skillFramingBlock(names []string) ContextBlock {
	content := "## Active Skills\n\n" +
		"The user has explicitly enabled the skills below for this session. Each skill defines a workflow you must follow when the user's request matches its domain. Skills govern your working process - they do not override project instructions (CLAUDE.md, AGENTS.md) or tool policy. The user can override a skill's workflow with an explicit instruction.\n\n" +
		"Enabled: " + strings.Join(names, ", ")

	return ContextBlock{
		Source:   ContextSourceSkill,
		Content:  content,
		ByteSize: len(content),
	}
}
