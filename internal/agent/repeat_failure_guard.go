package agent

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
)

// maxRepeatedFailureResponses is the number of consecutive model responses
// that must report the same tool failure signature before the run stops.
const maxRepeatedFailureResponses = 3

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
	kind, message, _ := toolErrorKindMessage(err)
	return toolFailureSignature{tool: name, kind: kind, message: message}
}

// responseFailureLog collects the distinct failure signatures of the tool
// calls in one model response.
type responseFailureLog struct {
	dispatched bool
	signatures map[toolFailureSignature]struct{}
}

// observeToolOutcome records one tool outcome from the model response being
// executed. Calls that were never dispatched are ignored. A success marks the
// response as dispatched but does not clear failures recorded earlier in the
// same response.
func (p *turnProgressor) observeToolOutcome(name string, err error) {
	if errors.Is(err, errNotDispatched) {
		return
	}
	p.responseFailures.dispatched = true
	if err == nil {
		return
	}
	if p.responseFailures.signatures == nil {
		p.responseFailures.signatures = map[toolFailureSignature]struct{}{}
	}
	p.responseFailures.signatures[toolFailureSignatureOf(name, err)] = struct{}{}
}

// beginToolResponse starts failure collection for one model response.
func (p *turnProgressor) beginToolResponse() {
	p.responseFailures = responseFailureLog{}
}

// commitToolResponse advances the consecutive-response counts with the
// signatures reported by the completed response. A signature present in the
// response starts at one or advances its count; a signature absent from the
// response is dropped. A response with no dispatched call is neutral and
// leaves the counts unchanged. The caller must commit only after every tool
// call in the response has run.
func (p *turnProgressor) commitToolResponse() {
	if !p.responseFailures.dispatched || p.tripped != nil {
		return
	}
	counts := make(map[toolFailureSignature]int, len(p.responseFailures.signatures))
	for sig := range p.responseFailures.signatures {
		counts[sig] = p.failureCounts[sig] + 1
	}
	p.failureCounts = counts
	for _, sig := range sortedFailureSignatures(counts) {
		if counts[sig] < maxRepeatedFailureResponses {
			continue
		}
		tripped := sig
		p.tripped = &tripped
		return
	}
}

// sortedFailureSignatures orders signatures by tool, kind, then message so a
// response that trips several signatures stops on a deterministic one.
func sortedFailureSignatures(counts map[toolFailureSignature]int) []toolFailureSignature {
	sigs := make([]toolFailureSignature, 0, len(counts))
	for sig := range counts {
		sigs = append(sigs, sig)
	}
	slices.SortFunc(sigs, func(a, b toolFailureSignature) int {
		if c := cmp.Compare(a.tool, b.tool); c != 0 {
			return c
		}
		if c := cmp.Compare(a.kind, b.kind); c != 0 {
			return c
		}
		return cmp.Compare(a.message, b.message)
	})
	return sigs
}

// RepeatedToolFailureError reports that the repeat failure guard stopped a run
// because the same tool failure signature appeared in
// maxRepeatedFailureResponses consecutive model responses. Detect it with
// errors.As; ReportedError unwraps to it.
type RepeatedToolFailureError struct {
	sig toolFailureSignature
}

// Error returns the guard's stop message.
func (e *RepeatedToolFailureError) Error() string {
	return fmt.Sprintf("tool %q failed with the same error in %d consecutive model responses: %s", e.sig.tool, maxRepeatedFailureResponses, e.sig.message)
}

// stopOnRepeatedFailure ends the run once the failure guard has tripped. Every
// tool call in the response has already run by then.
func (p *turnProgressor) stopOnRepeatedFailure(state RunState) (turnOutcome, bool) {
	if p.tripped == nil {
		return turnOutcome{}, false
	}
	state.StopReason = StopReasonError
	return turnOutcome{
		State: state,
		Stop:  true,
		Error: &RepeatedToolFailureError{sig: *p.tripped},
	}, true
}
