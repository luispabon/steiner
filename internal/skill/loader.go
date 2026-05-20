// Package skill discovers and loads auxiliary skill documents.
package skill

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Loader discovers and loads skill documents from disk.
// RootDirs defines the precedence order for skill discovery:
// earlier entries have higher priority when a skill name appears in multiple roots.
// BundledFS, when set, is walked first; bundled skills always win over filesystem skills.
type Loader struct {
	RootDirs  []string
	BundledFS fs.FS
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

	bundled, bundledNames, err := l.discoverBundled(ctx)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(bundledNames))
	for name := range bundledNames {
		seen[name] = true
	}

	fsSkills, err := l.discoverFromRoots(ctx, bundledNames, seen)
	if err != nil {
		return nil, err
	}

	bundled = append(bundled, fsSkills...)
	sort.SliceStable(bundled, func(i, j int) bool {
		return bundled[i].Name < bundled[j].Name
	})
	return bundled, nil
}

func (l Loader) discoverBundled(ctx context.Context) ([]Skill, map[string]bool, error) {
	bundledNames := make(map[string]bool)
	if l.BundledFS == nil {
		return nil, bundledNames, nil
	}
	_ = ctx // reserved for future cancellation within the walk
	entries, err := fs.ReadDir(l.BundledFS, ".")
	if err != nil {
		return nil, nil, fmt.Errorf("read bundled skills: %w", err)
	}
	var skills []Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		path := name + "/SKILL.md"
		data, err := fs.ReadFile(l.BundledFS, path)
		if err != nil {
			continue
		}
		content := string(data)
		skills = append(skills, Skill{
			Name:    name,
			Path:    path,
			Source:  "bundled",
			Summary: discoverSummaryFromContent(content),
		})
		bundledNames[name] = true
	}
	return skills, bundledNames, nil
}

func (l Loader) discoverFromRoots(ctx context.Context, bundledNames, seen map[string]bool) ([]Skill, error) {
	var skills []Skill
	for i, root := range l.RootDirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		source := rootSource(i)
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
			name := entry.Name()
			if bundledNames[name] {
				slog.Warn("skill shadowed by bundled skill", "name", name)
				continue
			}
			skill, ok, err := l.discoverEntry(ctx, root, name, source, seen)
			if err != nil {
				return nil, err
			}
			if ok {
				skills = append(skills, skill)
				seen[skill.Name] = true
			}
		}
	}
	return skills, nil
}

// rootSource maps a RootDirs index to its source label.
// 0 -> "project", 1 -> "user", 2 -> "global", >2 -> "root<N>"
func rootSource(i int) string {
	switch i {
	case 0:
		return "project"
	case 1:
		return "user"
	case 2:
		return "global"
	default:
		return fmt.Sprintf("root%d", i)
	}
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
// Checks BundledFS first, then searches RootDirs in order.
func (l Loader) Load(ctx context.Context, name string) (Skill, error) {
	if err := ctx.Err(); err != nil {
		return Skill{}, err
	}
	if err := validateSkillName(name); err != nil {
		return Skill{}, err
	}

	if l.BundledFS != nil {
		path := name + "/SKILL.md"
		data, err := fs.ReadFile(l.BundledFS, path)
		if err == nil {
			content := string(data)
			return Skill{
				Name:     name,
				Path:     path,
				Content:  content,
				ByteSize: len(data),
				Source:   "bundled",
				Summary:  discoverSummaryFromContent(content),
			}, nil
		}
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
	return discoverSummaryFromContent(string(data)), nil
}

func discoverSummaryFromContent(content string) string {
	_, desc := parseFrontmatter(content)
	if desc != "" {
		return desc
	}
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
			return line
		}
	}
	return ""
}

func skipFrontmatter(lines []string) []string {
	for i, raw := range lines {
		if strings.TrimSpace(raw) == "---" {
			return lines[i+1:]
		}
	}
	return lines
}
