package builtin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestGrepContextAppliesConfiguredDeadline(t *testing.T) {
	timeout := 30 * time.Second
	before := time.Now()

	ctx, cancel := grepContext(context.Background(), timeout)
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("grepContext with a positive timeout produced no deadline")
	}
	if deadline.Before(before.Add(timeout)) || deadline.After(time.Now().Add(timeout)) {
		t.Fatalf("deadline = %v, want a point near now+%v", deadline, timeout)
	}
}

func TestGrepContextKeepsEarlierCallerDeadline(t *testing.T) {
	parentDeadline := time.Now().Add(time.Minute)
	parent, cancelParent := context.WithDeadline(context.Background(), parentDeadline)
	defer cancelParent()

	ctx, cancel := grepContext(parent, time.Hour)
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok || !deadline.Equal(parentDeadline) {
		t.Fatalf("deadline = %v (ok=%v), want the caller deadline %v", deadline, ok, parentDeadline)
	}
}

func TestGrepContextIgnoresNonPositiveTimeout(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
	}{
		{"zero", 0},
		{"negative", -time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent, cancelParent := context.WithTimeout(context.Background(), time.Hour)
			defer cancelParent()

			ctx, cancel := grepContext(parent, tt.timeout)
			defer cancel()

			if ctx != parent {
				t.Fatalf("grepContext derived a context for timeout %v, want the caller context", tt.timeout)
			}
		})
	}
}

func TestGrepToolSucceedsWithinConfiguredTimeout(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "match.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatalf("write match file: %v", err)
	}
	policy := tool.NewPathPolicy(dir, config.PathsConfig{})
	def := NewGrepTool(Env{WorkDir: dir, PathPolicy: &policy, GrepTimeout: 30 * time.Second})

	result, err := def.Handler(context.Background(), map[string]any{"pattern": "needle"})
	if err != nil {
		t.Fatalf("grep handler: %v", err)
	}
	res, ok := result.(GrepResult)
	if !ok {
		t.Fatalf("result type = %T, want GrepResult", result)
	}
	if res.Shown != 1 || !strings.Contains(res.Output, "match.txt") {
		t.Errorf("Shown = %d, Output = %q, want one row for match.txt", res.Shown, res.Output)
	}
}

func TestGrepToolInterruptedSearchErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "match.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatalf("write match file: %v", err)
	}
	policy := tool.NewPathPolicy(dir, config.PathsConfig{})
	def := NewGrepTool(Env{WorkDir: dir, PathPolicy: &policy, GrepTimeout: time.Minute})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := def.Handler(ctx, map[string]any{"pattern": "needle"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if result != nil {
		t.Fatalf("result = %#v, want nil when the search is interrupted", result)
	}
}
