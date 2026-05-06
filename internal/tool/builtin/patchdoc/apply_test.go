package patchdoc

import (
	"strings"
	"testing"
)

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
