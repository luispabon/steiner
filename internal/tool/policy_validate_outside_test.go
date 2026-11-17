package tool

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

// TestValidateToolInput_MutateCollectsEveryOutsidePath pins that a mutate batch
// reports every distinct outside path before the user is asked, including both
// sides of a move and relative paths normalized against the policy root.
func TestValidateToolInput_MutateCollectsEveryOutsidePath(t *testing.T) {
	policy := NewPathPolicy("/project", config.PathsConfig{ProjectRootOnly: true})

	tests := []struct {
		name      string
		ops       []any
		wantPaths []string
		wantCount int
	}{
		{
			name: "every operation in the batch",
			ops: []any{
				map[string]any{"type": "create", "path": "notes.txt", "content": "x"},
				map[string]any{"type": "write", "path": "/etc/passwd", "content": "x"},
				map[string]any{"type": "move", "from": "/var/log/app.log", "to": "/tmp/app.log"},
			},
			wantPaths: []string{"/etc/passwd", "/var/log/app.log", "/tmp/app.log"},
			wantCount: 3,
		},
		{
			name: "relative path normalizes against the root",
			ops: []any{
				map[string]any{"type": "write", "path": "../sibling/file.txt", "content": "x"},
			},
			wantPaths: []string{"/sibling/file.txt"},
			wantCount: 1,
		},
		{
			name: "repeated path counts once",
			ops: []any{
				map[string]any{"type": "write", "path": "/etc/passwd", "content": "x"},
				map[string]any{"type": "write", "path": "/etc/passwd", "content": "y"},
				map[string]any{"type": "move", "from": "/etc/passwd", "to": "inside.txt"},
			},
			wantPaths: []string{"/etc/passwd"},
			wantCount: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := policy.ValidateToolInput("mutate", map[string]any{"operations": tt.ops})
			var policyErr *PathPolicyError
			if !errors.As(err, &policyErr) {
				t.Fatalf("ValidateToolInput() error = %v, want *PathPolicyError", err)
			}
			if !policyErr.Promptable {
				t.Fatal("Promptable = false, want true for outside-root paths")
			}
			if !slices.Equal(policyErr.Paths, tt.wantPaths) {
				t.Fatalf("Paths = %v, want %v", policyErr.Paths, tt.wantPaths)
			}
			if policyErr.PathCount != tt.wantCount {
				t.Fatalf("PathCount = %d, want %d", policyErr.PathCount, tt.wantCount)
			}
			if policyErr.Path != tt.wantPaths[0] {
				t.Fatalf("Path = %q, want first collected path %q", policyErr.Path, tt.wantPaths[0])
			}
		})
	}
}

// TestValidateToolInput_MutateOutsidePathsAreBounded pins that a huge batch
// keeps the prompt list bounded while still counting every outside path.
func TestValidateToolInput_MutateOutsidePathsAreBounded(t *testing.T) {
	policy := NewPathPolicy("/project", config.PathsConfig{ProjectRootOnly: true})

	ops := make([]any, 0, maxCollectedOutsidePaths+5)
	for i := range maxCollectedOutsidePaths + 5 {
		ops = append(ops, map[string]any{"type": "write", "path": fmt.Sprintf("/outside/file-%02d.txt", i), "content": "x"})
	}

	_, err := policy.ValidateToolInput("mutate", map[string]any{"operations": ops})
	var policyErr *PathPolicyError
	if !errors.As(err, &policyErr) {
		t.Fatalf("ValidateToolInput() error = %v, want *PathPolicyError", err)
	}
	if want := maxCollectedOutsidePaths; len(policyErr.Paths) != want {
		t.Fatalf("len(Paths) = %d, want %d", len(policyErr.Paths), want)
	}
	if want := maxCollectedOutsidePaths + 5; policyErr.PathCount != want {
		t.Fatalf("PathCount = %d, want %d", policyErr.PathCount, want)
	}
	if got := policyErr.Paths[len(policyErr.Paths)-1]; got != "/outside/file-19.txt" {
		t.Fatalf("last collected path = %q, want '/outside/file-19.txt'", got)
	}
}

