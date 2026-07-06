package delegation

import (
	"fmt"
	"sync/atomic"
)

// agentCounter is a process-local monotonic counter for agent ID generation.
var agentCounter atomic.Uint64

// idGen is the agent ID generator; tests may override for determinism.
var idGen = func() string {
	return fmt.Sprintf("child-%d", agentCounter.Add(1))
}

func generateAgentID() string {
	return idGen()
}
