package diagnostics

import (
	"path/filepath"
	"testing"
)

// TestSharedFixturesDecode reads the same testdata/diagnostics/*.jsonl
// fixtures that scripts/diagnostics_test.mjs parses, so a schema change on
// either side that the other doesn't follow fails here instead of silently
// producing zero rows in the mjs script (issue #707's stage 5 warns this is
// worse than no script at all).
func TestSharedFixturesDecode(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "diagnostics")

	for _, kind := range []Kind{KindCache, KindProvider, KindTool} {
		t.Run(string(kind), func(t *testing.T) {
			path := filepath.Join(root, kind.fileName())
			records := readRecords(t, path)
			if len(records) == 0 {
				t.Fatalf("no records decoded from %s", path)
			}
			for i, rec := range records {
				if rec.Kind != kind {
					t.Errorf("record %d: kind = %q, want %q", i, rec.Kind, kind)
				}
				if rec.Payload == nil {
					t.Errorf("record %d: payload is nil", i)
				}
			}
		})
	}
}
