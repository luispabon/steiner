// Package skill discovers and loads auxiliary skill documents.
package skill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Loader discovers and loads skill documents from disk.
// RootDirs defines the precedence order for skill discovery:
// earlier entries have higher priority when a skill name appears in multiple roots.
type Loader struct {
	RootDirs []string
}

// Skill describes a discovered skill document on disk.
type Skill struct {
	Name     string
	Path     string
	Content  string
	ByteSize int
	Source   string // "project", "user", "global" or root-based identifier
	Summary  string
}

// Discover lists skills available under the configured root directories.
// Skills are scanned in RootDirs precedence order; if a skill name appears
// in multiple roots, only the first occurrence is returned.
// Results are returned sorted by name.
func (l Loader) Discover(ctx context.Context) ([]Skill, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var skills []Skill

	for i, root := range l.RootDirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		root := strings.TrimSpace(root)
		if root == "" {
			continue
		}

		// Determine source label based on root index:
		// 0 -> "project", 1 -> "user", 2 -> "global", >2 -> "root<N>"
		var source string
		switch i {
		case 0:
			source = "project"
		case 1:
			source = "user"
		case 2:
			source = "global"
		default:
			source = fmt.Sprintf("root%d", i)
		}

		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read skills root %s: %w", root, err)
		}

		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !entryIsDir(root, entry) {
				continue
			}
			skill, ok, err := l.discoverEntry(ctx, root, entry.Name(), source, seen)
			if err != nil {
				return nil, err
			}
			if ok {
				skills = append(skills, skill)
				seen[skill.Name] = true
			}
		}
	}

	sort.SliceStable(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})

	return skills, nil
}

// discoverEntry validates a directory entry and builds a Skill if valid.
// Returns (Skill, ok, error) where ok indicates if the entry should be added.
func (l Loader) discoverEntry(ctx context.Context, root, name, source string, seen map[string]bool) (Skill, bool, error) {
	if err := ctx.Err(); err != nil {
		return Skill{}, false, err
	}
	if seen[name] {
		return Skill{}, false, nil
	}
	path := filepath.Join(root, name, "SKILL.md")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return Skill{}, false, nil
		}
		return Skill{}, false, fmt.Errorf("stat skill %s: %w", path, err)
	}
	summary, err := discoverSummary(path)
	if err != nil {
		return Skill{}, false, fmt.Errorf("read skill summary %s: %w", path, err)
	}
	return Skill{Name: name, Path: path, Source: source, Summary: summary}, true, nil
}

// Load reads a single skill document by name.
// Searches RootDirs in order and returns the first match found.
func (l Loader) Load(ctx context.Context, name string) (Skill, error) {
	if err := ctx.Err(); err != nil {
		return Skill{}, err
	}
	if err := validateSkillName(name); err != nil {
		return Skill{}, err
	}

	for _, root := range l.RootDirs {
		if err := ctx.Err(); err != nil {
			return Skill{}, err
		}

		root := strings.TrimSpace(root)
		if root == "" {
			continue
		}

		path := filepath.Join(root, name, "SKILL.md")
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return Skill{}, fmt.Errorf("load skill %s: %w", path, err)
		}

		return Skill{
			Name:     name,
			Path:     path,
			Content:  string(data),
			ByteSize: len(data),
		}, nil
	}

	return Skill{}, fmt.Errorf("skill %q not found in any root", name)
}

// LoadMany reads multiple skill documents by name.
// Searches RootDirs in order for each name and returns the first match.
func (l Loader) LoadMany(ctx context.Context, names []string) ([]Skill, error) {
	skills := make([]Skill, 0, len(names))
	for _, name := range names {
		skill, err := l.Load(ctx, name)
		if err != nil {
			return nil, err
		}
		skills = append(skills, skill)
	}
	return skills, nil
}

// entryIsDir reports whether a directory entry is a directory, following symlinks.
func entryIsDir(root string, entry os.DirEntry) bool {
	if entry.IsDir() {
		return true
	}
	if entry.Type()&os.ModeSymlink != 0 {
		info, err := os.Stat(filepath.Join(root, entry.Name()))
		return err == nil && info.IsDir()
	}
	return false
}

func validateSkillName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("skill name is required")
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid skill name %q", name)
	}
	if filepath.Base(name) != name {
		return fmt.Errorf("invalid skill name %q", name)
	}
	return nil
}

func discoverSummary(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := string(data)
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		lines = skipFrontmatter(lines[1:])
	}
	inCodeFence := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "```") {
			inCodeFence = !inCodeFence
			continue
		}
		if inCodeFence {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimLeft(line, "-*0123456789. ")
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			return line, nil
		}
	}
	return "", nil
}

func skipFrontmatter(lines []string) []string {
	for i, raw := range lines {
		if strings.TrimSpace(raw) == "---" {
			return lines[i+1:]
		}
	}
	return lines
}
