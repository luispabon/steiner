package main

import (
	"slices"
	"testing"
)

func TestRunQuitSequenceOrder(t *testing.T) {
	var got []string
	step := func(name string) func() { return func() { got = append(got, name) } }
	runQuitSequence(quitSequence{
		cancelBackground:   step("cancelBackground"),
		shutdownDelegation: step("shutdown"),
		awaitRuns:          step("await"),
		closeSession:       step("close"),
		prune:              step("prune"),
		farewell:           step("farewell"),
		closeRuntime:       step("closeRuntime"),
	})
	want := []string{"cancelBackground", "shutdown", "await", "close", "prune", "farewell", "closeRuntime"}
	if !slices.Equal(got, want) {
		t.Fatalf("quit order = %v, want %v", got, want)
	}
}
