package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSymbolPosition(t *testing.T) {
	tests := []struct {
		name        string
		fileContent string
		symbol      string
		line        int
		wantLine    int
		wantCol     int
		wantErr     string
	}{
		{
			name:        "unique match on specified line",
			fileContent: "package main\nfunc Foo() {\n}\n",
			symbol:      "Foo",
			line:        2,
			wantLine:    2,
			wantCol:     6, // "func Foo" — Foo starts at column 6 (1-based)
		},
		{
			name:        "unique match whole file",
			fileContent: "package main\nfunc Foo() {\n}\n",
			symbol:      "Foo",
			line:        0,
			wantLine:    2,
			wantCol:     6,
		},
		{
			name:        "Foo does not match inside FooBar",
			fileContent: "var x = FooBar\nvar y = Foo\n",
			symbol:      "Foo",
			line:        1,
			wantErr:     "not found on line 1",
		},
		{
			name:        "Foo matches after dot",
			fileContent: "m.Definitions(ctx, ...)\n",
			symbol:      "Definitions",
			line:        1,
			wantLine:    1,
			wantCol:     3, // "m.Definitions" — Definitions starts at column 3 (1-based)
		},
		{
			name:        "no match with line given",
			fileContent: "package main\nfunc Bar() {\n}\n",
			symbol:      "Foo",
			line:        2,
			wantErr:     "not found on line 2",
		},
		{
			name:        "no match file-wide",
			fileContent: "package main\nfunc Bar() {\n}\n",
			symbol:      "Foo",
			line:        0,
			wantErr:     "not found",
		},
		{
			name:        "multiple matches on same line uses leftmost",
			fileContent: "Foo = Foo\n",
			symbol:      "Foo",
			line:        1,
			wantLine:    1,
			wantCol:     1,
		},
		{
			name:        "multiple matches on different lines errors with list",
			fileContent: "func Foo() {}\nfunc Foo() {}\n",
			symbol:      "Foo",
			line:        0,
			wantErr:     "found on lines 1, 2",
		},
		{
			name:        "line out of range",
			fileContent: "line 1\nline 2\n",
			symbol:      "x",
			line:        10,
			wantErr:     "out of range",
		},
		{
			name:        "symbol with underscore and digits",
			fileContent: "var x_1 = 5\nvar x_1 = 10\n",
			symbol:      "x_1",
			line:        1,
			wantLine:    1,
			wantCol:     5,
		},
		{
			name:        "match at start of line",
			fileContent: "Foo bar\n",
			symbol:      "Foo",
			line:        1,
			wantLine:    1,
			wantCol:     1,
		},
		{
			name:        "match at end of line",
			fileContent: "bar Foo\n",
			symbol:      "Foo",
			line:        1,
			wantLine:    1,
			wantCol:     5,
		},
		{
			name:        "symbol not found in empty file",
			fileContent: "",
			symbol:      "Foo",
			line:        0,
			wantErr:     "not found",
		},
		{
			name:        "crlf line endings",
			fileContent: "func Foo() {}\r\nfunc Bar() {}\r\n",
			symbol:      "Foo",
			line:        1,
			wantLine:    1,
			wantCol:     6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpdir := t.TempDir()
			testFile := filepath.Join(tmpdir, "test.go")
			if err := os.WriteFile(testFile, []byte(tt.fileContent), 0o644); err != nil {
				t.Fatalf("write file: %v", err)
			}

			gotFile, gotLine, gotCol, err := resolveSymbolPosition(tmpdir, "test.go", tt.symbol, tt.line)

			if tt.wantErr != "" {
				if err == nil {
					t.Errorf("resolveSymbolPosition: got nil error, want error containing %q", tt.wantErr)
				} else if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("resolveSymbolPosition: error = %q, want substring %q", err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Errorf("resolveSymbolPosition: got error %v", err)
				return
			}

			if gotLine != tt.wantLine {
				t.Errorf("resolveSymbolPosition: line = %d, want %d", gotLine, tt.wantLine)
			}
			if gotCol != tt.wantCol {
				t.Errorf("resolveSymbolPosition: column = %d, want %d", gotCol, tt.wantCol)
			}
			if gotFile != testFile {
				t.Errorf("resolveSymbolPosition: file = %q, want %q", gotFile, testFile)
			}
		})
	}
}

func TestResolveSymbolPositionAbsolutePath(t *testing.T) {
	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "test.go")
	if err := os.WriteFile(testFile, []byte("func Foo() {}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	gotFile, gotLine, gotCol, err := resolveSymbolPosition(tmpdir, testFile, "Foo", 0)
	if err != nil {
		t.Errorf("resolveSymbolPosition with absolute path: got error %v", err)
	}
	if gotLine != 1 || gotCol != 6 {
		t.Errorf("resolveSymbolPosition: got (%d, %d), want (1, 6)", gotLine, gotCol)
	}
	if gotFile != testFile {
		t.Errorf("resolveSymbolPosition: file = %q, want %q", gotFile, testFile)
	}
}

func TestResolveSymbolPositionEmptySymbol(t *testing.T) {
	tmpdir := t.TempDir()
	_, _, _, err := resolveSymbolPosition(tmpdir, "test.go", "", 1)
	if err == nil {
		t.Errorf("resolveSymbolPosition with empty symbol: got nil error, want error")
	}
}

func TestResolveSymbolPositionLineZeroVsAbsent(t *testing.T) {
	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "test.go")
	if err := os.WriteFile(testFile, []byte("Foo\nBar\nFoo\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// line=0 and line not provided should both scan whole file.
	// Foo appears on lines 1 and 3, so should error with ambiguous match.
	_, _, _, err := resolveSymbolPosition(tmpdir, "test.go", "Foo", 0)
	if err == nil {
		t.Errorf("resolveSymbolPosition: expected error for ambiguous match, got nil")
	}
	if !strings.Contains(err.Error(), "found on lines") {
		t.Errorf("resolveSymbolPosition: error = %q, want substring 'found on lines'", err.Error())
	}
}

func TestResolveSymbolPositionMaxListedLines(t *testing.T) {
	// Create a file with many matches (more than 20).
	var content string
	for i := 1; i <= 25; i++ {
		content += "Foo\n"
	}

	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "test.go")
	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, _, _, err := resolveSymbolPosition(tmpdir, "test.go", "Foo", 0)
	if err == nil {
		t.Errorf("resolveSymbolPosition: expected error for many matches, got nil")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "20") || !strings.Contains(errMsg, "and 5 more") {
		t.Errorf("resolveSymbolPosition: error = %q, want to contain '20' and 'and 5 more'", errMsg)
	}
}
