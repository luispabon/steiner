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

	loader := Loader{RootDirs: []string{root}}

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

	loader := Loader{RootDirs: []string{t.TempDir()}}

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

	loader := Loader{RootDirs: []string{"/nonexistent/path"}}
	skills, err := loader.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if skills != nil {
		t.Fatalf("Discover() = %v, want nil", skills)
	}
}

func TestLoaderDiscoverEmptyRoots(t *testing.T) {
	t.Parallel()

	loader := Loader{RootDirs: []string{}}
	skills, err := loader.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if skills != nil {
		t.Fatalf("Discover() = %v, want nil", skills)
	}
}

func TestLoaderDiscoverEmptyRootStrings(t *testing.T) {
	t.Parallel()

	loader := Loader{RootDirs: []string{"", "  "}}
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

	loader := Loader{RootDirs: []string{root}}
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

	loader := Loader{RootDirs: []string{root}}
	if _, err := loader.Discover(ctx); err == nil {
		t.Fatal("Discover() expected context cancellation error")
	}
}

func TestLoaderLoadMissingSkill(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	loader := Loader{RootDirs: []string{root}}

	if _, err := loader.Load(context.Background(), "missing"); err == nil {
		t.Fatal("Load() expected error for missing skill")
	}
}

func TestLoaderLoadEmptyRoots(t *testing.T) {
	t.Parallel()

	loader := Loader{RootDirs: []string{}}
	if _, err := loader.Load(context.Background(), "alpha"); err == nil {
		t.Fatal("Load() expected error for empty roots")
	}
}

func TestLoaderLoadContextCancellation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustSkill(t, root, "alpha", "instructions")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	loader := Loader{RootDirs: []string{root}}
	if _, err := loader.Load(ctx, "alpha"); err == nil {
		t.Fatal("Load() expected context cancellation error")
	}
}

func TestLoaderLoadManyStopsOnError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustSkill(t, root, "alpha", "instructions")

	loader := Loader{RootDirs: []string{root}}
	loaded, err := loader.LoadMany(context.Background(), []string{"alpha", "missing"})
	if err == nil {
		t.Fatal("LoadMany() expected error for missing skill")
	}
	if loaded != nil {
		t.Fatalf("LoadMany() = %v, want nil on error", loaded)
	}
}

func TestLoaderDiscoverMultiRootsNoOverlap(t *testing.T) {
	t.Parallel()

	root1 := t.TempDir()
	mustSkill(t, root1, "alpha", "alpha from root1")
	mustSkill(t, root1, "beta", "beta from root1")

	root2 := t.TempDir()
	mustSkill(t, root2, "gamma", "gamma from root2")
	mustSkill(t, root2, "delta", "delta from root2")

	loader := Loader{RootDirs: []string{root1, root2}}
	discovered, err := loader.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	if got, want := len(discovered), 4; got != want {
		t.Fatalf("len(discovered) = %d, want %d", got, want)
	}

	// Check that all skills are present and sorted
	names := []string{discovered[0].Name, discovered[1].Name, discovered[2].Name, discovered[3].Name}
	expected := []string{"alpha", "beta", "delta", "gamma"}
	for i, name := range names {
		if name != expected[i] {
			t.Fatalf("discovered[%d].Name = %s, want %s", i, name, expected[i])
		}
	}
}

func TestLoaderDiscoverMultiRootsWithConflictPrecedence(t *testing.T) {
	t.Parallel()

	root1 := t.TempDir()
	mustSkill(t, root1, "shared", "shared from root1")
	mustSkill(t, root1, "alpha", "alpha from root1")

	root2 := t.TempDir()
	mustSkill(t, root2, "shared", "shared from root2")
	mustSkill(t, root2, "beta", "beta from root2")

	loader := Loader{RootDirs: []string{root1, root2}}
	discovered, err := loader.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	if got, want := len(discovered), 3; got != want {
		t.Fatalf("len(discovered) = %d, want %d", got, want)
	}

	// Verify shared skill came from root1 (first in precedence)
	for _, skill := range discovered {
		if skill.Name == "shared" {
			expectedPath := filepath.Join(root1, "shared", "SKILL.md")
			if skill.Path != expectedPath {
				t.Fatalf("shared skill path = %s, want %s", skill.Path, expectedPath)
			}
		}
	}

	// Verify all skills are present
	names := []string{discovered[0].Name, discovered[1].Name, discovered[2].Name}
	expected := []string{"alpha", "beta", "shared"}
	for i, name := range names {
		if name != expected[i] {
			t.Fatalf("discovered[%d].Name = %s, want %s", i, name, expected[i])
		}
	}
}

