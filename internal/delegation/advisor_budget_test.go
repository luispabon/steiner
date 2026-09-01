package delegation

import (
	"sync"
	"testing"
)

func TestAdvisorBudgetStoreStateForSameID(t *testing.T) {
	store := NewAdvisorBudgetStore()
	state1 := store.StateFor("child-1")
	state2 := store.StateFor("child-1")
	if state1 != state2 {
		t.Fatal("StateFor with same agent ID returned different states (follow_up invariant violated)")
	}
}

func TestAdvisorBudgetStoreStateForDifferentIDs(t *testing.T) {
	store := NewAdvisorBudgetStore()
	state1 := store.StateFor("child-1")
	state2 := store.StateFor("child-2")
	if state1 == state2 {
		t.Fatal("StateFor with different agent IDs returned the same state (child isolation violated)")
	}
}

func TestAdvisorBudgetStoreIsolatedFromParent(t *testing.T) {
	store := NewAdvisorBudgetStore()
	parentState := store.StateFor("parent")
	childState := store.StateFor("child-1")
	if parentState == childState {
		t.Fatal("store.StateFor returned same state for parent and child (parent isolation violated)")
	}
}

func TestAdvisorBudgetStoreConcurrentAccess(t *testing.T) {
	store := NewAdvisorBudgetStore()
	const numGoroutines = 10
	var wg sync.WaitGroup
	results := make([]*struct {
		id    string
		state interface{}
	}, numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "child-1"
			state := store.StateFor(id)
			results[i] = &struct {
				id    string
				state interface{}
			}{id: id, state: state}
		}(i)
	}
	wg.Wait()

	firstState := results[0].state
	for i := 1; i < numGoroutines; i++ {
		if results[i].state != firstState {
			t.Fatal("concurrent StateFor calls returned different states for the same agent ID")
		}
	}
}

func TestAdvisorBudgetStoreReset(t *testing.T) {
	store := NewAdvisorBudgetStore()
	state1 := store.StateFor("child-1")
	store.Reset()
	state2 := store.StateFor("child-1")
	if state1 == state2 {
		t.Fatal("Reset did not produce a new state pointer for the same agent ID")
	}
}
