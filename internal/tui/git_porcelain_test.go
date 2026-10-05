package tui

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func newTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.name", "Test User")
	runGit(t, repo, "config", "user.email", "test@example.com")
	return repo
}

func writeTestFile(t *testing.T, repo, name, content string) {
	t.Helper()
	path := filepath.Join(repo, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func failOnLog(t *testing.T) func(error) {
	t.Helper()
	return func(err error) { t.Errorf("unexpected logged error: %v", err) }
}

func numberedLines(n int, prefix string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(prefix)
		b.WriteString(strings.Repeat("x", i%7+1))
		b.WriteString("\n")
	}
	return b.String()
}

func TestGitCommandDisablesOptionalLocks(t *testing.T) {
	t.Parallel()
	cmd := gitCommand(context.Background(), "/repo", "status")
	if len(cmd.Args) < 5 || cmd.Args[1] != "--no-optional-locks" {
		t.Fatalf("args = %v, want --no-optional-locks first", cmd.Args)
	}
	if !slices.Equal(cmd.Args[2:], []string{"-C", "/repo", "status"}) {
		t.Fatalf("args = %v, want -C /repo status after the flag", cmd.Args)
	}
	for _, want := range []string{"GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0"} {
		if !slices.Contains(cmd.Env, want) {
			t.Errorf("env missing %s", want)
		}
	}
}

func TestExecGitErrorIncludesStderr(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := readGitStatus(context.Background(), dir)
	if err == nil {
		t.Fatal("err = nil, want failure")
	}
	if !strings.HasPrefix(err.Error(), "git status:") || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("err = %q, want git status prefix with git's stderr", err)
	}
}

func TestParseGitStatusV2(t *testing.T) {
	t.Parallel()
	const sha = "0123456789abcdef0123456789abcdef01234567"
	tests := []struct {
		name      string
		input     string
		wantOID   string
		wantHead  string
		wantAhead int
		wantFiles []gitModifiedFile
		wantName  string
	}{
		{
			name: "branch headers with upstream",
			input: "# branch.oid " + sha + "\x00# branch.head main\x00# branch.upstream origin/main\x00# branch.ab +3 -1\x00" +
				"1 .M N... 100644 100644 100644 aaa bbb dir/sp ace.txt\x00",
			wantOID: sha, wantHead: "main", wantAhead: 3, wantName: "main",
			wantFiles: []gitModifiedFile{{Status: "M", Path: filepath.Join("dir", "sp ace.txt")}},
		},
		{
			name:    "initial repo with staged add",
			input:   "# branch.oid (initial)\x00# branch.head main\x001 A. N... 000000 100644 100644 000 bbb new.go\x00",
			wantOID: "(initial)", wantHead: "main", wantName: "main",
			wantFiles: []gitModifiedFile{{Status: "A", Path: "new.go"}},
		},
		{
			name:    "detached",
			input:   "# branch.oid " + sha + "\x00# branch.head (detached)\x00",
			wantOID: sha, wantHead: "(detached)", wantName: "detached@0123456",
		},
		{
			name:    "detached with short oid",
			input:   "# branch.oid abc\x00# branch.head (detached)\x00",
			wantOID: "abc", wantHead: "(detached)", wantName: "detached",
		},
		{
			name: "rename consumes original path record",
			input: "2 R. N... 100644 100644 100644 aaa bbb R100 new/name.go\x00old/name.go\x00" +
				"1 .D N... 100644 100644 000000 aaa bbb gone.go\x00",
			wantFiles: []gitModifiedFile{
				{Status: "A", Path: filepath.Join("new", "name.go")},
				{Status: "D", Path: "gone.go"},
			},
		},
		{
			name: "unmerged untracked and ignored",
			input: "u UU N... 100644 100644 100644 100644 a b c conflict.go\x00" +
				"? scratch.txt\x00! build.out\x00? ü mlaut.txt\x00",
			wantFiles: []gitModifiedFile{
				{Status: "U", Path: "conflict.go"},
				{Status: "U", Path: "scratch.txt"},
				{Status: "U", Path: "ü mlaut.txt"},
			},
		},
		{
			name:      "xy with U is unmerged",
			input:     "1 AU N... 100644 100644 100644 a b both.go\x00",
			wantFiles: []gitModifiedFile{{Status: "U", Path: "both.go"}},
		},
		{
			name:      "type change is modification",
			input:     "1 T. N... 100644 120000 120000 a b link\x00",
			wantFiles: []gitModifiedFile{{Status: "M", Path: "link"}},
		},
		{
			name:      "malformed records skipped",
			input:     "1 .M short\x00?\x00u UU\x00x garbage\x00# branch.ab nonsense\x00\x001 .M N... 1 1 1 a b ok.go\x00",
			wantFiles: []gitModifiedFile{{Status: "M", Path: "ok.go"}},
		},
		{
			name:  "empty",
			input: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseGitStatusV2([]byte(tt.input))
			if got.oid != tt.wantOID || got.head != tt.wantHead || got.ahead != tt.wantAhead {
				t.Errorf("headers = (%q, %q, %d), want (%q, %q, %d)", got.oid, got.head, got.ahead, tt.wantOID, tt.wantHead, tt.wantAhead)
			}
			if tt.wantName != "" && got.branch() != tt.wantName {
				t.Errorf("branch() = %q, want %q", got.branch(), tt.wantName)
			}
			if !reflect.DeepEqual(got.files, tt.wantFiles) {
				t.Errorf("files = %+v, want %+v", got.files, tt.wantFiles)
			}
		})
	}
}

