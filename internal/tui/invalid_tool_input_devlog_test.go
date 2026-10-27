package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestInvalidToolInputRejectionStillReachesDevLog(t *testing.T) {
	const message = "tool execution failed: sub_agent: invalid_tool_input: tool input could not be parsed by the Claude CLI"
	admission := &output.DelegationAdmission{Status: "rejected", ModelGuidance: true}
	event := output.NewToolCallFinishedEventWithAdmission(1, "sub_agent", "call", "", errors.New(message), output.ToolPreview{}, admission)
	class, ok := classifyDevLog(event)
	if !ok {
		t.Fatal("classifyDevLog ok = false, want the rejection recorded in the dev log")
	}
	if !strings.Contains(class.message, "invalid_tool_input") {
		t.Fatalf("dev log message = %q, want it to carry the error", class.message)
	}
}
