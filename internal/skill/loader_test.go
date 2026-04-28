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

func TestLoaderDiscoverNonExistentRoot(t *testing.T) {
	t.Parallel()

	loader := Loader{RootDir: "/nonexistent/path"}
	skills, err := loader.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if skills != nil {
		t.Fatalf("Discover() = %v, want nil", skills)
	}
}

func TestLoaderDiscoverEmptyRoot(t *testing.T) {
	t.Parallel()

	loader := Loader{RootDir: ""}
	skills, err := loader.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if skills != nil {
		t.Fatalf("Discover() = %v, want nil", skills)
	}
}

func TestLoaderDiscoverSkipsFilesAndDirsWithoutSkill(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustSkill(t, root, "valid", "instructions")

	// directory without SKILL.md
	dirNoSkill := filepath.Join(root, "noskill")
	if err := os.MkdirAll(dirNoSkill, 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	// regular file at root level
	filePath := filepath.Join(root, "file.txt")
	if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	loader := Loader{RootDir: root}
	discovered, err := loader.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if got, want := len(discovered), 1; got != want {
		t.Fatalf("len(discovered) = %d, want %d", got, want)
	}
	if discovered[0].Name != "valid" {
		t.Fatalf("discovered[0].Name = %s, want valid", discovered[0].Name)
	}
}

func TestLoaderDiscoverContextCancellation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustSkill(t, root, "alpha", "instructions")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	loader := Loader{RootDir: root}
	if _, err := loader.Discover(ctx); err == nil {
		t.Fatal("Discover() expected context cancellation error")
	}
}

func TestLoaderLoadMissingSkill(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	loader := Loader{RootDir: root}

	if _, err := loader.Load(context.Background(), "missing"); err == nil {
		t.Fatal("Load() expected error for missing skill")
	}
}

func TestLoaderLoadEmptyRoot(t *testing.T) {
	t.Parallel()

	loader := Loader{RootDir: ""}
	if _, err := loader.Load(context.Background(), "alpha"); err == nil {
		t.Fatal("Load() expected error for empty root")
	}
}

func TestLoaderLoadContextCancellation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustSkill(t, root, "alpha", "instructions")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	loader := Loader{RootDir: root}
	if _, err := loader.Load(ctx, "alpha"); err == nil {
		t.Fatal("Load() expected context cancellation error")
	}
}

func TestLoaderLoadManyStopsOnError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustSkill(t, root, "alpha", "instructions")

	loader := Loader{RootDir: root}
	loaded, err := loader.LoadMany(context.Background(), []string{"alpha", "missing"})
	if err == nil {
		t.Fatal("LoadMany() expected error for missing skill")
	}
	if loaded != nil {
		t.Fatalf("LoadMany() = %v, want nil on error", loaded)
	}
}

func TestValidateSkillName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr bool
	}{
		{"alpha", false},
		{"alpha-beta", false},
		{"alpha_beta", false},
		{"", true},
		{"   ", true},
		{"../alpha", true},
		{"alpha/beta", true},
		{"alpha\\beta", true},
	}
	for _, tt := range tests {
		err := validateSkillName(tt.name)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateSkillName(%q) error = %v, wantErr = %v", tt.name, err, tt.wantErr)
		}
	}
}
