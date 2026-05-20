package skill

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// parseFrontmatter extracts name and description from YAML frontmatter (--- ... ---).
// Returns empty strings if no frontmatter is present, fields are absent, or YAML is malformed.
func parseFrontmatter(content string) (name, description string) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", ""
	}
	var blockLines []string
	found := false
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			found = true
			break
		}
		blockLines = append(blockLines, line)
	}
	if !found {
		return "", ""
	}
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(strings.Join(blockLines, "\n")), &fm); err != nil {
		return "", ""
	}
	return fm.Name, fm.Description
}
