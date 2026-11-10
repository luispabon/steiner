package builtin

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// writeGrepRetentionFiles writes files into a new temp directory and returns
// the directory path.
func writeGrepRetentionFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// generatedGrepFiles returns twelve files whose match counts vary by file and
// line. The fifth and tenth files have no match.
func generatedGrepFiles() map[string]string {
	files := make(map[string]string, 12)
	for i := range 12 {
		var b strings.Builder
		for line := range 10 + i {
			if i%5 != 4 && (line+i)%3 == 0 {
				fmt.Fprintf(&b, "needle %d-%d\n", i, line)
			} else {
				fmt.Fprintf(&b, "filler %d-%d\n", i, line)
			}
		}
		files[fmt.Sprintf("f%02d.txt", i)] = b.String()
	}
	return files
}

// checkGrepRetention asserts that every matching file reports its match count.
// Files named in retained must keep their lines and matches; every other file
// must keep neither.
func checkGrepRetention(t *testing.T, files []grepFileResult, counts map[string]int, retained []string) {
	t.Helper()
	if len(files) != len(counts) {
		t.Fatalf("got %d matching files, want %d", len(files), len(counts))
	}
	for _, file := range files {
		want, ok := counts[file.file]
		if !ok {
			t.Fatalf("unexpected matching file %q", file.file)
		}
		if file.matchCount != want {
			t.Errorf("%s matchCount = %d, want %d", file.file, file.matchCount, want)
		}
		if slices.Contains(retained, file.file) {
			if file.lines == nil || len(file.matches) != want {
				t.Errorf("%s inside page: lines nil = %t, matches = %d, want %d", file.file, file.lines == nil, len(file.matches), want)
			}
			continue
		}
		if file.lines != nil || file.matches != nil {
			t.Errorf("%s outside page keeps %d lines and %d matches, want none", file.file, len(file.lines), len(file.matches))
		}
	}
}

func TestGrepSearchRetainsOnlyPageRows(t *testing.T) {
	// Content rows in walk order: a.txt 0-2, b.txt 3-7, d.txt 8-9. c.txt has
	// no match and is not a result.
	root := writeGrepRetentionFiles(t, map[string]string{
		"a.txt": "needle a1\nneedle a2\nneedle a3\n",
		"b.txt": "needle b1\nneedle b2\nneedle b3\nneedle b4\nneedle b5\n",
		"c.txt": "nothing here\n",
		"d.txt": "needle d1\nneedle d2\n",
	})
	counts := map[string]int{"a.txt": 3, "b.txt": 5, "d.txt": 2}
	policy := tool.NewPathPolicy(root, config.PathsConfig{})

	tests := []struct {
		name      string
		mode      string
		offset    int
		headLimit int
		retained  []string
	}{
		{"first rows stay in first file", "content", 0, 2, []string{"a.txt"}},
		{"whole first file", "content", 0, 3, []string{"a.txt"}},
		{"window crosses file boundary", "content", 2, 2, []string{"a.txt", "b.txt"}},
		{"window inside second file", "content", 4, 2, []string{"b.txt"}},
		{"window spans into last file", "content", 7, 3, []string{"b.txt", "d.txt"}},
		{"only last file", "content", 8, 10, []string{"d.txt"}},
		{"offset past the end", "content", 10, 5, nil},
		{"zero head limit", "content", 0, 0, nil},
		{"negative offset clamps to zero", "content", -2, 1, []string{"a.txt"}},
		{"head limit smaller than file", "content", 3, 1, []string{"b.txt"}},
		{"files_with_matches keeps no lines", "files_with_matches", 2, 2, nil},
		{"count keeps no lines", "count", 0, 10, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, _, err := grepSearch(context.Background(), grepSearchParams{
				root:        root,
				displayPath: root,
				pattern:     "needle",
				policy:      &policy,
				window:      &grepPageWindow{mode: tt.mode, offset: tt.offset, headLimit: tt.headLimit},
			})
			if err != nil {
				t.Fatalf("grepSearch: %v", err)
			}
			checkGrepRetention(t, files, counts, tt.retained)
		})
	}

	t.Run("nil window keeps every file", func(t *testing.T) {
		files, _, err := grepSearch(context.Background(), grepSearchParams{
			root:        root,
			displayPath: root,
			pattern:     "needle",
			policy:      &policy,
		})
		if err != nil {
			t.Fatalf("grepSearch: %v", err)
		}
		checkGrepRetention(t, files, counts, []string{"a.txt", "b.txt", "d.txt"})
	})
}

func TestGrepSearchSingleFileRetainsOnlyPageRows(t *testing.T) {
	root := writeGrepRetentionFiles(t, map[string]string{
		"single.txt": "needle 1\nneedle 2\nneedle 3\nneedle 4\n",
	})
	policy := tool.NewPathPolicy(root, config.PathsConfig{})

	tests := []struct {
		name      string
		mode      string
		offset    int
		headLimit int
		retained  bool
	}{
		{"first row", "content", 0, 1, true},
		{"last row", "content", 3, 1, true},
		{"past the end", "content", 4, 1, false},
		{"files_with_matches", "files_with_matches", 0, 1, false},
		{"count", "count", 0, 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, _, err := grepSearch(context.Background(), grepSearchParams{
				root:        filepath.Join(root, "single.txt"),
				displayPath: "single.txt",
				pattern:     "needle",
				policy:      &policy,
				window:      &grepPageWindow{mode: tt.mode, offset: tt.offset, headLimit: tt.headLimit},
			})
			if err != nil {
				t.Fatalf("grepSearch: %v", err)
			}
			var retained []string
			if tt.retained {
				retained = []string{"single.txt"}
			}
			checkGrepRetention(t, files, map[string]int{"single.txt": 4}, retained)
		})
	}
}

