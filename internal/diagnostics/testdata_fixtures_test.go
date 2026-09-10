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
//
// The coldturns/ subdirectory is a second fixture set rather than more lines
// in the flat one: coldturns mode needs cache and tool records that line up
// in time, while every other mode's assertions count the flat fixtures' rows.
// It carries no provider stream, since the join never reads one.
func TestSharedFixturesDecode(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "diagnostics")

	for _, set := range []struct {
		name  string
		dir   string
		kinds []Kind
	}{
		{name: "flat", dir: root, kinds: []Kind{KindCache, KindProvider, KindTool}},
		{name: "coldturns", dir: filepath.Join(root, "coldturns"), kinds: []Kind{KindCache, KindTool}},
	} {
		t.Run(set.name, func(t *testing.T) {
			for _, kind := range set.kinds {
				t.Run(string(kind), func(t *testing.T) {
					path := filepath.Join(set.dir, kind.fileName())
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
		})
	}
}
