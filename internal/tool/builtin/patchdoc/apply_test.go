package patchdoc

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOSFS(t *testing.T) {
	t.Parallel()

	fs := OSFS{}
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")

	if err := fs.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatalf("OSFS.MkdirAll() error = %v", err)
	}
	if err := fs.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("OSFS.WriteFile() error = %v", err)
	}

	info, err := fs.Stat(path)
	if err != nil {
		t.Fatalf("OSFS.Stat() error = %v", err)
	}
	if info.Name() != "file.txt" {
		t.Fatalf("OSFS.Stat() name = %q, want %q", info.Name(), "file.txt")
	}

	data, err := fs.ReadFile(path)
	if err != nil {
		t.Fatalf("OSFS.ReadFile() error = %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("OSFS.ReadFile() = %q, want %q", string(data), "hello")
	}

	if err := fs.Remove(path); err != nil {
		t.Fatalf("OSFS.Remove() error = %v", err)
	}
	if _, err := fs.Stat(path); err == nil {
		t.Fatalf("OSFS.Stat() error = nil, want file missing")
	}
}

func TestDeriveNewContents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		original string
		path     string
		chunks   []UpdateFileChunk
		want     string
		wantErr  string
	}{
		{
			name:     "basic replacement",
			original: "one\ntwo\nthree\n",
			path:     "basic.txt",
			chunks: []UpdateFileChunk{
				{OldLines: []string{"two"}, NewLines: []string{"deux"}},
			},
			want: "one\ndeux\nthree\n",
		},
		{
			name:     "insertion when old lines empty",
			original: "alpha\n",
			path:     "insert.txt",
			chunks: []UpdateFileChunk{
				{NewLines: []string{"beta"}},
			},
			want: "alpha\nbeta\n",
		},
		{
			name:     "multiple replacements without index drift",
			original: "one\ntwo\nthree\nfour\n",
			path:     "drift.txt",
			chunks: []UpdateFileChunk{
				{OldLines: []string{"one"}, NewLines: []string{"ONE1", "ONE2"}},
				{OldLines: []string{"three"}, NewLines: []string{"THREE"}},
			},
			want: "ONE1\nONE2\ntwo\nTHREE\nfour\n",
		},
		{
			name:     "context advances search start",
			original: "anchor\nleft\nanchor\nright\n",
			path:     "context.txt",
			chunks: []UpdateFileChunk{
				{HasContext: true, ChangeContext: "anchor", OldLines: []string{"left"}, NewLines: []string{"LEFT"}},
				{HasContext: true, ChangeContext: "anchor", OldLines: []string{"right"}, NewLines: []string{"RIGHT"}},
			},
			want: "anchor\nLEFT\nanchor\nRIGHT\n",
		},
		{
			name:     "eof constrained match",
			original: "top\nneedle\nbottom\nneedle\n",
			path:     "eof.txt",
			chunks: []UpdateFileChunk{
				{OldLines: []string{"needle"}, NewLines: []string{"EOF"}, EndOfFile: true},
			},
			want: "top\nneedle\nbottom\nEOF\n",
		},
		{
			name:     "trailing empty old new retry",
			original: "alpha\nbeta\n",
			path:     "retry.txt",
			chunks: []UpdateFileChunk{
				{OldLines: []string{"beta", ""}, NewLines: []string{"BETA", ""}},
			},
			want: "alpha\nBETA\n",
		},
		{
			name:     "failure message when expected lines are missing",
			original: "alpha\n",
			path:     "file.txt",
			chunks: []UpdateFileChunk{
				{OldLines: []string{"missing"}},
			},
			wantErr: "failed to find expected lines in file.txt:\nmissing",
		},
		{
			name:     "output is newline terminated",
			original: "solo",
			path:     "newline.txt",
			want:     "solo\n",
		},
		{
			name:     "lenient whitespace matching via seek sequence",
			original: "alpha\ntrimmed   \nomega\n",
			path:     "whitespace.txt",
			chunks: []UpdateFileChunk{
				{OldLines: []string{"trimmed"}, NewLines: []string{"updated"}},
			},
			want: "alpha\nupdated\nomega\n",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := DeriveNewContents(tt.original, tt.path, tt.chunks)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("DeriveNewContents() error = nil, want %q", tt.wantErr)
				}
				if err.Error() != tt.wantErr {
					t.Fatalf("DeriveNewContents() error = %q, want %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DeriveNewContents() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("DeriveNewContents() = %q, want %q", got, tt.want)
			}
			if !strings.HasSuffix(got, "\n") {
				t.Fatalf("DeriveNewContents() output is not newline terminated: %q", got)
			}
		})
	}
}

