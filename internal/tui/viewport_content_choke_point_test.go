package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestViewportSetContentChokePoint enforces that setViewportContent is the only
// call site for the viewport's content setters. Bypassing it lets m.viewportLines
// drift out of sync with the viewport's own content, which visibleViewportContent
// relies on to avoid re-deriving the full rendered conversation on every frame.
//
// SetContentLines is policed alongside SetContent: it writes the viewport's line
// slice just as directly, so it desyncs m.viewportLines the same way. Its one
// existing use (selection_test.go) only needs the viewport's line count to force
// a scrollbar and never renders content, so it is listed as a known exception.
func TestViewportSetContentChokePoint(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	// Built by concatenation so this file does not match its own needles.
	needles := []string{".viewport." + "SetContent(", ".viewport." + "SetContentLines("}
	const allowedFile = "model_layout.go"
	exempt := map[string]string{
		"viewport_content_choke_point_test.go": "defines the needles",
		"selection_test.go":                    "line count only, never renders viewport content",
	}

	var scanned, matchedAllowed int
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		if _, ok := exempt[entry.Name()]; ok {
			continue
		}

		path := filepath.Join(".", entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		scanned++

		for _, needle := range needles {
			if !strings.Contains(string(content), needle) {
				continue
			}
			if entry.Name() == allowedFile {
				matchedAllowed++
				continue
			}
			t.Errorf("%s calls m%s directly; use m.setViewportContent (defined in %s) instead so m.viewportLines stays in sync", path, needle, allowedFile)
		}
	}

	// Positive control: without this, a stale needle would make the scan above
	// pass vacuously forever.
	if scanned == 0 {
		t.Fatal("scanned 0 files; the choke-point scan is not running against package source")
	}
	if matchedAllowed == 0 {
		t.Fatalf("no needle matched in %s; the needles %q are stale and the scan asserts nothing", allowedFile, needles)
	}
}
