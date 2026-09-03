package delegation

import (
	"strings"
	"sync"
	"testing"
)

func TestResetAgentCounter(t *testing.T) {
	generateAgentID()
	generateAgentID()
	ResetAgentCounter()
	if got := generateAgentID(); got != "child-1" {
		t.Errorf("generateAgentID() after reset = %q, want child-1", got)
	}
}

func TestResetForNewConversation(t *testing.T) {
	tests := []struct {
		name           string
		sessions       *SessionStore
		budgets        *AdvisorBudgetStore
		wantSessionCnt int
		wantBudgetCnt  int
	}{
		{
			name:           "resets conversation state",
			sessions:       NewSessionStore(),
			budgets:        NewAdvisorBudgetStore(),
			wantSessionCnt: 0,
			wantBudgetCnt:  0,
		},
		{
			name: "nil safe",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ResetAgentCounter()
			if tt.sessions != nil {
				if !tt.sessions.Save(&ChildSession{Spec: Spec{AgentID: "child-1"}}) {
					t.Fatal("SessionStore.Save() = false, want true")
				}
			}
			if tt.budgets != nil {
				tt.budgets.StateFor("child-1")
			}
			if got := generateAgentID(); got != "child-1" {
				t.Fatalf("generateAgentID() before reset = %q, want child-1", got)
			}

			ResetForNewConversation(tt.sessions, tt.budgets)

			if tt.sessions != nil && tt.sessions.Count() != tt.wantSessionCnt {
				t.Errorf("SessionStore.Count() = %d, want %d", tt.sessions.Count(), tt.wantSessionCnt)
			}
			if tt.budgets != nil && len(tt.budgets.states) != tt.wantBudgetCnt {
				t.Errorf("AdvisorBudgetStore states = %d, want %d", len(tt.budgets.states), tt.wantBudgetCnt)
			}
			if got := generateAgentID(); got != "child-1" {
				t.Errorf("generateAgentID() after reset = %q, want child-1", got)
			}
		})
	}
}

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
