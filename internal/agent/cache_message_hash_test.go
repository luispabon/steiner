package agent

import (
	"testing"

	"github.com/luispabon/steiner/internal/provider"
)

func TestPerMessageHashes_EmptyMessagePreservesEmptyHash(t *testing.T) {
	if got := perMessageHashes([]provider.Message{{}})[0]; got != "" {
		t.Fatalf("perMessageHashes([]provider.Message{{}})[0] = %q, want empty", got)
	}
}
