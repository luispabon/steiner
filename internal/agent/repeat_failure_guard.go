package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// maxIdenticalToolFailures is the number of consecutive tool failures with the
// same tool, kind, and message that stops the run.
const maxIdenticalToolFailures = 3

// toolFailureSignature identifies a tool failure for repeat detection.
type toolFailureSignature struct {
	tool    string
	kind    string
	message string
}

// toolFailureSignatureOf returns the signature of a failed tool call. It
// mirrors formatToolError: an executor error contributes its kind and message,
// and any other error is reported as tool_error.
func toolFailureSignatureOf(name string, err error) toolFailureSignature {
	var tee *tool.ToolExecutionError
	if errors.As(err, &tee) {
		return toolFailureSignature{tool: name, kind: tee.Kind, message: tee.Message}
	}
	return toolFailureSignature{tool: name, kind: "tool_error", message: err.Error()}
}

// observeToolOutcome records one tool outcome for repeat detection. Calls that
// were never dispatched are ignored. A success clears the streak. Once a
// signature reaches maxIdenticalToolFailures it stays latched for the run.
func (p *turnProgressor) observeToolOutcome(name string, err error) {
	if errors.Is(err, errNotDispatched) {
		return
	}
	if err == nil {
		p.identicalFailures = 0
		return
	}
	sig := toolFailureSignatureOf(name, err)
	if sig == p.lastFailure {
		p.identicalFailures++
	} else {
		p.lastFailure = sig
		p.identicalFailures = 1
	}
	if p.identicalFailures >= maxIdenticalToolFailures && p.tripped == nil {
		tripped := p.lastFailure
		p.tripped = &tripped
	}
}

// stopOnRepeatedFailure ends the turn once the failure guard has tripped. The
// calls that have not run are recorded as not dispatched so every tool call
// keeps its tool result.
func (p *turnProgressor) stopOnRepeatedFailure(ctx context.Context, state RunState, turn int, remaining []provider.ToolCall) (turnOutcome, bool) {
	if p.tripped == nil {
		return turnOutcome{}, false
	}
	state = p.appendNotDispatched(ctx, state, turn, remaining)
	return p.repeatedFailureStop(state)
}

// repeatedFailureStop returns the error stop for a tripped failure guard. It
// reports false when the guard has not tripped.
func (p *turnProgressor) repeatedFailureStop(state RunState) (turnOutcome, bool) {
	if p.tripped == nil {
		return turnOutcome{}, false
	}
	state.StopReason = StopReasonError
	return turnOutcome{
		State: state,
		Stop:  true,
		Error: fmt.Errorf("tool %q failed %d times in a row with the same error: %s", p.tripped.tool, maxIdenticalToolFailures, p.tripped.message),
	}, true
}
