package provider

import (
	"reflect"
	"testing"
)

func TestResponsesLedgerIdentityFreeDeltaUsesAddedMessage(t *testing.T) {
	state := responsesStreamState{}
	index := 1
	if _, err := state.resolve("message", "msg", &index, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := state.resolve("message", "", nil, "", false); err != nil {
		t.Fatal(err)
	}
	state.ledger[0].appendPart(0, "first")
	if len(state.ledger) != 1 || state.ledger[0].itemID != "msg" {
		t.Fatalf("ledger = %#v", state.ledger)
	}
}

func TestResponsesLedgerTextDoneReplacesIndexedPart(t *testing.T) {
	state := responsesStreamState{}
	idx0 := 0
	entry, err := state.resolve("message", "m", &idx0, "", false)
	if err != nil {
		t.Fatal(err)
	}
	entry.appendPart(0, "old")
	entry.appendPart(1, "keep")
	if _, err := handleResponsesTextDone(&state, responsesStreamEvent{ItemID: "m", OutputIndex: &idx0, ContentIndex: &idx0, Text: "new"}); err != nil {
		t.Fatal(err)
	}
	blocks, _, text := state.projected()
	if text != "newkeep" || !reflect.DeepEqual(blocks, []CodexMessageBlock{{Kind: "message", Text: "newkeep"}}) {
		t.Fatalf("projection = %#v, %q", blocks, text)
	}
}

func TestResponsesLedgerAliasConflictDoesNotMutate(t *testing.T) {
	state := responsesStreamState{ledger: []responsesLedgerEntry{
		{kind: "message", itemID: "a"},
		{kind: "message", outputIndex: intPointer(1)},
	}}
	index := 1
	before := append([]responsesLedgerEntry(nil), state.ledger...)
	if _, err := state.resolve("message", "a", &index, "", false); err == nil {
		t.Fatal("expected split alias conflict")
	}
	if !reflect.DeepEqual(state.ledger, before) {
		t.Fatalf("ledger changed: %#v, want %#v", state.ledger, before)
	}
}

func TestResponsesLedgerProjectionSortsPartsAndOmitsUnfinishedCalls(t *testing.T) {
	state := responsesStreamState{ledger: []responsesLedgerEntry{
		{kind: "message", outputIndex: intPointer(0), parts: map[int]string{1: "b", 0: "a"}, partOrder: []int{1, 0}},
		{kind: "function_call", outputIndex: intPointer(1), call: &ToolCall{ID: "unfinished"}},
	}}
	blocks, calls, content := state.projected()
	if content != "ab" || !reflect.DeepEqual(blocks, []CodexMessageBlock{{Kind: "message", Text: "ab"}}) || len(calls) != 0 {
		t.Fatalf("projection = %#v %#v %q", blocks, calls, content)
	}
}

func intPointer(n int) *int { return &n }
