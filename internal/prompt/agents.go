package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultGlobalAgentsPath returns the default global AGENTS.md location.
func DefaultGlobalAgentsPath(homeDir string) string {
	if homeDir == "" {
		return ""
	}
	return filepath.Join(homeDir, ".config", "steiner", "AGENTS.md")
}

func loadAgents(globalPath, projectPath string) ([]ContextBlock, error) {
	blocks := make([]ContextBlock, 0, 2)

	if block, err := loadMarkdownFile(globalPath, ContextSourceGlobalAgentsMD); err != nil {
		return nil, err
	} else if block != nil {
		blocks = append(blocks, *block)
	}

	if block, err := loadMarkdownFile(projectPath, ContextSourceProjectAgentsMD); err != nil {
		return nil, err
	} else if block != nil {
		blocks = append(blocks, *block)
	}

	return blocks, nil
}

func loadMarkdownFile(path string, source ContextSource) (*ContextBlock, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("load %s: %w", path, err)
	}

	return &ContextBlock{
		Source:   source,
		Path:     path,
		Content:  string(data),
		ByteSize: len(data),
	}, nil
}