func TestComputeReplacementsInsertionBeforeTrailingEmpty(t *testing.T) {
	t.Parallel()

	replacements, err := computeReplacements([]string{"root", ""}, "insert.txt", []UpdateFileChunk{
		{NewLines: []string{"child"}},
	})
	if err != nil {
		t.Fatalf("computeReplacements() error = %v", err)
	}
	if len(replacements) != 1 {
		t.Fatalf("computeReplacements() replacements = %d, want 1", len(replacements))
	}
	if replacements[0].Start != 1 {
		t.Fatalf("computeReplacements() start = %d, want 1", replacements[0].Start)
	}
	if replacements[0].OldLen != 0 {
		t.Fatalf("computeReplacements() old len = %d, want 0", replacements[0].OldLen)
	}
}

func TestApplyPatchDryRunWithNoHunks(t *testing.T) {
	t.Parallel()

	got, err := ApplyPatch(t.TempDir(), Patch{}, true, OSFS{})
	if err != nil {
		t.Fatalf("ApplyPatch() error = %v", err)
	}
	if !got.DryRun {
		t.Fatal("ApplyPatch() DryRun = false, want true")
	}
	if len(got.Added) != 0 || len(got.Modified) != 0 || len(got.Deleted) != 0 || len(got.Moved) != 0 {
		t.Fatalf("ApplyPatch() result = %#v, want empty result", got)
	}
}

func TestApplyPatchDuplicateTargetsBeforePlanning(t *testing.T) {
	t.Parallel()

	_, err := ApplyPatch(t.TempDir(), Patch{
		Hunks: []Hunk{
			AddFile{PathValue: "same.txt"},
			UpdateFile{PathValue: "other.txt", MovePath: "same.txt"},
		},
	}, true, OSFS{})
	if err == nil {
		t.Fatal("ApplyPatch() error = nil, want duplicate target error")
	}
	if !strings.Contains(err.Error(), "duplicate affected path") {
		t.Fatalf("ApplyPatch() error = %v, want duplicate target validation error", err)
	}
	if strings.Contains(err.Error(), "planning not implemented") {
		t.Fatalf("ApplyPatch() error = %v, want validation before planning", err)
	}
}

func TestApplyPatchNilFSDefaultsToOSFSInDryRun(t *testing.T) {
	t.Parallel()

	got, err := ApplyPatch(t.TempDir(), Patch{}, true, nil)
	if err != nil {
		t.Fatalf("ApplyPatch() error = %v", err)
	}
	if !got.DryRun {
		t.Fatal("ApplyPatch() DryRun = false, want true")
	}
}

func TestPlanAddFileFailsWhenTargetExists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "exists.txt")
	if err := os.WriteFile(path, []byte("present\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	_, err := planAddFile(root, AddFile{PathValue: "exists.txt", Contents: "new\n"}, OSFS{})
	if err == nil {
		t.Fatal("planAddFile() error = nil, want existing target error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("planAddFile() error = %v, want existing target validation error", err)
	}
}

func TestPlanAddFilePlansContentModeAndPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	got, err := planAddFile(root, AddFile{PathValue: "nested/new.txt", Contents: "hello\n"}, OSFS{})
	if err != nil {
		t.Fatalf("planAddFile() error = %v", err)
	}
	if got.Kind != ChangeAdd {
		t.Fatalf("planAddFile() Kind = %v, want %v", got.Kind, ChangeAdd)
	}
	if got.RelPath != "nested/new.txt" {
		t.Fatalf("planAddFile() RelPath = %q, want %q", got.RelPath, "nested/new.txt")
	}
	if got.Path != filepath.Join(root, "nested", "new.txt") {
		t.Fatalf("planAddFile() Path = %q, want %q", got.Path, filepath.Join(root, "nested", "new.txt"))
	}
	if string(got.NewContent) != "hello\n" {
		t.Fatalf("planAddFile() NewContent = %q, want %q", string(got.NewContent), "hello\n")
	}
	if got.Mode != 0o644 {
		t.Fatalf("planAddFile() Mode = %v, want %v", got.Mode, 0o644)
	}
}

func TestPlanDeleteFileRejectsDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatalf("os.Mkdir() error = %v", err)
	}

	_, err := planDeleteFile(root, DeleteFile{PathValue: "dir"}, OSFS{})
	if err == nil {
		t.Fatal("planDeleteFile() error = nil, want directory error")
	}
	if !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("planDeleteFile() error = %v, want directory validation error", err)
	}
}