// TestInGrepPageMatchesPaginateGrepRows ties the content row rule to
// paginateGrepRows: a row is in the page exactly when pagination returns it.
func TestInGrepPageMatchesPaginateGrepRows(t *testing.T) {
	rows := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	for offset := -2; offset <= 12; offset++ {
		for headLimit := -1; headLimit <= 11; headLimit++ {
			page := paginateGrepRows(rows, offset, headLimit)
			for row := range rows {
				if got, want := inGrepPage(row, 1, offset, headLimit), slices.Contains(page, row); got != want {
					t.Errorf("inGrepPage(row %d, offset %d, headLimit %d) = %t, want %t", row, offset, headLimit, got, want)
				}
			}
		}
	}
}

func TestInGrepPageRanges(t *testing.T) {
	tests := []struct {
		name      string
		start     int
		count     int
		offset    int
		headLimit int
		want      bool
	}{
		{"file starts inside page", 0, 3, 0, 3, true},
		{"file starts at page end", 3, 5, 0, 3, false},
		{"file straddles page start", 2, 2, 0, 3, true},
		{"file ends at offset", 0, 3, 3, 5, false},
		{"file straddles offset", 0, 3, 2, 5, true},
		{"negative offset counts as zero", 2, 2, -4, 3, true},
		{"zero count", 2, 0, 0, 3, false},
		{"zero head limit", 0, 3, 0, 0, false},
		{"huge offset does not overflow", 0, 3, math.MaxInt, 5, false},
		{"huge head limit does not overflow", 4, 2, 1, math.MaxInt, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inGrepPage(tt.start, tt.count, tt.offset, tt.headLimit); got != tt.want {
				t.Errorf("inGrepPage(%d, %d, %d, %d) = %t, want %t", tt.start, tt.count, tt.offset, tt.headLimit, got, tt.want)
			}
		})
	}
}

// TestBuildGrepResultMatchesFullRetention checks that a windowed search renders
// the same GrepResult as a search that keeps every file in full.
func TestBuildGrepResultMatchesFullRetention(t *testing.T) {
	root := writeGrepRetentionFiles(t, generatedGrepFiles())
	policy := tool.NewPathPolicy(root, config.PathsConfig{})
	ctx := context.Background()
	full, fullStats, err := grepSearch(ctx, grepSearchParams{
		root:        root,
		displayPath: root,
		pattern:     "needle",
		policy:      &policy,
	})
	if err != nil {
		t.Fatalf("full grepSearch: %v", err)
	}

	for _, mode := range []string{"content", "files_with_matches", "count"} {
		for _, offset := range []int{0, 1, 3, 4, 7, 10, 25, 1000} {
			for _, headLimit := range []int{0, 1, 2, 3, 5, 7, 250} {
				for _, lineContext := range []int{0, 1} {
					name := fmt.Sprintf("%s/offset=%d/head_limit=%d/context=%d", mode, offset, headLimit, lineContext)
					t.Run(name, func(t *testing.T) {
						windowed, windowedStats, err := grepSearch(ctx, grepSearchParams{
							root:        root,
							displayPath: root,
							pattern:     "needle",
							policy:      &policy,
							window:      &grepPageWindow{mode: mode, offset: offset, headLimit: headLimit},
						})
						if err != nil {
							t.Fatalf("windowed grepSearch: %v", err)
						}
						got := buildGrepResult(windowed, windowedStats, mode, true, lineContext, lineContext, offset, headLimit)
						want := buildGrepResult(full, fullStats, mode, true, lineContext, lineContext, offset, headLimit)
						if !reflect.DeepEqual(got, want) {
							t.Errorf("windowed result = %+v, want %+v", got, want)
						}
					})
				}
			}
		}
	}
}

// TestGrepToolWindowMatchesFullResult checks the handler wiring: its result for
// each page equals buildGrepResult over fully retained files.
func TestGrepToolWindowMatchesFullResult(t *testing.T) {
	root := writeGrepRetentionFiles(t, generatedGrepFiles())
	policy := tool.NewPathPolicy(root, config.PathsConfig{})
	toolDef := NewGrepTool(Env{WorkDir: root, PathPolicy: &policy})
	ctx := context.Background()
	full, fullStats, err := grepSearch(ctx, grepSearchParams{
		root:        root,
		displayPath: root,
		pattern:     "needle",
		policy:      &policy,
	})
	if err != nil {
		t.Fatalf("full grepSearch: %v", err)
	}

	tests := []struct {
		name        string
		mode        string
		offset      int
		headLimit   int
		lineContext int
	}{
		{"content first page", "content", 0, 7, 0},
		{"content mid file with context", "content", 4, 3, 1},
		{"content past the end", "content", 1000, 5, 0},
		{"files_with_matches", "files_with_matches", 2, 3, 0},
		{"count", "count", 4, 3, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runGrepForTest(t, toolDef, map[string]any{
				"pattern":     "needle",
				"output_mode": tt.mode,
				"offset":      tt.offset,
				"head_limit":  tt.headLimit,
				"context":     tt.lineContext,
			})
			want := buildGrepResult(full, fullStats, tt.mode, true, tt.lineContext, tt.lineContext, tt.offset, tt.headLimit)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("handler result = %+v, want %+v", got, want)
			}
		})
	}
}
