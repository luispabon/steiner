package builtin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestGlobWalkExcludesRelativeToRoot(t *testing.T) {
	tests := []struct {
		name       string
		rootSubdir string
		files      []string
		want       []string
	}{
		{
			name:       "root under build dir",
			rootSubdir: "build/proj",
			files:      []string{"a.go", "sub/b.go", "vendor/x.go", "node_modules/y.go"},
			want:       []string{"a.go", "sub/b.go"},
		},
		{
			name:       "root under steiner worktrees",
			rootSubdir: ".steiner/worktrees/x",
			files:      []string{"a.go", "pkg/b.go", "dist/c.go"},
			want:       []string{"a.go", "pkg/b.go"},
		},
		{
			name:       "plain root still skips excluded subdirs",
			rootSubdir: "proj",
			files:      []string{"a.go", "vendor/x.go", "target/z.go"},
			want:       []string{"a.go"},
		},
	}
	for _, tt := range tests {
		for _, withPolicy := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/policy=%t", tt.name, withPolicy), func(t *testing.T) {
				base := t.TempDir()
				root := filepath.Join(base, filepath.FromSlash(tt.rootSubdir))
				for _, f := range tt.files {
					p := filepath.Join(root, filepath.FromSlash(f))
					if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
						t.Fatalf("mkdir: %v", err)
					}
					if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
						t.Fatalf("write: %v", err)
					}
				}
				var policy *tool.PathPolicy
				if withPolicy {
					pp := tool.NewPathPolicy(base, config.PathsConfig{})
					policy = &pp
				}
				got, err := globWalk(root, "**/*.go", tool.NewPathExcluder(nil, nil), policy)
				if err != nil {
					t.Fatalf("globWalk error: %v", err)
				}
				if strings.Join(got, ",") != strings.Join(tt.want, ",") {
					t.Errorf("got %v, want %v", got, tt.want)
				}
			})
		}
	}
}
