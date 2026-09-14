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

// processHashMu guards processHashValue generation and reads.
var processHashMu sync.Mutex

// processHashValue stores the generated process hash, once successfully generated.
var processHashValue string

// randRead is the entropy source used to generate the process hash; tests
// may override it to simulate entropy-source failures.
var randRead = rand.Read

// getProcessHash returns the process-level identity hash, generating it on
// first successful call. A transient entropy-source failure is not cached,
// so a later call may succeed.
func getProcessHash() (string, error) {
	processHashMu.Lock()
	defer processHashMu.Unlock()

	if processHashValue != "" {
		return processHashValue, nil
	}

	b := make([]byte, 4)
	if _, err := randRead(b); err != nil {
		return "", fmt.Errorf("generate process hash: %w", err)
	}
	processHashValue = hex.EncodeToString(b)
	return processHashValue, nil
}

// resetProcessHashForTesting resets the process hash, allowing tests to simulate
// a new process. This is unexported and only for testing.
func resetProcessHashForTesting() {
	processHashMu.Lock()
	defer processHashMu.Unlock()
	processHashValue = ""
}

// ResetAgentCounter resets agent IDs for a new conversation boundary such as
// clear.
func ResetAgentCounter() {
	agentCounter.Store(0)
}

func generateAgentID() string {
	return idGen()
}
