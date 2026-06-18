package oneshot

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed prompts/*.md
var promptFiles embed.FS

// LoadPrompt returns the embedded orchestration prompt for the requested phase.
func LoadPrompt(phase Phase) (string, error) {
	return LoadPromptByName(string(phase))
}

// LoadPromptByName returns the embedded orchestration prompt for the requested phase name.
func LoadPromptByName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("load prompt: empty phase")
	}

	data, err := fs.ReadFile(promptFiles, "prompts/"+name+".md")
	if err != nil {
		return "", fmt.Errorf("load prompt %q: %w", name, err)
	}
	return string(data), nil
}
