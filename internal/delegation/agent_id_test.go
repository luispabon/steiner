package delegation

import (
	"strings"
	"sync"
	"testing"
)

// TestGenerateAgentID_UniqueUnderRace verifies that concurrent calls produce
// unique IDs with no data races. Run with -race.
func TestGenerateAgentID_UniqueUnderRace(t *testing.T) {
	const n = 200
	results := make([]string, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			results[i] = generateAgentID()
		}()
	}
	wg.Wait()

	seen := make(map[string]struct{}, n)
	for _, id := range results {
		if !strings.HasPrefix(id, "child-") {
			t.Errorf("id %q missing child- prefix", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = struct{}{}
	}
}
