package licensescan

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderDeterministic(t *testing.T) {
	moduleA := Module{
		Path:     "example.com/a",
		Version:  "v0.1.0",
		Licenses: []string{"Apache-2.0", "MIT"},
		Files: []LicenseFile{
			{Path: "NOTICE", Text: "Notice body\n"},
			{Path: "LICENSE", Text: "A fence ``` inside\n"},
		},
	}
	moduleB := Module{
		Path:     "example.com/b",
		Version:  "v1.2.0",
		Licenses: []string{"MIT"},
		Files:    []LicenseFile{{Path: "LICENSE", Text: "MIT License\n"}},
	}
	report := &Report{
		GoVersion: "go1.26.11",
		GoFiles:   []LicenseFile{{Path: "LICENSE", Text: "Go license\n"}},
		Modules:   []Module{moduleB, moduleA},
	}

	first := Render(report)
	second := Render(report)
	if !bytes.Equal(first, second) {
		t.Fatal("Render is not stable across calls")
	}

	reordered := &Report{
		GoVersion: report.GoVersion,
		GoFiles:   report.GoFiles,
		Modules:   []Module{moduleA, moduleB},
	}
	if !bytes.Equal(Render(reordered), first) {
		t.Error("Render output depends on module input order")
	}

	out := string(first)
	if !strings.HasSuffix(out, "\n") || strings.HasSuffix(out, "\n\n") {
		t.Errorf("output must end with exactly one newline, got suffix %q", out[len(out)-10:])
	}

	summary := strings.Index(out, "## Summary")
	goSection := strings.Index(out, "## Go standard library and runtime (go1.26.11)")
	aIdx := strings.Index(out, "## example.com/a v0.1.0")
	bIdx := strings.Index(out, "## example.com/b v1.2.0")
	for name, idx := range map[string]int{"Summary": summary, "Go section": goSection, "module a": aIdx, "module b": bIdx} {
		if idx < 0 {
			t.Fatalf("output is missing the %s section", name)
		}
	}
	if summary >= goSection || goSection >= aIdx || aIdx >= bIdx {
		t.Errorf("sections are out of order: summary=%d go=%d a=%d b=%d", summary, goSection, aIdx, bIdx)
	}

	apacheRow := strings.Index(out, "| Apache-2.0 | 1 |")
	mitRow := strings.Index(out, "| MIT | 2 |")
	if apacheRow < 0 || mitRow < 0 {
		t.Error("summary table is missing the expected license counts")
	}
	if apacheRow > mitRow {
		t.Error("summary rows are not sorted by license name")
	}

	if !strings.Contains(out, "````text\nA fence ``` inside\n````\n") {
		t.Error("license text with backticks is not fenced by a longer fence")
	}
	if strings.Index(out, "### LICENSE\n") > strings.Index(out, "### NOTICE\n") {
		t.Error("license files are not sorted by path")
	}
}