// TestValidateToolInput_MutateOutsidePathHonoursNonPromptableRules pins that an
// outside path which also breaks a rule approval cannot relax is denied without
// a prompt. Approving such a call could never let it run, and the docs promise
// no prompt.
func TestValidateToolInput_MutateOutsidePathHonoursNonPromptableRules(t *testing.T) {
	tests := []struct {
		name   string
		config config.PathsConfig
		path   string
		want   string
	}{
		{
			name:   "blocked directory outside the root",
			config: config.PathsConfig{ProjectRootOnly: true, BlockedPaths: []string{"/blocked"}},
			path:   "/blocked/secret.txt",
			want:   "blocked by policy",
		},
		{
			name:   "writable allowlist outside the root",
			config: config.PathsConfig{ProjectRootOnly: true, WritablePaths: []string{"/allowed"}},
			path:   "/elsewhere/notes.txt",
			want:   "not in the writable allowlist",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := NewPathPolicy("/project", tt.config)
			_, err := policy.ValidateToolInput("mutate", map[string]any{
				"operations": []any{
					map[string]any{"type": "write", "path": tt.path, "content": "x"},
				},
			})
			var policyErr *PathPolicyError
			if !errors.As(err, &policyErr) {
				t.Fatalf("ValidateToolInput() error = %v, want *PathPolicyError", err)
			}
			if policyErr.Promptable {
				t.Fatalf("Promptable = true for %q, want false so the call is denied without a prompt", tt.path)
			}
			if !strings.Contains(policyErr.Error(), tt.want) {
				t.Fatalf("Error() = %q, want it to contain %q", policyErr.Error(), tt.want)
			}
		})
	}
}

// TestValidateToolInput_MutateOutsidePathStillPrompts pins that closing the
// prompt hole did not turn ordinary outside paths into denials.
func TestValidateToolInput_MutateOutsidePathStillPrompts(t *testing.T) {
	policy := NewPathPolicy("/project", config.PathsConfig{ProjectRootOnly: true, BlockedPaths: []string{"/project/blocked"}})

	_, err := policy.ValidateToolInput("mutate", map[string]any{
		"operations": []any{
			map[string]any{"type": "write", "path": "/etc/passwd", "content": "x"},
		},
	})
	var policyErr *PathPolicyError
	if !errors.As(err, &policyErr) {
		t.Fatalf("ValidateToolInput() error = %v, want *PathPolicyError", err)
	}
	if !policyErr.Promptable {
		t.Fatalf("Promptable = false, want true for an outside path no other rule blocks: %v", policyErr)
	}
	if want := []string{"/etc/passwd"}; !slices.Equal(policyErr.Paths, want) {
		t.Fatalf("Paths = %v, want %v", policyErr.Paths, want)
	}
}

// TestValidateToolInput_MutatePlanWriteLimitOutsideRoot pins that a restriction
// independent of the root still fails an outside path closed in plan mode.
func TestValidateToolInput_MutatePlanWriteLimitOutsideRoot(t *testing.T) {
	policy := NewPathPolicy("/project", config.PathsConfig{ProjectRootOnly: true}).RestrictWritesTo("/project")

	_, err := policy.ValidateToolInput("mutate", map[string]any{
		"operations": []any{
			map[string]any{"type": "write", "path": "/etc/passwd", "content": "x"},
		},
	})
	var policyErr *PathPolicyError
	if !errors.As(err, &policyErr) {
		t.Fatalf("ValidateToolInput() error = %v, want *PathPolicyError", err)
	}
	if policyErr.Promptable {
		t.Fatalf("Promptable = true, want false for a plan-mode write limit: %v", policyErr)
	}
}

// TestValidateToolInput_MutateNonPromptableViolationFailsClosed pins that a
// batch cannot buy a prompt with one outside path while another operation
// breaks a rule the user cannot approve.
func TestValidateToolInput_MutateNonPromptableViolationFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		config config.PathsConfig
		want   string
	}{
		{
			name:   "blocked path after an outside path",
			config: config.PathsConfig{ProjectRootOnly: true, BlockedPaths: []string{"/project/blocked"}},
			want:   "blocked by policy",
		},
		{
			name:   "writable allowlist after an outside path",
			config: config.PathsConfig{ProjectRootOnly: true, WritablePaths: []string{"/project/allowed"}},
			want:   "not in the writable allowlist",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := NewPathPolicy("/project", tt.config)
			_, err := policy.ValidateToolInput("mutate", map[string]any{
				"operations": []any{
					map[string]any{"type": "write", "path": "/etc/passwd", "content": "x"},
					map[string]any{"type": "write", "path": "blocked/secret.txt", "content": "x"},
					map[string]any{"type": "write", "path": "elsewhere/other.txt", "content": "x"},
				},
			})
			var policyErr *PathPolicyError
			if !errors.As(err, &policyErr) {
				t.Fatalf("ValidateToolInput() error = %v, want *PathPolicyError", err)
			}
			if policyErr.Promptable {
				t.Fatal("Promptable = true, want false so the call is denied without a prompt")
			}
			if !strings.Contains(policyErr.Error(), tt.want) {
				t.Fatalf("Error() = %q, want it to contain %q", policyErr.Error(), tt.want)
			}
		})
	}
}