func TestLoaderLoadMultiRootsPrecedence(t *testing.T) {
	t.Parallel()

	root1 := t.TempDir()
	mustSkill(t, root1, "alpha", "alpha from root1")

	root2 := t.TempDir()
	mustSkill(t, root2, "alpha", "alpha from root2")
	mustSkill(t, root2, "beta", "beta from root2")

	loader := Loader{RootDirs: []string{root1, root2}}

	// Load "alpha" should get root1's version
	alpha, err := loader.Load(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("Load(alpha) error = %v", err)
	}
	if alpha.Content != "alpha from root1" {
		t.Fatalf("alpha content = %s, want 'alpha from root1'", alpha.Content)
	}

	// Load "beta" should get root2's version
	beta, err := loader.Load(context.Background(), "beta")
	if err != nil {
		t.Fatalf("Load(beta) error = %v", err)
	}
	if beta.Content != "beta from root2" {
		t.Fatalf("beta content = %s, want 'beta from root2'", beta.Content)
	}
}

func TestLoaderLoadManyMultiRoots(t *testing.T) {
	t.Parallel()

	root1 := t.TempDir()
	mustSkill(t, root1, "alpha", "alpha from root1")

	root2 := t.TempDir()
	mustSkill(t, root2, "beta", "beta from root2")

	loader := Loader{RootDirs: []string{root1, root2}}

	loaded, err := loader.LoadMany(context.Background(), []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("LoadMany() error = %v", err)
	}

	if got, want := len(loaded), 2; got != want {
		t.Fatalf("len(loaded) = %d, want %d", got, want)
	}
	if loaded[0].Name != "alpha" || loaded[0].Content != "alpha from root1" {
		t.Fatalf("loaded[0] = %v, want alpha from root1", loaded[0])
	}
	if loaded[1].Name != "beta" || loaded[1].Content != "beta from root2" {
		t.Fatalf("loaded[1] = %v, want beta from root2", loaded[1])
	}
}

func TestLoaderLoadMultiRootsMissingInAll(t *testing.T) {
	t.Parallel()

	root1 := t.TempDir()
	mustSkill(t, root1, "alpha", "alpha from root1")

	root2 := t.TempDir()
	mustSkill(t, root2, "beta", "beta from root2")

	loader := Loader{RootDirs: []string{root1, root2}}

	if _, err := loader.Load(context.Background(), "missing"); err == nil {
		t.Fatal("Load(missing) expected error")
	}
}

