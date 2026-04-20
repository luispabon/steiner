package skill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Loader struct {
	RootDir string
}

type Skill struct {
	Name     string
	Path     string
	Content  string
	ByteSize int
}

func (l Loader) Discover(ctx context.Context) ([]Skill, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root := strings.TrimSpace(l.RootDir)
	if root == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read skills root %s: %w", root, err)
	}

	skills := make([]Skill, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		path := filepath.Join(root, name, "SKILL.md")
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("stat skill %s: %w", path, err)
		}
		skills = append(skills, Skill{Name: name, Path: path})
	}

	sort.SliceStable(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})

	return skills, nil
}

func (l Loader) Load(ctx context.Context, name string) (Skill, error) {
	if err := ctx.Err(); err != nil {
		return Skill{}, err
	}
	root := strings.TrimSpace(l.RootDir)
	if root == "" {
		return Skill{}, fmt.Errorf("skills root is required")
	}
	if err := validateSkillName(name); err != nil {
		return Skill{}, err
	}

	path := filepath.Join(root, name, "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Skill{}, fmt.Errorf("skill %q not found under %s", name, root)
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
