package skill

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoaderDiscoverAndLoadMany(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustSkill(t, root, "beta", "beta instructions")
	mustSkill(t, root, "alpha", "alpha instructions")

	loader := Loader{RootDir: root}

	discovered, err := loader.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if got, want := len(discovered), 2; got != want {
		t.Fatalf("len(discovered) = %d, want %d", got, want)
	}
	if discovered[0].Name != "alpha" || discovered[1].Name != "beta" {
		t.Fatalf("discovered order = [%s %s], want [alpha beta]", discovered[0].Name, discovered[1].Name)
	}

	loaded, err := loader.LoadMany(context.Background(), []string{"beta", "alpha"})
	if err != nil {
		t.Fatalf("LoadMany() error = %v", err)
	}
	if loaded[0].Name != "beta" || loaded[1].Name != "alpha" {
		t.Fatalf("loaded order = [%s %s], want [beta alpha]", loaded[0].Name, loaded[1].Name)
	}
	if loaded[0].Content != "beta instructions" || loaded[1].Content != "alpha instructions" {
		t.Fatalf("loaded contents do not match")
	}
}

func TestLoaderRejectsInvalidSkillNames(t *testing.T) {
	t.Parallel()

	loader := Loader{RootDir: t.TempDir()}

	if _, err := loader.Load(context.Background(), "../alpha"); err == nil {
		t.Fatalf("expected invalid skill name error")
	}
}

func mustSkill(t *testing.T, root, name, content string) {
	t.Helper()

	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", dir, err)
	}
}
