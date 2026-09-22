package config

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func inspectOptions(home, work string) LoadOptions {
	return LoadOptions{HomeDir: home, WorkingDir: work, Env: map[string]string{}}
}

func writeFixture(t *testing.T, destDir, destRel, fixtureName string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "trust", fixtureName))
	if err != nil {
		t.Fatalf("read fixture %q: %v", fixtureName, err)
	}
	dest := filepath.Join(destDir, destRel)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func writeProjectFixture(t *testing.T, work, fixtureName string) {
	t.Helper()
	writeFixture(t, work, filepath.Join(".steiner", "config.yaml"), fixtureName)
}

func writeGlobalFixture(t *testing.T, home, fixtureName string) {
	t.Helper()
	writeFixture(t, home, filepath.Join(".config", "steiner", "config.yaml"), fixtureName)
}

func TestInspectProjectNoProjectFile(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()

	inspection, err := InspectProject(inspectOptions(home, work))
	if err != nil {
		t.Fatalf("InspectProject: %v", err)
	}
	if inspection.ConfigPath != "" {
		t.Errorf("ConfigPath = %q, want empty", inspection.ConfigPath)
	}
	if len(inspection.Changes) != 0 {
		t.Errorf("Changes = %v, want empty", inspection.Changes)
	}
}

func TestInspectProjectSandboxDisabledFromDefault(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	writeProjectFixture(t, work, "sandbox_disabled.yaml")

	inspection, err := InspectProject(inspectOptions(home, work))
	if err != nil {
		t.Fatalf("InspectProject: %v", err)
	}
	want := FieldChange{Path: "sandbox.enabled", Before: "true", After: "false", Security: true}
	if len(inspection.Changes) != 1 || inspection.Changes[0] != want {
		t.Fatalf("Changes = %+v, want [%+v]", inspection.Changes, want)
	}
}

func TestInspectProjectMasksAPIKeyBeforeNotAfter(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	writeGlobalFixture(t, home, "global_api_key.yaml")
	writeProjectFixture(t, work, "project_api_key_override.yaml")

	inspection, err := InspectProject(inspectOptions(home, work))
	if err != nil {
		t.Fatalf("InspectProject: %v", err)
	}
	want := FieldChange{
		Path:     "providers.p.api_key",
		Before:   "(set)",
		After:    strconv.Quote("${EVIL}"),
		Security: true,
	}
	if len(inspection.Changes) != 1 || inspection.Changes[0] != want {
		t.Fatalf("Changes = %+v, want [%+v]", inspection.Changes, want)
	}
}

func TestInspectProjectMCPEnvSecretShownInFull(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	writeProjectFixture(t, work, "project_mcp_env.yaml")

	inspection, err := InspectProject(inspectOptions(home, work))
	if err != nil {
		t.Fatalf("InspectProject: %v", err)
	}
	want := FieldChange{
		Path:     "mcp.servers.x.env.LD_PRELOAD",
		Before:   "(unset)",
		After:    strconv.Quote("/tmp/x.so"),
		Security: true,
	}
	if len(inspection.Changes) != 1 || inspection.Changes[0] != want {
		t.Fatalf("Changes = %+v, want [%+v]", inspection.Changes, want)
	}
}

func TestInspectProjectOmitsUnchangedValue(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	writeGlobalFixture(t, home, "global_equal_value.yaml")
	writeProjectFixture(t, work, "project_equal_value.yaml")

	inspection, err := InspectProject(inspectOptions(home, work))
	if err != nil {
		t.Fatalf("InspectProject: %v", err)
	}
	if len(inspection.Changes) != 0 {
		t.Errorf("Changes = %+v, want empty", inspection.Changes)
	}
}

func TestInspectProjectUndefinedVarDoesNotError(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	writeProjectFixture(t, work, "project_undefined_var.yaml")

	inspection, err := InspectProject(inspectOptions(home, work))
	if err != nil {
		t.Fatalf("InspectProject: %v", err)
	}
	if inspection.ParseError != "" {
		t.Fatalf("ParseError = %q, want empty", inspection.ParseError)
	}
	found := false
	for _, c := range inspection.Changes {
		if c.Path == "models.default" && c.After == strconv.Quote("${UNDEFINED_VAR}") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Changes = %+v, want models.default with unexpanded value", inspection.Changes)
	}
}

func TestInspectProjectUnknownFieldDoesNotError(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	writeProjectFixture(t, work, "project_unknown_field.yaml")

	inspection, err := InspectProject(inspectOptions(home, work))
	if err != nil {
		t.Fatalf("InspectProject: %v", err)
	}
	if inspection.ParseError != "" {
		t.Fatalf("ParseError = %q, want empty", inspection.ParseError)
	}
	if len(inspection.Changes) == 0 {
		t.Fatal("Changes = empty, want at least sandbox.enabled")
	}
}

