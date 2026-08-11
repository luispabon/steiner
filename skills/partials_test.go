package skills_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/skill"
	"github.com/luispabon/steiner/skills"
)

// includePrefix and includeSuffix mirror the directive markers parsed by
// skills/gen/main.go's parseDirective. Duplicated here (rather than
// imported) because skills/gen is package main and cannot be imported;
// skills/gen/main.go remains the source of truth for the include syntax.
const (
	includePrefix = "<!-- include: "
	includeSuffix = " -->"
)

// parseDirective reports whether line is an include directive, returning
// its referenced partial path. Mirrors parseDirective in skills/gen/main.go.
func parseDirective(line string) (path string, isDirective bool) {
	line = strings.TrimSuffix(line, "\n")
	if len(line) < len(includePrefix)+len(includeSuffix) {
		return "", false
	}
	if !strings.HasPrefix(line, includePrefix) || !strings.HasSuffix(line, includeSuffix) {
		return "", false
	}
	return line[len(includePrefix) : len(line)-len(includeSuffix)], true
}

// skillDirs returns the sorted list of skill directory names under
// skills/ (those containing a SKILL.md file), excluding partials/gen/testdata.
func skillDirs(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read skills dir: %v", err)
	}

	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(entry.Name(), "SKILL.md")); err != nil {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

// srcDirectives parses every skills/*/SKILL.md.src file and returns a map
// from skill name to the set of partial paths it references.
func srcDirectives(t *testing.T) map[string]map[string]bool {
	t.Helper()

	refs := make(map[string]map[string]bool)
	for _, name := range skillDirs(t) {
		srcPath := filepath.Join(name, "SKILL.md.src")
		data, err := os.ReadFile(srcPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read %s: %v", srcPath, err)
		}
		set := make(map[string]bool)
		for _, line := range strings.Split(string(data), "\n") {
			if path, ok := parseDirective(line); ok {
				set[path] = true
			}
		}
		refs[name] = set
	}
	return refs
}

func TestNoOrphanedPartials(t *testing.T) {
	refs := srcDirectives(t)

	referenced := make(map[string]bool)
	for _, set := range refs {
		for path := range set {
			referenced[path] = true
		}
	}

	entries, err := os.ReadDir("partials")
	if err != nil {
		t.Fatalf("read partials dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("no partials found under skills/partials/")
	}
	if len(referenced) == 0 {
		t.Fatalf("no include directives parsed from any SKILL.md.src")
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := "partials/" + entry.Name()
		if !referenced[path] {
			t.Errorf("orphaned partial: %s is not referenced by any skills/*/SKILL.md.src", path)
		}
	}
}

func TestNoSkillInlinesUnreferencedPartial(t *testing.T) {
	refs := srcDirectives(t)

	partialEntries, err := os.ReadDir("partials")
	if err != nil {
		t.Fatalf("read partials dir: %v", err)
	}
	if len(partialEntries) == 0 {
		t.Fatalf("no partials found under skills/partials/")
	}

	partialContents := make(map[string]string)
	for _, entry := range partialEntries {
		if entry.IsDir() {
			continue
		}
		path := "partials/" + entry.Name()
		data, err := os.ReadFile(filepath.Join("partials", entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		partialContents[path] = strings.TrimSuffix(string(data), "\n")
	}

	names := skillDirs(t)
	if len(names) == 0 {
		t.Fatalf("no skill directories found under skills/")
	}

	for _, name := range names {
		skillMD, err := os.ReadFile(filepath.Join(name, "SKILL.md"))
		if err != nil {
			t.Fatalf("read %s/SKILL.md: %v", name, err)
		}
		content := string(skillMD)

		for path, partial := range partialContents {
			if refs[name][path] {
				continue
			}
			if partial == "" {
				continue
			}
			if strings.Contains(content, partial) {
				t.Errorf("skill %q re-inlines partial %q content verbatim without referencing it via SKILL.md.src", name, path)
			}
		}
	}
}

func TestPartialsNotDiscoveredAsSkill(t *testing.T) {
	loader := skill.Loader{BundledFS: skills.FS}

	discovered, err := loader.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	var got []string
	for _, s := range discovered {
		got = append(got, s.Name)
	}

	// The //go:embed */SKILL.md pattern in skills/embed.go only embeds
	// directories that contain a SKILL.md file, so partials/, gen/, and
	// testdata/ are never part of skills.FS in the first place.
	want := []string{"implement", "plan", "pull-request", "review", "simplify"}
	if len(got) != len(want) {
		t.Fatalf("discovered skills = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("discovered skills = %v, want %v", got, want)
		}
	}

	for _, name := range got {
		if name == "partials" || name == "gen" || name == "testdata" {
			t.Errorf("discovered skill %q should not be exposed as a skill", name)
		}
	}
}
