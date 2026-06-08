package sandbox

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSSHOverlayClose_NilAndEmpty(t *testing.T) {
	var overlay *sshOverlay
	if err := overlay.Close(); err != nil {
		t.Fatalf("nil overlay close: %v", err)
	}

	if err := (&sshOverlay{}).Close(); err != nil {
		t.Fatalf("empty overlay close: %v", err)
	}
}

func TestSSHOverlayClose_ClosesMemfds(t *testing.T) {
	f1, err := os.CreateTemp(t.TempDir(), "ssh-overlay-*")
	if err != nil {
		t.Fatalf("create temp file 1: %v", err)
	}
	f2, err := os.CreateTemp(t.TempDir(), "ssh-overlay-*")
	if err != nil {
		t.Fatalf("create temp file 2: %v", err)
	}

	overlay := &sshOverlay{
		memfds: []*os.File{f1, nil, f2},
	}

	if err := overlay.Close(); err != nil {
		t.Fatalf("close overlay: %v", err)
	}

	if err := f1.Close(); err == nil {
		t.Fatal("expected first memfd to be closed")
	}
	if err := f2.Close(); err == nil {
		t.Fatal("expected second memfd to be closed")
	}
}

func TestSplitSSHConfigFields(t *testing.T) {
	line := ` Include "/etc/ssh/with # hash.conf" /etc/ssh/one.conf # trailing comment`

	fields, ok := splitSSHConfigFields(line)
	if !ok {
		t.Fatal("expected line to parse")
	}

	want := []string{"Include", "/etc/ssh/with # hash.conf", "/etc/ssh/one.conf"}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("fields = %#v, want %#v", fields, want)
	}
}

func TestSplitSSHConfigFields_UnbalancedQuoteIsIgnored(t *testing.T) {
	fields, ok := splitSSHConfigFields(`Include "/etc/ssh/bad.conf`)
	if ok {
		t.Fatalf("expected parse failure, got fields=%v", fields)
	}
}

func TestResolveSSHIncludeResolution_ParsesIncludesRecursively(t *testing.T) {
	rootDir := t.TempDir()
	sshDir := filepath.Join(rootDir, "etc", "ssh")
	dropInDir := filepath.Join(sshDir, "ssh_config.d")
	matchDir := filepath.Join(sshDir, "match.d")
	realDir := filepath.Join(rootDir, "real")

	mkdirAll(t, dropInDir)
	mkdirAll(t, matchDir)
	mkdirAll(t, realDir)

	rootConfig := filepath.Join(sshDir, "ssh_config")
	first := filepath.Join(dropInDir, "10-first.conf")
	second := filepath.Join(dropInDir, "20-second.conf")
	matchConfig := filepath.Join(matchDir, "30-match.conf")
	realConfig := filepath.Join(realDir, "symlinked.conf")
	linkConfig := filepath.Join(sshDir, "symlink.conf")

	writeFile(t, first, "Host *\n")
	writeFile(t, second, "Include "+matchConfig+"\n")
	writeFile(t, matchConfig, "Host other\n")
	writeFile(t, realConfig, "Host symlinked\n")
	if err := os.Symlink(realConfig, linkConfig); err != nil {
		t.Fatalf("symlink include: %v", err)
	}

	rootContent := "" +
		"include " + filepath.Join(dropInDir, "*.conf") + " " + linkConfig + "\n" +
		"Include ~/skip-me.conf ${HOME}/skip-too.conf relative.conf %h-dynamic.conf\n" +
		"Host github.com\n" +
		"  Include " + matchConfig + "\n"
	writeFile(t, rootConfig, rootContent)

	resolution := resolveSSHIncludeResolution(rootConfig)

	gotSandboxPaths := make([]string, 0, len(resolution.files))
	for _, file := range resolution.files {
		gotSandboxPaths = append(gotSandboxPaths, file.sandboxPath)
	}

	wantSandboxPaths := []string{rootConfig, first, second, matchConfig, linkConfig}
	if !reflect.DeepEqual(gotSandboxPaths, wantSandboxPaths) {
		t.Fatalf("sandbox paths = %#v, want %#v", gotSandboxPaths, wantSandboxPaths)
	}

	if resolution.files[len(resolution.files)-1].sourcePath != realConfig {
		t.Fatalf("symlink source path = %q, want %q", resolution.files[len(resolution.files)-1].sourcePath, realConfig)
	}

	wantReplacementDirs := []string{dropInDir, matchDir}
	if !reflect.DeepEqual(resolution.replacementDirs, wantReplacementDirs) {
		t.Fatalf("replacement dirs = %#v, want %#v", resolution.replacementDirs, wantReplacementDirs)
	}

	if !slices.ContainsFunc(resolution.skippedDiagnostics, func(msg string) bool {
		return msg == "skip ssh include ~/skip-me.conf: dynamic path"
	}) {
		t.Fatalf("expected dynamic include diagnostic, got %#v", resolution.skippedDiagnostics)
	}
}

