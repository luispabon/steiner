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
			name: "parse add file",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Add File: notes.txt",
				"+hello",
				"world",
				"++literal",
				"*** End Patch",
			}, "\n"),
			want: &Patch{
				Hunks: []Hunk{
					AddFile{
						PathValue: "notes.txt",
						Contents:  "hello\nworld\n+literal\n",
					},
				},
			},
		},
		{
			name: "parse delete file",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Delete File: old.txt",
				"*** End Patch",
			}, "\n"),
			want: &Patch{
				Hunks: []Hunk{
					DeleteFile{PathValue: "old.txt"},
				},
			},
		},
		{
			name: "parse update file",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Update File: src.txt",
				"@@ base",
				" shared",
				"-old",
				"+new",
				"*** End Patch",
			}, "\n"),
			want: &Patch{
				Hunks: []Hunk{
					UpdateFile{
						PathValue: "src.txt",
						Chunks: []UpdateFileChunk{
							{
								HasContext:    true,
								ChangeContext: "base",
								OldLines:      []string{"shared", "old"},
								NewLines:      []string{"shared", "new"},
							},
						},
					},
				},
			},
		},
		{
			name: "parse update with move",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Update File: src.txt",
				"*** Move to: dst.txt",
				"@@",
				" shared",
				"-old",
				"+new",
				"*** End Patch",
			}, "\n"),
			want: &Patch{
				Hunks: []Hunk{
					UpdateFile{
						PathValue: "src.txt",
						MovePath:  "dst.txt",
						Chunks: []UpdateFileChunk{
							{
								OldLines: []string{"shared", "old"},
								NewLines: []string{"shared", "new"},
							},
						},
					},
				},
			},
		},
		{
			name: "parse multiple operations",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Add File: notes.txt",
				"+hello",
				"*** Delete File: old.txt",
				"*** Update File: src.txt",
				"@@ first",
				" shared",
				"-old",
				"+new",
				"@@ second",
				" keep",
				"-gone",
				"+stay",
				"*** End Patch",
			}, "\n"),
			want: &Patch{
				Hunks: []Hunk{
					AddFile{PathValue: "notes.txt", Contents: "hello\n"},
					DeleteFile{PathValue: "old.txt"},
					UpdateFile{
						PathValue: "src.txt",
						Chunks: []UpdateFileChunk{
							{
								HasContext:    true,
								ChangeContext: "first",
								OldLines:      []string{"shared", "old"},
								NewLines:      []string{"shared", "new"},
							},
							{
								HasContext:    true,
								ChangeContext: "second",
								OldLines:      []string{"keep", "gone"},
								NewLines:      []string{"keep", "stay"},
							},
						},
					},
				},
			},
		},
		{
			name: "parse empty context marker",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Update File: src.txt",
				"@@",
				" shared",
				"-old",
				"+new",
				"*** End Patch",
			}, "\n"),
			want: &Patch{
				Hunks: []Hunk{
					UpdateFile{
						PathValue: "src.txt",
						Chunks: []UpdateFileChunk{
							{
								HasContext: false,
								OldLines:   []string{"shared", "old"},
								NewLines:   []string{"shared", "new"},
							},
						},
					},
				},
			},
		},
		{
			name: "parse context marker",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Update File: src.txt",
				"@@ function",
				" shared",
				"-old",
				"+new",
				"*** End Patch",
			}, "\n"),
			want: &Patch{
				Hunks: []Hunk{
					UpdateFile{
						PathValue: "src.txt",
						Chunks: []UpdateFileChunk{
							{
								HasContext:    true,
								ChangeContext: "function",
								OldLines:      []string{"shared", "old"},
								NewLines:      []string{"shared", "new"},
							},
						},
					},
				},
			},
		},
		{
			name: "parse first update chunk without context marker",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Update File: src.txt",
				"-old",
				"+new",
				"*** End Patch",
			}, "\n"),
			want: &Patch{
				Hunks: []Hunk{
					UpdateFile{
						PathValue: "src.txt",
						Chunks: []UpdateFileChunk{
							{
								OldLines: []string{"old"},
								NewLines: []string{"new"},
							},
						},
					},
				},
			},
		},
		{
			name: "parse subsequent update chunk requiring context marker",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Update File: src.txt",
				"-old",
				"+new",
				"@@ second",
				" keep",
				"-gone",
				"+stay",
				"*** End Patch",
			}, "\n"),
			want: &Patch{
				Hunks: []Hunk{
					UpdateFile{
						PathValue: "src.txt",
						Chunks: []UpdateFileChunk{
							{
								OldLines: []string{"old"},
								NewLines: []string{"new"},
							},
							{
								HasContext:    true,
								ChangeContext: "second",
								OldLines:      []string{"keep", "gone"},
								NewLines:      []string{"keep", "stay"},
							},
						},
					},
				},
			},
		},
		{
			name: "parse empty line as empty context line",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Update File: src.txt",
				"@@",
				"",
				"*** End of File",
				"*** End Patch",
			}, "\n"),
			want: &Patch{
				Hunks: []Hunk{
					UpdateFile{
						PathValue: "src.txt",
						Chunks: []UpdateFileChunk{
							{
								OldLines:  []string{""},
								NewLines:  []string{""},
								EndOfFile: true,
							},
						},
					},
				},
			},
		},
		{
			name: "parse eof marker",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Update File: src.txt",
				"@@",
				" shared",
				"-old",
				"+new",
				"*** End of File",
				"*** End Patch",
			}, "\n"),
			want: &Patch{
				Hunks: []Hunk{
					UpdateFile{
						PathValue: "src.txt",
						Chunks: []UpdateFileChunk{
							{
								OldLines:   []string{"shared", "old"},
								NewLines:   []string{"shared", "new"},
								EndOfFile:  true,
								HasContext: false,
							},
						},
					},
				},
			},
		},
		{
			name: "empty add file allowed",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Add File: empty.txt",
				"*** End Patch",
			}, "\n"),
			want: &Patch{
				Hunks: []Hunk{
					AddFile{PathValue: "empty.txt", Contents: ""},
				},
			},
		},
		{
			name: "add file with triple asterisk content",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Add File: poem.txt",
				"+first line",
				"+*** stars ***",
				"+last line",
				"*** End Patch",
			}, "\n"),
			want: &Patch{
				Hunks: []Hunk{
					AddFile{
						PathValue: "poem.txt",
						Contents:  "first line\n*** stars ***\nlast line\n",
					},
				},
			},
		},
		{
			name: "add file with markdown HR",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Add File: readme.txt",
				"+before",
				"+***",
				"+after",
				"*** End Patch",
			}, "\n"),
			want: &Patch{
				Hunks: []Hunk{
					AddFile{
						PathValue: "readme.txt",
						Contents:  "before\n***\nafter\n",
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
			name: "missing end marker",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Add File: note.txt",
				"+hello",
			}, "\n"),
			wantErr: `expected "*** End Patch"`,
		},
		{
			name: "heredoc wrapper is malformed",
			input: strings.Join([]string{
				"cat <<'EOF'",
				"*** Begin Patch",
				"*** Add File: note.txt",
				"+hello",
				"*** End Patch",
				"EOF",
			}, "\n"),
			wantErr: `expected "*** Begin Patch"`,
		},
		{
			name: "unknown hunk header",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Rename File: note.txt",
				"*** End Patch",
			}, "\n"),
			wantErr: `supported markers are "*** Add File:", "*** Update File:", and "*** Delete File:"`,
		},
		{
			name: "unsupported write file marker",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Write File: note.txt",
				"*** End Patch",
			}, "\n"),
			wantErr: `supported markers are "*** Add File:", "*** Update File:", and "*** Delete File:"`,
		},
		{
			name: "update file with no chunks",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Update File: src.txt",
				"*** End Patch",
			}, "\n"),
			wantErr: `update file "src.txt" has no chunks`,
		},
		{
			name: "unexpected raw heading in update hunk",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Update File: src.txt",
				"@@",
				"# heading",
				"*** End Patch",
			}, "\n"),
			wantErr: `prefix raw "#" lines with a leading space`,
		},
		{
			name: "unexpected hunk line",
			input: strings.Join([]string{
				"*** Begin Patch",
				"*** Update File: src.txt",
				"@@",
				"?bogus",
				"*** End Patch",
			}, "\n"),
			wantErr: `prefix raw "?" lines with a leading space`,
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
