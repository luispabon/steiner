package delegation

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
)

// agentCounter is a process-local monotonic counter for agent ID generation.
var agentCounter atomic.Uint64

// idGen is the agent ID generator; tests may override for determinism.
var idGen = func() string {
	return fmt.Sprintf("child-%d", agentCounter.Add(1))
}

// processHashOnce ensures processHash is generated exactly once per process.
var processHashOnce sync.Once

// processHashValue stores the generated process hash.
var processHashValue string

// initProcessHash generates a random 8-byte hex string for this process.
func initProcessHash() {
	processHashOnce.Do(func() {
		b := make([]byte, 4)
		if _, err := rand.Read(b); err != nil {
			panic(fmt.Sprintf("failed to generate process hash: %v", err))
		}
		processHashValue = hex.EncodeToString(b)
	})
}

// getProcessHash returns the process-level identity hash, generating it on first call.
func getProcessHash() string {
	initProcessHash()
	return processHashValue
}

// resetProcessHashForTesting resets the process hash, allowing tests to simulate
// a new process. This is unexported and only for testing.
func resetProcessHashForTesting() {
	processHashOnce = sync.Once{}
	processHashValue = ""
}

func generateAgentID() string {
	return idGen()
}