func TestResolveSSHIncludeResolution_SkipsNonRegularAndDuplicateFiles(t *testing.T) {
	rootDir := t.TempDir()
	sshDir := filepath.Join(rootDir, "etc", "ssh")
	dropInDir := filepath.Join(sshDir, "ssh_config.d")
	mkdirAll(t, dropInDir)

	rootConfig := filepath.Join(sshDir, "ssh_config")
	regular := filepath.Join(dropInDir, "10-regular.conf")
	dirInclude := filepath.Join(dropInDir, "20-dir.conf")
	alias := filepath.Join(dropInDir, "30-alias.conf")
	target := filepath.Join(rootDir, "real.conf")

	writeFile(t, regular, "Host *\n")
	mkdirAll(t, dirInclude)
	writeFile(t, target, "Host alias\n")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatalf("symlink alias: %v", err)
	}

	rootContent := "" +
		"Include " + regular + " " + dirInclude + " " + alias + "\n" +
		"Include " + target + "\n"
	writeFile(t, rootConfig, rootContent)

	resolution := resolveSSHIncludeResolution(rootConfig)

	gotSandboxPaths := make([]string, 0, len(resolution.files))
	for _, file := range resolution.files {
		gotSandboxPaths = append(gotSandboxPaths, file.sandboxPath)
	}

	wantSandboxPaths := []string{rootConfig, regular, alias}
	if !reflect.DeepEqual(gotSandboxPaths, wantSandboxPaths) {
		t.Fatalf("sandbox paths = %#v, want %#v", gotSandboxPaths, wantSandboxPaths)
	}

	if !slices.ContainsFunc(resolution.skippedDiagnostics, func(msg string) bool {
		return msg == "skip ssh include "+dirInclude+": not a regular file"
	}) {
		t.Fatalf("expected non-regular diagnostic, got %#v", resolution.skippedDiagnostics)
	}
}

