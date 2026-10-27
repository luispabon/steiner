package builtin

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestBashToolTruncatedFlag(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	policy := tool.NewPathPolicy(dir, config.PathsConfig{})
	toolDef := NewBashTool(Env{WorkDir: dir, PathPolicy: &policy})
	ctx := withUnsandboxedWrapper(context.Background())

	tests := []struct {
		name      string
		command   string
		truncated bool
	}{
		{"literal marker in small output", "echo '[output truncated]'", false},
		{"multi-megabyte output", "yes x | head -c 5000000; echo done >&2", true},
		{"multi-megabyte stderr", "yes y | head -c 5000000 >&2", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resI, err := toolDef.Handler(ctx, map[string]any{"command": tt.command})
			if err != nil {
				t.Fatalf("handler: %v", err)
			}
			res := resI.(*BashResult)
			if res.ExitCode != 0 {
				t.Errorf("ExitCode = %d, want 0", res.ExitCode)
			}
			if res.Truncated != tt.truncated {
				t.Errorf("Truncated = %v, want %v", res.Truncated, tt.truncated)
			}
			if len(res.Output) > 2*bashSessionMaxOutput {
				t.Errorf("Output length = %d, want bounded", len(res.Output))
			}
		})
	}
}

func TestBashSessionLargeOutputKeepsExitCode(t *testing.T) {
	t.Parallel()
	s := NewBashSession()
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, _, code, err := s.Execute(ctx, "yes x | head -c 4000000; (exit 7)")
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
	if len(out) > bashSessionMaxOutput+len("\n[output truncated]") {
		t.Errorf("output length = %d, want bounded", len(out))
	}
	if !strings.HasSuffix(out, "[output truncated]") {
		t.Errorf("output missing truncation marker")
	}
}