func TestPlanDeleteFileRecordsOldContentAndMode(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "delete.txt")
	if err := os.WriteFile(path, []byte("gone\n"), 0o640); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	got, err := planDeleteFile(root, DeleteFile{PathValue: "delete.txt"}, OSFS{})
	if err != nil {
		t.Fatalf("planDeleteFile() error = %v", err)
	}
	if got.Kind != ChangeDelete {
		t.Fatalf("planDeleteFile() Kind = %v, want %v", got.Kind, ChangeDelete)
	}
	if got.RelPath != "delete.txt" {
		t.Fatalf("planDeleteFile() RelPath = %q, want %q", got.RelPath, "delete.txt")
	}
	if string(got.OldContent) != "gone\n" {
		t.Fatalf("planDeleteFile() OldContent = %q, want %q", string(got.OldContent), "gone\n")
	}
	if got.Mode != 0o640 {
		t.Fatalf("planDeleteFile() Mode = %v, want %v", got.Mode, 0o640)
	}
}

func TestPlanUpdateFileRejectsBinary(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "binary.dat")
	if err := os.WriteFile(path, []byte{'a', 0, 'b'}, 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	_, err := planUpdateFile(root, UpdateFile{PathValue: "binary.dat"}, OSFS{})
	if err == nil {
		t.Fatal("planUpdateFile() error = nil, want binary file error")
	}
	if !strings.Contains(err.Error(), "appears to be binary") {
		t.Fatalf("planUpdateFile() error = %v, want binary validation error", err)
	}
}

func TestPlanUpdateFileDerivesContentAndPreservesMode(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "update.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o640); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	got, err := planUpdateFile(root, UpdateFile{
		PathValue: "update.txt",
		Chunks: []UpdateFileChunk{
			{OldLines: []string{"old"}, NewLines: []string{"new"}},
		},
	}, OSFS{})
	if err != nil {
		t.Fatalf("planUpdateFile() error = %v", err)
	}
	if got.Kind != ChangeUpdate {
		t.Fatalf("planUpdateFile() Kind = %v, want %v", got.Kind, ChangeUpdate)
	}
	if got.RelPath != "update.txt" {
		t.Fatalf("planUpdateFile() RelPath = %q, want %q", got.RelPath, "update.txt")
	}
	if string(got.NewContent) != "new\n" {
		t.Fatalf("planUpdateFile() NewContent = %q, want %q", string(got.NewContent), "new\n")
	}
	if got.Mode != 0o640 {
		t.Fatalf("planUpdateFile() Mode = %v, want %v", got.Mode, 0o640)
	}
}

func TestPlanUpdateFileMoveRejectsExistingDestination(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("move\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "dest.txt"), []byte("present\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	_, err := planUpdateFile(root, UpdateFile{
		PathValue: "source.txt",
		MovePath:  "dest.txt",
		Chunks: []UpdateFileChunk{
			{OldLines: []string{"move"}, NewLines: []string{"move"}},
		},
	}, OSFS{})
	if err == nil {
		t.Fatal("planUpdateFile() error = nil, want destination exists error")
	}
	if !strings.Contains(err.Error(), "destination already exists") {
		t.Fatalf("planUpdateFile() error = %v, want destination exists validation error", err)
	}
}

func TestPlanUpdateFileMoveRecordsPathsAndMode(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("move\n"), 0o640); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	got, err := planUpdateFile(root, UpdateFile{
		PathValue: "source.txt",
		MovePath:  "nested/dest.txt",
		Chunks: []UpdateFileChunk{
			{OldLines: []string{"move"}, NewLines: []string{"move"}},
		},
	}, OSFS{})
	if err != nil {
		t.Fatalf("planUpdateFile() error = %v", err)
	}
	if got.Kind != ChangeMove {
		t.Fatalf("planUpdateFile() Kind = %v, want %v", got.Kind, ChangeMove)
	}
	if got.RelPath != "source.txt" {
		t.Fatalf("planUpdateFile() RelPath = %q, want %q", got.RelPath, "source.txt")
	}
	if got.MoveRelPath != "nested/dest.txt" {
		t.Fatalf("planUpdateFile() MoveRelPath = %q, want %q", got.MoveRelPath, "nested/dest.txt")
	}
	if got.Mode != 0o640 {
		t.Fatalf("planUpdateFile() Mode = %v, want %v", got.Mode, 0o640)
	}
}

