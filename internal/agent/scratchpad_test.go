package agent

import (
	"strings"
	"testing"
)

func TestParseScratchpadCarriesForwardMissingFields(t *testing.T) {
	previous := Scratchpad{
		Goal: "goal",
		Plan: "plan",
		Step: "step",
		Next: "next",
		Open: "open",
	}

	got, ok := ParseScratchpad("<scratchpad>\ngoal: updated\nnext: finish tests\n</scratchpad>", previous)
	if !ok {
		t.Fatal("ParseScratchpad() = false, want true")
	}
	if got.Goal != "updated" || got.Next != "finish tests" {
		t.Fatalf("parsed fields mismatch: %+v", got)
	}
	if got.Plan != "plan" || got.Step != "step" || got.Open != "open" {
		t.Fatalf("missing fields should carry forward: %+v", got)
	}
}

func TestParseScratchpadIgnoresThinkingBlocks(t *testing.T) {
	text := "<thinking>\n<scratchpad>\ngoal: hidden\n</scratchpad>\n</thinking>\n<scratchpad>\ngoal: visible\n</scratchpad>"
	got, ok := ParseScratchpad(text, Scratchpad{})
	if !ok {
		t.Fatal("ParseScratchpad() = false, want true")
	}
	if got.Goal != "visible" {
		t.Fatalf("Goal = %q, want visible", got.Goal)
	}
}

func TestParseScratchpadMissingBlockCarriesForward(t *testing.T) {
	previous := Scratchpad{Goal: "keep me"}
	got, ok := ParseScratchpad("plain assistant reply", previous)
	if ok {
		t.Fatal("ParseScratchpad() = true, want false")
	}
	if got.Goal != "keep me" {
		t.Fatalf("Goal = %q, want carry-forward", got.Goal)
	}
}

func TestStripScratchpadRemovesTaggedBlock(t *testing.T) {
	text := "done\n<scratchpad>\ngoal: x\n</scratchpad>"
	got := StripScratchpad(text)
	if strings.Contains(got, "<scratchpad>") {
		t.Fatalf("StripScratchpad() = %q, want scratchpad removed", got)
	}
	if got != "done" {
		t.Fatalf("StripScratchpad() = %q, want done", got)
	}
}
