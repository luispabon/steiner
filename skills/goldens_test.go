package skills_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/luispabon/steiner/internal/skill"
	"github.com/luispabon/steiner/skills"
)

func TestBundledSkillsMatchGoldens(t *testing.T) {
	tests := []struct {
		name       string
		goldenPath string
	}{
		{"implement", "testdata/goldens/implement.golden.md"},
		{"review", "testdata/goldens/review.golden.md"},
		{"simplify", "testdata/goldens/simplify.golden.md"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loader := skill.Loader{BundledFS: skills.FS}
			loaded, err := loader.Load(context.Background(), tt.name)
			if err != nil {
				t.Fatalf("load skill %q: %v", tt.name, err)
			}

			golden, err := os.ReadFile(tt.goldenPath)
			if err != nil {
				t.Fatalf("read golden %q: %v", tt.goldenPath, err)
			}

			if loaded.Content != string(golden) {
				t.Errorf("skill %q content does not match golden %q:\n%s", tt.name, tt.goldenPath, diffExcerpt(loaded.Content, string(golden)))
			}
			if loaded.ByteSize != len(golden) {
				t.Errorf("skill %q byte size = %d, want %d", tt.name, loaded.ByteSize, len(golden))
			}
		})
	}
}

// diffExcerpt reports the first differing byte offset between got and want,
// along with a short excerpt from each side around that offset.
func diffExcerpt(got, want string) string {
	minLen := len(got)
	if len(want) < minLen {
		minLen = len(want)
	}

	offset := -1
	for i := 0; i < minLen; i++ {
		if got[i] != want[i] {
			offset = i
			break
		}
	}
	if offset == -1 {
		if len(got) != len(want) {
			offset = minLen
		} else {
			return "no byte difference found"
		}
	}

	const window = 40
	return fmt.Sprintf(
		"first differing byte offset: %d\ngot  (%d bytes): %q\nwant (%d bytes): %q",
		offset,
		len(got), excerpt(got, offset, window),
		len(want), excerpt(want, offset, window),
	)
}

func excerpt(s string, offset, window int) string {
	start := offset - window
	if start < 0 {
		start = 0
	}
	end := offset + window
	if end > len(s) {
		end = len(s)
	}
	return s[start:end]
}
