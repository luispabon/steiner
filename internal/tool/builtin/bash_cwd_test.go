package builtin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

// TestBashToolRejectsNULCWD proves a cwd containing a NUL byte is refused
// before the command is built. bash drops NUL bytes from the command it reads,
// so ".\x00." would collapse to ".." and the command would run in a directory
// the caller never asked for. Path policies that skip root containment accept
// such a cwd, so the check belongs to the tool.
func TestBashToolRejectsNULCWD(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", realDir, err)
	}

	// An absolute marker proves the command did not run, wherever bash would
	// have put it.
	markerPath := filepath.Join(base, "steiner-bash-cwd-nul-marker")
	t.Cleanup(func() { _ = os.Remove(markerPath) })
	command := `touch '` + markerPath + `'`

	tests := []struct {
		name            string
		cwd             string
		rootless        bool
		projectRootOnly bool
	}{
		{
			name:     "rootless policy, trailing NUL",
			cwd:      realDir + "\x00",
			rootless: true,
		},
		{
			name:     "rootless policy, NUL between dot segments",
			cwd:      realDir + "/.\x00.",
			rootless: true,
		},
		{
			name: "containment disabled, trailing NUL",
			cwd:  realDir + "\x00",
		},
		{
			name: "containment disabled, NUL between dot segments",
			cwd:  realDir + "/.\x00.",
		},
		{
			name:            "containment enabled, trailing NUL",
			cwd:             realDir + "\x00",
			projectRootOnly: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := base
			if tt.rootless {
				root = ""
			}
			policy := tool.NewPathPolicy(root, config.PathsConfig{ProjectRootOnly: tt.projectRootOnly})
			toolDef := NewBashTool(Env{WorkDir: base, PathPolicy: &policy})
			ctx := withUnsandboxedWrapper(context.Background())

			resultI, err := toolDef.Handler(ctx, map[string]any{
				"command": command,
				"cwd":     tt.cwd,
			})
			if err == nil {
				t.Fatalf("Handler() result = %#v, want NUL rejection error", resultI)
			}
			if !strings.Contains(err.Error(), "NUL") {
				t.Errorf("Handler() error = %v, want a NUL rejection message", err)
			}
			if _, statErr := os.Stat(markerPath); statErr == nil {
				t.Errorf("command ran despite the invalid cwd: %q exists", markerPath)
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