func TestApplyPatchDryRunWithRealHunksPlansWithoutWriting(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	addPath := filepath.Join(root, "add.txt")
	deletePath := filepath.Join(root, "delete.txt")
	updatePath := filepath.Join(root, "update.txt")
	moveSourcePath := filepath.Join(root, "move.txt")
	moveDestPath := filepath.Join(root, "moved.txt")

	if err := os.WriteFile(deletePath, []byte("delete\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(delete) error = %v", err)
	}
	if err := os.WriteFile(updatePath, []byte("old\n"), 0o640); err != nil {
		t.Fatalf("os.WriteFile(update) error = %v", err)
	}
	if err := os.WriteFile(moveSourcePath, []byte("move\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(move) error = %v", err)
	}

	got, err := ApplyPatch(root, Patch{
		Hunks: []Hunk{
			AddFile{PathValue: "add.txt", Contents: "added\n"},
			DeleteFile{PathValue: "delete.txt"},
			UpdateFile{
				PathValue: "update.txt",
				Chunks: []UpdateFileChunk{
					{OldLines: []string{"old"}, NewLines: []string{"new"}},
				},
			},
			UpdateFile{
				PathValue: "move.txt",
				MovePath:  "moved.txt",
				Chunks: []UpdateFileChunk{
					{OldLines: []string{"move"}, NewLines: []string{"move"}},
				},
			},
		},
	}, true, OSFS{})
	if err != nil {
		t.Fatalf("ApplyPatch() error = %v", err)
	}
	if !got.DryRun {
		t.Fatal("ApplyPatch() DryRun = false, want true")
	}
	if len(got.Added) != 1 || got.Added[0] != "add.txt" {
		t.Fatalf("ApplyPatch() Added = %#v, want [add.txt]", got.Added)
	}
	if len(got.Deleted) != 1 || got.Deleted[0] != "delete.txt" {
		t.Fatalf("ApplyPatch() Deleted = %#v, want [delete.txt]", got.Deleted)
	}
	if len(got.Modified) != 1 || got.Modified[0] != "update.txt" {
		t.Fatalf("ApplyPatch() Modified = %#v, want [update.txt]", got.Modified)
	}
	if len(got.Moved) != 1 || got.Moved[0].From != "move.txt" || got.Moved[0].To != "moved.txt" {
		t.Fatalf("ApplyPatch() Moved = %#v, want [{move.txt moved.txt}]", got.Moved)
	}

	if _, err := os.Stat(addPath); !os.IsNotExist(err) {
		t.Fatalf("os.Stat(add) error = %v, want file not written", err)
	}
	if data, err := os.ReadFile(deletePath); err != nil || string(data) != "delete\n" {
		t.Fatalf("os.ReadFile(delete) = %q, %v, want unchanged", string(data), err)
	}
	if data, err := os.ReadFile(updatePath); err != nil || string(data) != "old\n" {
		t.Fatalf("os.ReadFile(update) = %q, %v, want unchanged", string(data), err)
	}
	if data, err := os.ReadFile(moveSourcePath); err != nil || string(data) != "move\n" {
		t.Fatalf("os.ReadFile(move source) = %q, %v, want unchanged", string(data), err)
	}
	if _, err := os.Stat(moveDestPath); !os.IsNotExist(err) {
		t.Fatalf("os.Stat(move dest) error = %v, want file not written", err)
	}
}

func TestBuildApplyResult(t *testing.T) {
	t.Parallel()

	got := buildApplyResult([]PlannedChange{
		{Kind: ChangeAdd, RelPath: "added.txt"},
		{Kind: ChangeUpdate, RelPath: "updated.txt"},
		{Kind: ChangeDelete, RelPath: "deleted.txt"},
		{Kind: ChangeMove, RelPath: "from.txt", MoveRelPath: "to.txt"},
	})

	if len(got.Added) != 1 || got.Added[0] != "added.txt" {
		t.Fatalf("buildApplyResult() Added = %#v, want [added.txt]", got.Added)
	}
	if len(got.Modified) != 1 || got.Modified[0] != "updated.txt" {
		t.Fatalf("buildApplyResult() Modified = %#v, want [updated.txt]", got.Modified)
	}
	if len(got.Deleted) != 1 || got.Deleted[0] != "deleted.txt" {
		t.Fatalf("buildApplyResult() Deleted = %#v, want [deleted.txt]", got.Deleted)
	}
	if len(got.Moved) != 1 || got.Moved[0].From != "from.txt" || got.Moved[0].To != "to.txt" {
		t.Fatalf("buildApplyResult() Moved = %#v, want [{from.txt to.txt}]", got.Moved)
	}
}

