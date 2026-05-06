package patchdoc

import (
	"reflect"
	"strings"
	"testing"
)

func TestParsePatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  *Patch
	}{
		{
			name:  "empty patch",
			input: "\n*** Begin Patch\n*** End Patch\n",
			want:  &Patch{},
		},
		{
			name: "add delete and update",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Add File: notes.txt",
				"+hello",
				"world",
				"++literal",
				"*** Delete File: old.txt",
				"*** Update File: src.txt",
				"*** Move to: dst.txt",
				"@@ base",
				" shared",
				"-old",
				"+new",
				"@@",
				" blank",
				"*** End of File",
				"*** End Patch",
			}, "\n"),
			want: &Patch{
				Hunks: []Hunk{
					AddFile{
						PathValue: "notes.txt",
						Contents:  "hello\nworld\n+literal\n",
					},
					DeleteFile{
						PathValue: "old.txt",
					},
					UpdateFile{
						PathValue: "src.txt",
						MovePath:  "dst.txt",
						Chunks: []UpdateFileChunk{
							{
								HasContext:    true,
								ChangeContext: "base",
								OldLines:      []string{"shared", "old"},
								NewLines:      []string{"shared", "new"},
							},
							{
								HasContext: true,
								OldLines:   []string{"blank"},
								NewLines:   []string{"blank"},
								EndOfFile:  true,
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Parse() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParsePatchErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name:    "missing begin marker",
			input:   "*** End Patch",
			wantErr: "patch must begin with",
		},
		{
			name: "unexpected top-level line",
			input: strings.Join([]string{
				"*** Begin Patch",
				"bogus",
				"*** End Patch",
			}, "\n"),
			wantErr: `unexpected top-level marker "bogus"`,
		},
		{
			name: "update chunk missing context",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Update File: src.txt",
				" line",
				"*** End Patch",
			}, "\n"),
			wantErr: `expected update chunk context marker`,
		},
		{
			name: "update chunk without body",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Update File: src.txt",
				"@@",
				"*** End Patch",
			}, "\n"),
			wantErr: "update chunk has no lines",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Parse(tt.input)
			if err == nil {
				t.Fatalf("Parse() error = nil, want %q", tt.wantErr)
			}
			if got != nil {
				t.Fatalf("Parse() = %#v, want nil", got)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Parse() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}