func TestParseGitNumstatZ(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  map[string]gitCounts
	}{
		{
			name:  "normal and binary",
			input: "3\t1\tsp ace.txt\x00-\t-\timg.png\x00",
			want:  map[string]gitCounts{"sp ace.txt": {3, 1}, "img.png": {0, 0}},
		},
		{
			name:  "rename keyed by destination",
			input: "5\t2\t\x00src/old/big\x00src/new/big\x001\t0\ta.go\x00",
			want:  map[string]gitCounts{"src/new/big": {5, 2}, "a.go": {1, 0}},
		},
		{
			name:  "truncated rename and junk",
			input: "junk\x002\t2\t\x00only-src\x00",
			want:  map[string]gitCounts{},
		},
		{
			name:  "empty",
			input: "",
			want:  map[string]gitCounts{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := parseGitNumstatZ([]byte(tt.input)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("counts = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDetectGitSnapshotUnbornRepoStagedFile(t *testing.T) {
	t.Parallel()
	repo := newTestRepo(t)
	writeTestFile(t, repo, "first.txt", "hello\n")
	runGit(t, repo, "add", "first.txt")

	snap := detectGitSnapshot(context.Background(), repo, failOnLog(t))
	if !snap.ready || !snap.dirty {
		t.Fatalf("ready=%v dirty=%v, want both true", snap.ready, snap.dirty)
	}
	if snap.branch != "main" {
		t.Errorf("branch = %q, want main", snap.branch)
	}
	want := []gitModifiedFile{{Status: "A", Path: "first.txt"}}
	if !reflect.DeepEqual(snap.modifiedFiles, want) {
		t.Errorf("files = %+v, want %+v", snap.modifiedFiles, want)
	}
}

func TestDetectGitSnapshotRenameWithModification(t *testing.T) {
	t.Parallel()
	repo := newTestRepo(t)
	body := numberedLines(200, "line ")
	writeTestFile(t, repo, "src/old/big.go", body)
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "init")

	runGit(t, repo, "mv", "src/old/big.go", "src/new_big.go")
	writeTestFile(t, repo, "src/new_big.go", body+"extra one\nextra two\n")
	runGit(t, repo, "add", ".")

	snap := detectGitSnapshot(context.Background(), repo, failOnLog(t))
	if len(snap.modifiedFiles) != 1 {
		t.Fatalf("files = %+v, want exactly one", snap.modifiedFiles)
	}
	f := snap.modifiedFiles[0]
	if f.Path != filepath.Join("src", "new_big.go") || f.Status != "A" {
		t.Errorf("file = %+v, want A at src/new_big.go", f)
	}
	if f.Added <= 0 {
		t.Errorf("added = %d, want > 0", f.Added)
	}
}

func TestDetectGitSnapshotUnusualPaths(t *testing.T) {
	t.Parallel()
	repo := newTestRepo(t)
	names := []string{"sp ace.txt", "ümlaut.txt"}
	for _, name := range names {
		writeTestFile(t, repo, name, "one\n")
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "init")
	for _, name := range names {
		writeTestFile(t, repo, name, "one\ntwo\n")
	}

	snap := detectGitSnapshot(context.Background(), repo, failOnLog(t))
	if len(snap.modifiedFiles) != len(names) {
		t.Fatalf("files = %+v, want %d", snap.modifiedFiles, len(names))
	}
	got := map[string]gitModifiedFile{}
	for _, f := range snap.modifiedFiles {
		got[f.Path] = f
	}
	for _, name := range names {
		f, ok := got[name]
		if !ok {
			t.Errorf("missing path %q in %+v", name, snap.modifiedFiles)
			continue
		}
		if f.Added != 1 {
			t.Errorf("%q added = %d, want 1", name, f.Added)
		}
	}
}

func TestDetectGitSnapshotAhead(t *testing.T) {
	t.Parallel()
	repo := newTestRepo(t)
	runGit(t, repo, "commit", "--allow-empty", "-m", "init")

	snap := detectGitSnapshot(context.Background(), repo, failOnLog(t))
	if snap.ahead != 0 {
		t.Errorf("ahead without upstream = %d, want 0", snap.ahead)
	}

	runGit(t, repo, "branch", "base")
	runGit(t, repo, "commit", "--allow-empty", "-m", "two")
	runGit(t, repo, "commit", "--allow-empty", "-m", "three")
	runGit(t, repo, "branch", "--set-upstream-to=base")

	snap = detectGitSnapshot(context.Background(), repo, failOnLog(t))
	if snap.ahead != 2 {
		t.Errorf("ahead = %d, want 2", snap.ahead)
	}
}

func TestDetectGitSnapshotDetachedHead(t *testing.T) {
	t.Parallel()
	repo := newTestRepo(t)
	runGit(t, repo, "commit", "--allow-empty", "-m", "init")
	sha := gitOutput(t, repo, "rev-parse", "HEAD")
	runGit(t, repo, "checkout", "--detach")

	snap := detectGitSnapshot(context.Background(), repo, failOnLog(t))
	if want := "detached@" + sha[:7]; snap.branch != want {
		t.Errorf("branch = %q, want %q", snap.branch, want)
	}
}

func TestDetectGitSnapshotSortsFiles(t *testing.T) {
	t.Parallel()
	repo := newTestRepo(t)
	for _, name := range []string{"m2.txt", "m1.txt", "del.txt"} {
		writeTestFile(t, repo, name, "x\n")
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "init")

	writeTestFile(t, repo, "m2.txt", "y\n")
	writeTestFile(t, repo, "m1.txt", "y\n")
	if err := os.Remove(filepath.Join(repo, "del.txt")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, repo, "added.txt", "a\n")
	runGit(t, repo, "add", "added.txt")
	writeTestFile(t, repo, "a_untracked.txt", "u\n")

	snap := detectGitSnapshot(context.Background(), repo, failOnLog(t))
	var got []string
	for _, f := range snap.modifiedFiles {
		got = append(got, f.Status+" "+f.Path)
	}
	want := []string{"M m1.txt", "M m2.txt", "A added.txt", "D del.txt", "U a_untracked.txt"}
	if !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}