func TestPrepareSSHOverlayFromPath_BuildsMemfdsAndArgs(t *testing.T) {
	rootDir := t.TempDir()
	sshDir := filepath.Join(rootDir, "etc", "ssh")
	dropInDir := filepath.Join(sshDir, "ssh_config.d")
	mkdirAll(t, dropInDir)

	rootConfig := filepath.Join(sshDir, "ssh_config")
	included := filepath.Join(dropInDir, "10-main.conf")
	writeFile(t, included, "Host *\n")
	writeFile(t, rootConfig, "Include "+filepath.Join(dropInDir, "*.conf")+"\n")

	overlay, err := prepareSSHOverlayFromPath(rootConfig, 3)
	if isMemfdUnavailable(err) {
		t.Skipf("memfd unavailable: %v", err)
	}
	if err != nil {
		t.Fatalf("prepare overlay: %v", err)
	}
	defer func() {
		if err := overlay.Close(); err != nil {
			t.Fatalf("close overlay: %v", err)
		}
	}()

	if overlay == nil {
		t.Fatal("expected overlay")
	}
	if len(overlay.memfds) != 2 {
		t.Fatalf("memfd count = %d, want 2", len(overlay.memfds))
	}

	if !containsSeq(overlay.bwrapArgs, "--tmpfs", dropInDir) {
		t.Fatalf("expected tmpfs mount for drop-in dir, args=%v", overlay.bwrapArgs)
	}
	if !containsSeq(overlay.bwrapArgs, "--perms", "0644", "--ro-bind-data", "3", rootConfig) {
		t.Fatalf("expected root config ro-bind-data mount, args=%v", overlay.bwrapArgs)
	}
	if !containsSeq(overlay.bwrapArgs, "--perms", "0644", "--ro-bind-data", "4", included) {
		t.Fatalf("expected included config ro-bind-data mount, args=%v", overlay.bwrapArgs)
	}

	gotRootContent, err := io.ReadAll(overlay.memfds[0])
	if err != nil {
		t.Fatalf("read root memfd: %v", err)
	}
	if string(gotRootContent) != "Include "+filepath.Join(dropInDir, "*.conf")+"\n" {
		t.Fatalf("root memfd content = %q", string(gotRootContent))
	}
}

func TestPrepareSSHOverlayFromPath_MissingRootReturnsNil(t *testing.T) {
	overlay, err := prepareSSHOverlayFromPath(filepath.Join(t.TempDir(), "missing"), 3)
	if err != nil {
		t.Fatalf("prepare overlay: %v", err)
	}
	if overlay != nil {
		t.Fatalf("expected nil overlay for missing root, got %#v", overlay)
	}
}

func TestSSHOverlayConstantsAndScaffold(t *testing.T) {
	file := sshOverlayFile{
		sourcePath:  "/etc/ssh/ssh_config",
		sandboxPath: "/tmp/ssh_config",
		content:     []byte("Host *"),
	}
	resolution := sshIncludeResolution{
		files:              []sshOverlayFile{file},
		replacementDirs:    []string{"/tmp"},
		skippedDiagnostics: []string{"skipped include"},
	}
	overlay := sshOverlay{
		bwrapArgs: []string{"--ro-bind-data", "3", "/tmp/ssh_config"},
		memfds:    nil,
	}

	if got, want := sshSystemConfigPath, "/etc/ssh/ssh_config"; got != want {
		t.Fatalf("sshSystemConfigPath = %q, want %q", got, want)
	}
	if got, want := sshMaxIncludeDepth, 8; got != want {
		t.Fatalf("sshMaxIncludeDepth = %d, want %d", got, want)
	}
	if got, want := sshMaxFiles, 128; got != want {
		t.Fatalf("sshMaxFiles = %d, want %d", got, want)
	}
	if got, want := sshMaxFileBytes, 1<<20; got != want {
		t.Fatalf("sshMaxFileBytes = %d, want %d", got, want)
	}
	if got, want := sshMaxTotalBytes, 8<<20; got != want {
		t.Fatalf("sshMaxTotalBytes = %d, want %d", got, want)
	}
	if len(overlay.bwrapArgs) != 3 {
		t.Fatalf("overlay.bwrapArgs len = %d, want 3", len(overlay.bwrapArgs))
	}
	if len(resolution.files) != 1 || len(resolution.replacementDirs) != 1 || len(resolution.skippedDiagnostics) != 1 {
		t.Fatalf("unexpected resolution scaffold: %+v", resolution)
	}
	if resolution.files[0].sourcePath != file.sourcePath || resolution.files[0].sandboxPath != file.sandboxPath {
		t.Fatalf("resolution file mismatch: %+v", resolution.files[0])
	}
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func isMemfdUnavailable(err error) bool {
	return errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EPERM) || errors.Is(err, unix.EINVAL)
}
