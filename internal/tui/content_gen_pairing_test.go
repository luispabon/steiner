package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestRetroactiveDirtyMarksArePairedWithGenBump enforces the invariant the
// settled-prefix cache in contentBuffer.String depends on: marking an existing
// segment dirty in place must also bump contentBuffer.gen.
//
// prefixCacheValid compares prefixCacheGen against gen, so a mutation site that
// sets renderDirty without bumping gen leaves the cached prefix serving the
// pre-mutation render of that segment forever. The symptom is a stale frame,
// not a failing test, which is why this is policed at the source level rather
// than behaviourally.
func TestRetroactiveDirtyMarksArePairedWithGenBump(t *testing.T) {
	t.Parallel()

	// Built by concatenation so this file does not match its own needles.
	dirtyPattern := regexp.MustCompile(`segments\[[^\]]+\]\.` + `renderDirty = true`)
	genBump := "gen" + "++"

	const window = 6

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	scanned, matched := 0, 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		scanned++
		lines := strings.Split(string(src), "\n")
		for i, line := range lines {
			if !dirtyPattern.MatchString(line) {
				continue
			}
			matched++
			lo := max(0, i-window)
			hi := min(len(lines), i+window+1)
			if !strings.Contains(strings.Join(lines[lo:hi], "\n"), genBump) {
				t.Errorf("%s:%d marks an existing segment dirty without a nearby %s:\n\t%s\n"+
					"the settled-prefix cache keys on gen, so this mutation will not "+
					"invalidate the cached prefix and the segment will render stale",
					name, i+1, genBump, strings.TrimSpace(line))
			}
		}
	}

	// Positive controls: if the scan stops finding files, or the needle stops
	// matching real mutation sites, the loop above passes while asserting nothing.
	if scanned == 0 {
		t.Fatal("scanned no production files; the source scan asserts nothing")
	}
	if matched == 0 {
		t.Fatalf("found no %q sites; the needle is stale and asserts nothing", dirtyPattern)
	}
}
