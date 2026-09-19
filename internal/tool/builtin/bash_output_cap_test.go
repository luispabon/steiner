package builtin

import (
	"bufio"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestBashToolTruncatedFlag(t *testing.T) {
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

func TestReadUntilMarkerCapsAndFindsMarker(t *testing.T) {
	const marker = "__M__"
	long := strings.Repeat("a", 20000)
	tests := []struct {
		name      string
		input     string
		max       int
		want      string
		truncated bool
	}{
		{"under cap", "one\ntwo\n" + marker + "\n", 100, "one\ntwo\n", false},
		{"cap drops tail", "abcdef\nghij\n" + marker + "\n", 4, "abcd", true},
		{"very long line then marker", long + "\n" + marker + "\n", 10, strings.Repeat("a", 10), true},
		{"long line equal to marker prefix is not marker", marker + long + "\n" + marker + "\n", 1 << 20, marker + long + "\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, trunc, err := readUntilMarker(newTestReader(tt.input), marker, tt.max)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != tt.want || trunc != tt.truncated {
				t.Errorf("got (%q, %v), want (%q, %v)", got, trunc, tt.want, tt.truncated)
			}
		})
	}
}

func TestBashSessionLargeOutputKeepsExitCode(t *testing.T) {
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

func newTestReader(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }
