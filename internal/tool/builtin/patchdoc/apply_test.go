package patchdoc

import (
	"path/filepath"
	"strings"
	"testing"
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
