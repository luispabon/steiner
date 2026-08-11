package prompt

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var consumerFilePaths = []string{
	"../../skills/implement/SKILL.md",
	"../../skills/review/SKILL.md",
	"../../skills/simplify/SKILL.md",
	"../../skills/plan/SKILL.md",
	"../../skills/pull-request/SKILL.md",
}

var consumerGlobs = []string{
	"../oneshot/prompts/*.md",
}

type consumerParagraph struct {
	Path      string // repo-relative, e.g. "skills/implement/SKILL.md"
	StartLine int    // 1-indexed line number of the paragraph's first line
	Text      string // raw paragraph text, internal newlines preserved
}

type finding struct {
	Path      string
	StartLine int
	Detail    string // human-readable: what matched what, with both excerpts
}

// consumerPaths returns the repo-relative paths of every consumer file,
// sorted, resolved relative to the internal/prompt package directory.
func consumerPaths(t *testing.T) []string {
	t.Helper()

	var paths []string
	for _, p := range consumerFilePaths {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("consumer path missing: %s: %v", p, err)
		}
		paths = append(paths, p)
	}
	for _, g := range consumerGlobs {
		matches, err := filepath.Glob(g)
		if err != nil {
			t.Fatalf("glob %s: %v", g, err)
		}
		if len(matches) == 0 {
			t.Fatalf("glob %s matched no files", g)
		}
		paths = append(paths, matches...)
	}

	sort.Strings(paths)
	return paths
}

// loadConsumers reads consumerPaths and splits each into paragraphs.
func loadConsumers(t *testing.T) []consumerParagraph {
	t.Helper()

	var out []consumerParagraph
	for _, p := range consumerPaths(t) {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read consumer %s: %v", p, err)
		}
		out = append(out, splitParagraphs(repoRelativePath(p), string(data))...)
	}
	return out
}

// repoRelativePath converts a path relative to the internal/prompt package
// directory (as returned by consumerPaths, e.g. "../../skills/x/SKILL.md")
// into a repo-relative path (e.g. "skills/x/SKILL.md"). Two forms exist
// because os.ReadFile needs the package-relative form to actually open the
// file from a test binary's working directory, while consumerParagraph.Path
// is documented and matched in the repo-relative form
// (docs/canon-drift-checks.md), which is stable regardless of which
// package's tests produced the finding.
func repoRelativePath(pkgRelPath string) string {
	return filepath.Clean(filepath.Join("internal/prompt", pkgRelPath))
}

// splitParagraphs splits content into blank-line-delimited blocks, tagging
// each with its 1-indexed start line.
func splitParagraphs(path, content string) []consumerParagraph {
	lines := strings.Split(content, "\n")

	var out []consumerParagraph
	var block []string
	blockStart := 0

	flush := func() {
		if len(block) == 0 {
			return
		}
		out = append(out, consumerParagraph{
			Path:      path,
			StartLine: blockStart,
			Text:      strings.Join(block, "\n"),
		})
		block = nil
	}

	for i, line := range lines {
		lineNum := i + 1
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if len(block) == 0 {
			blockStart = lineNum
		}
		block = append(block, line)
	}
	flush()

	return out
}
