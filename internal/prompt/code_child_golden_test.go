package prompt

import (
	"os"
	"strings"
	"testing"
)

// TestCodeChildSectionGolden pins the rendered code sub-agent section so the
// documentor prompt refactor cannot silently change existing code-child bytes.
// The fixture captures strings.TrimSpace(renderTemplate(templateCodeChild, nil))
// as of origin/main and must not be regenerated to make the test pass.
func TestCodeChildSectionGolden(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/code_child_section.golden")
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	want := strings.TrimSuffix(string(raw), "\n")
	got := strings.TrimSpace(renderTemplate(templateCodeChild, nil))
	if got != want {
		t.Fatalf("code child section drifted from golden fixture\n--- got ---\n%s\n--- want ---\n%s\n--- end ---", got, want)
	}
}
