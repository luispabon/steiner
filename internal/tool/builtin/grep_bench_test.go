package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// BenchmarkGrepTool runs the grep handler over a generated tree of 300 files
// with 300 matching lines each, so every line is a match. Sub-benchmarks cover
// content and files_with_matches output with the default page size.
func BenchmarkGrepTool(b *testing.B) {
	tmpDir := b.TempDir()
	var content strings.Builder
	for i := range 300 {
		content.Reset()
		for line := 1; line <= 300; line++ {
			fmt.Fprintf(&content, "file %d line %d needle text for grep retention benchmarks\n", i, line)
		}
		name := filepath.Join(tmpDir, fmt.Sprintf("f%03d.txt", i))
		if err := os.WriteFile(name, []byte(content.String()), 0o644); err != nil {
			b.Fatalf("write %s: %v", name, err)
		}
	}

	policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
	toolDef := NewGrepTool(Env{WorkDir: tmpDir, PathPolicy: &policy})
	ctx := context.Background()

	for _, mode := range []string{"content", "files_with_matches"} {
		b.Run(mode, func(b *testing.B) {
			input := map[string]any{"pattern": "needle", "output_mode": mode}
			b.ReportAllocs()
			for range b.N {
				if _, err := toolDef.Handler(ctx, input); err != nil {
					b.Fatalf("grep handler: %v", err)
				}
			}
		})
	}
}
