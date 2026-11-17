package builtin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// TestShellQuote pins the single-quote encoding: the whole word is quoted and
// an embedded single quote is closed, escaped, and reopened.
func TestShellQuote(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: "''"},
		{name: "plain", in: "plain", want: "'plain'"},
		{name: "spaces", in: "dir with spaces", want: "'dir with spaces'"},
		{name: "single quote", in: "it's", want: `'it'\''s'`},
		{name: "only single quotes", in: "''", want: `''\'''\'''`},
		{name: "expansions", in: "$HOME `id` $(id)", want: "'$HOME `id` $(id)'"},
		{name: "backslash and double quote", in: `a\b "c"`, want: "'a\\b \"c\"'"},
		{name: "newline", in: "a\nb", want: "'a\nb'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shellQuote(tt.in); got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestBashToolCWDIsLiteral proves the resolved cwd reaches bash as one literal
// word: dollar expansion, command substitution, backticks, quotes, backslashes,
// shell metacharacters, spaces and newlines in directory names neither break the
// cd nor execute as shell code.
func TestBashToolCWDIsLiteral(t *testing.T) {
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}

	// Command substitution and backticks in the cwd would run in the session's
	// cwd, which is this process's cwd because the session is started without
	// an explicit Dir.
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	marker := "steiner-bash-cwd-injection-marker"
	markerPaths := []string{filepath.Join(origWD, marker), filepath.Join(root, marker)}
	clearMarkers := func() {
		for _, path := range markerPaths {
			_ = os.Remove(path)
		}
	}
	t.Cleanup(clearMarkers)

	tests := []struct {
		name string
		dir  string
	}{
		{name: "spaces", dir: "dir with spaces"},
		{name: "dollar expansion", dir: "dir $HOME $USER ${PATH}"},
		{name: "command substitution", dir: "dir $(touch " + marker + ")"},
		{name: "backticks", dir: "dir `touch " + marker + "`"},
		{name: "single quotes", dir: "dir 'single' quotes"},
		{name: "double quotes", dir: `dir "double" quotes`},
		{name: "backslash", dir: `dir back\slash`},
		{name: "glob metacharacters", dir: "dir * ? [abc] ~root"},
		{name: "shell operators", dir: "dir; touch " + marker + " & true"},
		{name: "newline", dir: "dir\nnewline"},
		{name: "tab", dir: "dir\ttab"},
	}

	policy := tool.NewPathPolicy(root, config.PathsConfig{})
	toolDef := NewBashTool(Env{WorkDir: root, PathPolicy: &policy})
	ctx := withUnsandboxedWrapper(context.Background())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantDir := filepath.Join(root, tt.dir)
			if err := os.MkdirAll(wantDir, 0o755); err != nil {
				t.Fatalf("mkdir %q: %v", wantDir, err)
			}
			// A failing subtest must not leave a marker behind for the next one.
			clearMarkers()
			t.Cleanup(clearMarkers)

			resultI, err := toolDef.Handler(ctx, map[string]any{
				"command": `printf 'CWD=<%s>' "$PWD"`,
				"cwd":     wantDir,
			})
			if err != nil {
				t.Fatalf("Handler() error = %v", err)
			}
			result, ok := resultI.(*BashResult)
			if !ok {
				t.Fatalf("Handler() result type = %T, want *BashResult", resultI)
			}
			if result.ExitCode != 0 {
				t.Errorf("ExitCode = %d, output = %q", result.ExitCode, result.Output)
			}
			for _, injected := range markerPaths {
				if _, statErr := os.Stat(injected); statErr == nil {
					t.Errorf("cwd was shell-expanded: %q exists", injected)
				}
			}
			if want := "CWD=<" + wantDir + ">"; result.Output != want {
				t.Errorf("Output = %q, want %q", result.Output, want)
			}
		})
	}
}