func TestApplyPatchNonDryRunCommitsChanges(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "delete.txt"), []byte("delete\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(delete) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "update.txt"), []byte("old\n"), 0o640); err != nil {
		t.Fatalf("os.WriteFile(update) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "move.txt"), []byte("move\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(move) error = %v", err)
	}

	got, err := ApplyPatch(root, Patch{
		Hunks: []Hunk{
			DeleteFile{PathValue: "delete.txt"},
			UpdateFile{
				PathValue: "update.txt",
				Chunks: []UpdateFileChunk{
					{OldLines: []string{"old"}, NewLines: []string{"new"}},
				},
			},
			AddFile{PathValue: "add.txt", Contents: "added\n"},
			UpdateFile{
				PathValue: "move.txt",
				MovePath:  "moved.txt",
				Chunks: []UpdateFileChunk{
					{OldLines: []string{"move"}, NewLines: []string{"moved"}},
				},
			},
		},
	}, false, OSFS{})
	if err != nil {
		t.Fatalf("ApplyPatch() error = %v", err)
	}
	if got.DryRun {
		t.Fatal("ApplyPatch() DryRun = true, want false")
	}

	if _, err := os.Stat(filepath.Join(root, "add.txt")); err != nil {
		t.Fatalf("os.Stat(add) error = %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "add.txt")); err != nil || string(data) != "added\n" {
		t.Fatalf("os.ReadFile(add) = %q, %v, want %q", string(data), err, "added\n")
	}
	if data, err := os.ReadFile(filepath.Join(root, "update.txt")); err != nil || string(data) != "new\n" {
		t.Fatalf("os.ReadFile(update) = %q, %v, want %q", string(data), err, "new\n")
	}
	if _, err := os.Stat(filepath.Join(root, "delete.txt")); !os.IsNotExist(err) {
		t.Fatalf("os.Stat(delete) error = %v, want file removed", err)
	}
	if _, err := os.Stat(filepath.Join(root, "move.txt")); !os.IsNotExist(err) {
		t.Fatalf("os.Stat(move source) error = %v, want file removed", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "moved.txt")); err != nil || string(data) != "moved\n" {
		t.Fatalf("os.ReadFile(move dest) = %q, %v, want %q", string(data), err, "moved\n")
	}
}

func TestCommitChangesOrdersKinds(t *testing.T) {
	t.Parallel()

	fsys := newFakeFS(map[string]fakeFile{
		"updates/update.txt": {data: []byte("old\n"), mode: 0o640},
		"moves/source.txt":   {data: []byte("move\n"), mode: 0o600},
		"deletes/delete.txt": {data: []byte("gone\n"), mode: 0o644},
	})

	err := commitChanges([]PlannedChange{
		{Kind: ChangeDelete, Path: "deletes/delete.txt"},
		{Kind: ChangeMove, Path: "moves/source.txt", MovePath: "moves/dest.txt", NewContent: []byte("move-new\n"), OldContent: []byte("move\n"), Mode: 0o600},
		{Kind: ChangeAdd, Path: "adds/add.txt", NewContent: []byte("add\n"), Mode: 0o644},
		{Kind: ChangeUpdate, Path: "updates/update.txt", NewContent: []byte("new\n"), OldContent: []byte("old\n"), Mode: 0o640},
	}, fsys)
	if err != nil {
		t.Fatalf("commitChanges() error = %v", err)
	}

	wantOps := []string{
		"mkdir:adds",
		"write:adds/add.txt",
		"write:updates/update.txt",
		"mkdir:moves",
		"write:moves/dest.txt",
		"remove:moves/source.txt",
		"remove:deletes/delete.txt",
	}
	if got := fsys.ops; !slicesEqual(got, wantOps) {
		t.Fatalf("commitChanges() ops = %#v, want %#v", got, wantOps)
	}
}

func TestCommitChangesRollsBackCommittedChanges(t *testing.T) {
	t.Parallel()

	fsys := newFakeFS(map[string]fakeFile{
		"updates/update.txt": {data: []byte("old\n"), mode: 0o640},
		"moves/source.txt":   {data: []byte("move\n"), mode: 0o600},
	})
	fsys.failWrite["moves/dest.txt"] = errors.New("boom")

	err := commitChanges([]PlannedChange{
		{Kind: ChangeMove, Path: "moves/source.txt", MovePath: "moves/dest.txt", NewContent: []byte("move-new\n"), OldContent: []byte("move\n"), Mode: 0o600},
		{Kind: ChangeUpdate, Path: "updates/update.txt", NewContent: []byte("new\n"), OldContent: []byte("old\n"), Mode: 0o640},
		{Kind: ChangeAdd, Path: "adds/add.txt", NewContent: []byte("add\n"), Mode: 0o644},
	}, fsys)
	if err == nil {
		t.Fatal("commitChanges() error = nil, want failure")
	}

	if data, ok := fsys.files["updates/update.txt"]; !ok || string(data.data) != "old\n" {
		t.Fatalf("rollback update content = %#v, want old content", data)
	}
	if _, ok := fsys.files["adds/add.txt"]; ok {
		t.Fatal("rollback add file still present, want removed")
	}
	if data, ok := fsys.files["moves/source.txt"]; !ok || string(data.data) != "move\n" {
		t.Fatalf("rollback move source = %#v, want original content", data)
	}
	if _, ok := fsys.files["moves/dest.txt"]; ok {
		t.Fatal("rollback move destination still present, want removed")
	}

	if len(fsys.ops) < 2 {
		t.Fatalf("commitChanges() ops = %#v, want rollback ops", fsys.ops)
	}
	wantTail := []string{"write:updates/update.txt", "remove:adds/add.txt"}
	if gotTail := fsys.ops[len(fsys.ops)-2:]; !slicesEqual(gotTail, wantTail) {
		t.Fatalf("commitChanges() rollback ops = %#v, want %#v", gotTail, wantTail)
	}
}

func TestCommitOneUnknownKindReturnsError(t *testing.T) {
	t.Parallel()

	err := commitOne(PlannedChange{Kind: "bogus"}, newFakeFS(nil))
	if err == nil {
		t.Fatal("commitOne() error = nil, want unknown kind error")
	}
	if want := `unknown change kind "bogus"`; err.Error() != want {
		t.Fatalf("commitOne() error = %q, want %q", err.Error(), want)
	}
}

type fakeFile struct {
	data []byte
	mode fs.FileMode
}

type fakeFS struct {
	files        map[string]fakeFile
	ops          []string
	failWrite    map[string]error
	failRemove   map[string]error
	failMkdirAll map[string]error
}

func newFakeFS(files map[string]fakeFile) *fakeFS {
	if files == nil {
		files = map[string]fakeFile{}
	}
	return &fakeFS{
		files:        files,
		failWrite:    map[string]error{},
		failRemove:   map[string]error{},
		failMkdirAll: map[string]error{},
	}
}

func (f *fakeFS) ReadFile(name string) ([]byte, error) {
	file, ok := f.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), file.data...), nil
}

func (f *fakeFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	f.ops = append(f.ops, "write:"+name)
	if err, ok := f.failWrite[name]; ok {
		return err
	}
	f.files[name] = fakeFile{data: append([]byte(nil), data...), mode: perm}
	return nil
}

func (f *fakeFS) Remove(name string) error {
	f.ops = append(f.ops, "remove:"+name)
	if err, ok := f.failRemove[name]; ok {
		return err
	}
	if _, ok := f.files[name]; !ok {
		return os.ErrNotExist
	}
	delete(f.files, name)
	return nil
}

func (f *fakeFS) MkdirAll(path string, perm fs.FileMode) error {
	f.ops = append(f.ops, "mkdir:"+path)
	if err, ok := f.failMkdirAll[path]; ok {
		return err
	}
	return nil
}

func (f *fakeFS) Stat(name string) (fs.FileInfo, error) {
	file, ok := f.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return fakeFileInfo{name: filepath.Base(name), size: int64(len(file.data)), mode: file.mode}, nil
}

type fakeFileInfo struct {
	name string
	size int64
	mode fs.FileMode
}

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return f.size }
func (f fakeFileInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }

func slicesEqual[T comparable](got, want []T) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