func TestInspectProjectInvalidYAMLSetsParseError(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	writeProjectFixture(t, work, "project_invalid.yaml")

	inspection, err := InspectProject(inspectOptions(home, work))
	if err != nil {
		t.Fatalf("InspectProject returned error %v, want nil", err)
	}
	if inspection.ParseError == "" {
		t.Fatal("ParseError = empty, want non-empty")
	}
	if len(inspection.Changes) != 0 {
		t.Errorf("Changes = %+v, want empty", inspection.Changes)
	}
}

func TestInspectProjectOrdering(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	writeProjectFixture(t, work, "project_ordering.yaml")

	inspection, err := InspectProject(inspectOptions(home, work))
	if err != nil {
		t.Fatalf("InspectProject: %v", err)
	}

	var gotPaths []string
	for _, c := range inspection.Changes {
		gotPaths = append(gotPaths, c.Path)
	}
	wantPaths := []string{"permissions.docker", "sandbox.enabled", "tui.fps"}
	if len(gotPaths) != len(wantPaths) {
		t.Fatalf("Changes paths = %v, want %v", gotPaths, wantPaths)
	}
	for i, p := range wantPaths {
		if gotPaths[i] != p {
			t.Fatalf("Changes paths = %v, want %v", gotPaths, wantPaths)
		}
	}
	if !inspection.Changes[0].Security || !inspection.Changes[1].Security {
		t.Errorf("expected first two changes to be security-relevant: %+v", inspection.Changes[:2])
	}
	if inspection.Changes[2].Security {
		t.Errorf("expected tui.fps to be non-security: %+v", inspection.Changes[2])
	}
}

func TestInspectProjectTruncatesLongValues(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	writeProjectFixture(t, work, "project_long_value.yaml")

	inspection, err := InspectProject(inspectOptions(home, work))
	if err != nil {
		t.Fatalf("InspectProject: %v", err)
	}
	var got *FieldChange
	for i := range inspection.Changes {
		if inspection.Changes[i].Path == "logging.file" {
			got = &inspection.Changes[i]
		}
	}
	if got == nil {
		t.Fatalf("Changes = %+v, want logging.file present", inspection.Changes)
	}
	runes := []rune(got.After)
	if len(runes) != 61 {
		t.Fatalf("After = %q (%d runes), want 61 (60 + ellipsis)", got.After, len(runes))
	}
	if runes[60] != '…' {
		t.Fatalf("After = %q, want to end with ellipsis", got.After)
	}
}

func TestInspectProjectRendersSequenceInFlowStyle(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	writeProjectFixture(t, work, "project_writable_paths.yaml")

	inspection, err := InspectProject(inspectOptions(home, work))
	if err != nil {
		t.Fatalf("InspectProject: %v", err)
	}
	want := FieldChange{
		Path:     "paths.writable_paths",
		Before:   "[]",
		After:    "[/tmp, /var/tmp]",
		Security: true,
	}
	if len(inspection.Changes) != 1 || inspection.Changes[0] != want {
		t.Fatalf("Changes = %+v, want [%+v]", inspection.Changes, want)
	}
}

func TestInspectProjectExplicitConfigPathMissing(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	missing := filepath.Join(work, "nope.yaml")
	opts := LoadOptions{
		HomeDir:    home,
		WorkingDir: work,
		Env:        map[string]string{},
		CLI:        CLIOverrides{ConfigPath: missing},
	}

	_, err := InspectProject(opts)
	if err == nil {
		t.Fatal("InspectProject: got nil error, want config-path-missing error")
	}
	want := `config path "` + missing + `" does not exist`
	if err.Error() != want {
		t.Fatalf("err = %q, want %q", err.Error(), want)
	}
}

func TestInspectProjectResolvesSymlinkedWorkingDir(t *testing.T) {
	home := t.TempDir()
	real := t.TempDir()
	resolvedReal, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	inspection, err := InspectProject(inspectOptions(home, link))
	if err != nil {
		t.Fatalf("InspectProject: %v", err)
	}
	if inspection.ProjectRoot != resolvedReal {
		t.Errorf("ProjectRoot = %q, want %q", inspection.ProjectRoot, resolvedReal)
	}
}

func TestInspectProjectReflectsTrust(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	root, err := resolveProjectRoot(work)
	if err != nil {
		t.Fatalf("resolveProjectRoot: %v", err)
	}
	if err := TrustProject(LoadOptions{HomeDir: home, Env: map[string]string{}}, root, time.Now()); err != nil {
		t.Fatalf("TrustProject: %v", err)
	}

	inspection, err := InspectProject(inspectOptions(home, work))
	if err != nil {
		t.Fatalf("InspectProject: %v", err)
	}
	if !inspection.Trusted {
		t.Error("Trusted = false, want true")
	}
}

func TestInspectProjectCorruptTrustStoreNotice(t *testing.T) {
	home, work := t.TempDir(), t.TempDir()
	storeDir := filepath.Join(home, ".config", "steiner")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "trusted_projects.json"), []byte("not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	inspection, err := InspectProject(inspectOptions(home, work))
	if err != nil {
		t.Fatalf("InspectProject: %v", err)
	}
	if inspection.StoreNotice == "" {
		t.Error("StoreNotice = empty, want non-empty")
	}
}