func TestLoaderDiscoverMultiRootsMixedExistent(t *testing.T) {
	t.Parallel()

	root1 := t.TempDir()
	mustSkill(t, root1, "alpha", "alpha from root1")

	root2 := t.TempDir()
	mustSkill(t, root2, "beta", "beta from root2")

	// root3 doesn't exist
	root3 := "/nonexistent/path/for/test"

	loader := Loader{RootDirs: []string{root1, root3, root2}}
	discovered, err := loader.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	if got, want := len(discovered), 2; got != want {
		t.Fatalf("len(discovered) = %d, want %d", got, want)
	}

	names := []string{discovered[0].Name, discovered[1].Name}
	expected := []string{"alpha", "beta"}
	for i, name := range names {
		if name != expected[i] {
			t.Fatalf("discovered[%d].Name = %s, want %s", i, name, expected[i])
		}
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

func TestLoaderDiscoverSourceSingleRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustSkill(t, root, "alpha", "alpha instructions")

	loader := Loader{RootDirs: []string{root}}
	discovered, err := loader.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	if len(discovered) != 1 {
		t.Fatalf("len(discovered) = %d, want 1", len(discovered))
	}
	if discovered[0].Source != "project" {
		t.Fatalf("discovered[0].Source = %q, want %q", discovered[0].Source, "project")
	}
}

func TestLoaderDiscoverSourceMultipleRoots(t *testing.T) {
	t.Parallel()

	root1 := t.TempDir()
	mustSkill(t, root1, "alpha", "alpha from root1")

	root2 := t.TempDir()
	mustSkill(t, root2, "beta", "beta from root2")

	root3 := t.TempDir()
	mustSkill(t, root3, "gamma", "gamma from root3")

	loader := Loader{RootDirs: []string{root1, root2, root3}}
	discovered, err := loader.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	if len(discovered) != 3 {
		t.Fatalf("len(discovered) = %d, want 3", len(discovered))
	}

	tests := []struct {
		index      int
		name       string
		wantSource string
	}{
		{0, "alpha", "project"},
		{1, "beta", "user"},
		{2, "gamma", "global"},
	}

	for _, tt := range tests {
		if discovered[tt.index].Name != tt.name {
			t.Fatalf("discovered[%d].Name = %q, want %q", tt.index, discovered[tt.index].Name, tt.name)
		}
		if discovered[tt.index].Source != tt.wantSource {
			t.Fatalf("discovered[%d].Source = %q, want %q", tt.index, discovered[tt.index].Source, tt.wantSource)
		}
	}
}

func TestLoaderDiscoverSourceWithConflict(t *testing.T) {
	t.Parallel()

	root1 := t.TempDir()
	mustSkill(t, root1, "shared", "shared from root1")

	root2 := t.TempDir()
	mustSkill(t, root2, "shared", "shared from root2")

	loader := Loader{RootDirs: []string{root1, root2}}
	discovered, err := loader.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	if len(discovered) != 1 {
		t.Fatalf("len(discovered) = %d, want 1", len(discovered))
	}

	// Shared skill should come from root1 (index 0) with "project" source
	if discovered[0].Name != "shared" {
		t.Fatalf("discovered[0].Name = %q, want %q", discovered[0].Name, "shared")
	}
	if discovered[0].Source != "project" {
		t.Fatalf("discovered[0].Source = %q, want %q", discovered[0].Source, "project")
	}
}

func TestLoaderDiscoverExtractsSummary(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustSkill(t, root, "alpha", "# Alpha\n\nUse when the user asks for alpha workflows.\n\n## Details\nMore text.")

	loader := Loader{RootDirs: []string{root}}
	discovered, err := loader.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("len(discovered) = %d, want 1", len(discovered))
	}
	if discovered[0].Summary != "Use when the user asks for alpha workflows." {
		t.Fatalf("discovered[0].Summary = %q, want extracted summary", discovered[0].Summary)
	}
}

func TestLoaderDiscoverSourceMoreThanThreeRoots(t *testing.T) {
	t.Parallel()

	root1 := t.TempDir()
	mustSkill(t, root1, "alpha", "alpha from root1")

	root2 := t.TempDir()
	mustSkill(t, root2, "beta", "beta from root2")

	root3 := t.TempDir()
	mustSkill(t, root3, "gamma", "gamma from root3")

	root4 := t.TempDir()
	mustSkill(t, root4, "delta", "delta from root4")

	loader := Loader{RootDirs: []string{root1, root2, root3, root4}}
	discovered, err := loader.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	if len(discovered) != 4 {
		t.Fatalf("len(discovered) = %d, want 4", len(discovered))
	}

	tests := []struct {
		index      int
		name       string
		wantSource string
	}{
		{0, "alpha", "project"},
		{1, "beta", "user"},
		{2, "delta", "root3"},
		{3, "gamma", "global"},
	}

	for _, tt := range tests {
		if discovered[tt.index].Name != tt.name {
			t.Fatalf("discovered[%d].Name = %q, want %q", tt.index, discovered[tt.index].Name, tt.name)
		}
		if discovered[tt.index].Source != tt.wantSource {
			t.Fatalf("discovered[%d].Source = %q, want %q", tt.index, discovered[tt.index].Source, tt.wantSource)
		}
	}
}
