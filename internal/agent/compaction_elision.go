package agent

import (
	"context"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

const (
	// retainedResultBudgetTokens is the soft token budget for result bodies kept
	// verbatim in the retained tail of a compaction. Once it is spent, older
	// consumed results that are worth eliding are replaced by their markers.
	retainedResultBudgetTokens = 20_000
	// retainedResultMinBytes and retainedResultMarkerFactor bound the bodies
	// worth replacing: a marker must be small against the text it hides.
	retainedResultMinBytes     = 1024
	retainedResultMarkerFactor = 4
)

// compactionElisionSettings fixes the elision decisions of one compaction
// operation to the request that entered it. compactionFallbackRequest copies the
// whole RunRequest, so a fallback copy keeps the main decisions and elision
// never follows the fallback provider or estimates on the fallback budget.
type compactionElisionSettings struct {
	// skip reports that the consuming provider owns its transcript, so the
	// retained tail keeps full bodies.
	skip bool
	// main is the request that entered the compaction operation. Every estimate
	// uses its model, budget and prompt suffix.
	main RunRequest
}

// withCompactionElision stamps req with the elision decisions its compaction
// operation uses. Call it once at compaction entry, before any fallback copy.
func withCompactionElision(req RunRequest) RunRequest {
	skip := req.Provider != nil && provider.IsStatefulTranscript(req.Provider)
	settings := &compactionElisionSettings{skip: skip, main: req}
	req.compactionElision = settings
	return req
}

// elideRetainedToolOutputs replaces older consumed result bodies in retained
// with recall markers and reports whether it replaced any. It works on a clone,
// so retained is never changed. A nil archive, an unstamped request, or a
// stateful consuming provider leaves retained as it is.
//
// Targets are walked newest first. Every body kept verbatim counts against
// budget, unconsumed and below-threshold ones included, until the budget is
// spent. From then on the count cannot change another decision, so older
// bodies are no longer estimated: each consumed body with at least
// retainedResultMinBytes and at least retainedResultMarkerFactor markers' worth
// of text is replaced, and elision is monotonic from newest to oldest.
func elideRetainedToolOutputs(ctx context.Context, req RunRequest, state RunState, retained []Message, budget int, fit compactionFitRunner) ([]Message, bool) {
	settings := req.compactionElision
	if req.RecallArchive == nil || settings == nil || settings.skip {
		return retained, false
	}
	targets := retainedResultTargets(retained)
	if len(targets) == 0 {
		return retained, false
	}
	baseline, err := fit(ctx, settings.main, state.WithConversation(nil))
	if err != nil {
		emitEvent(req.Events, resultElisionWarning(err))
		return retained, false
	}
	out := cloneMessages(retained)
	keptTokens := 0
	elided := false
	for i := len(targets) - 1; i >= 0; i-- {
		target := targets[i]
		if keptTokens >= budget {
			// The budget is spent, so no later decision depends on this body's
			// size. Skip its estimate: a failure here must not stop elision of
			// the remaining older consumed bodies.
			if target.consumed && elideRetainedResult(ctx, req, out, target) {
				elided = true
			}
			continue
		}
		tokens, err := estimateRetainedResultTokens(ctx, settings.main, state, baseline, target, fit)
		if err != nil {
			emitEvent(req.Events, resultElisionWarning(err))
			return out, elided
		}
		// Under budget the body is kept verbatim and its size can still decide
		// where the budget is spent.
		keptTokens += tokens
	}
	return out, elided
}

// retainedResultTarget is one result body of the retained tail and the message
// that carries it. span is nil for a whole tool message and the envelope range
// inside the message content for a sub-agent result.
type retainedResultTarget struct {
	msgIndex int
	span     *prompt.ResultEnvelopeSpan
	entry    RecallEntry
	consumed bool
}

// retainedResultTargets returns every result body of retained in message order,
// with the consumption rule shared with the protected-output check: a result is
// consumed once a later complete assistant message exists.
func retainedResultTargets(retained []Message) []retainedResultTarget {
	consumed := make([]bool, len(retained))
	read := false
	for i := len(retained) - 1; i >= 0; i-- {
		consumed[i] = read
		if retained[i].Role == MessageRoleAssistant && !retained[i].Incomplete {
			read = true
		}
	}

	var targets []retainedResultTarget
	for i, msg := range retained {
		switch msg.Role {
		case MessageRoleTool:
			targets = append(targets, retainedResultTarget{
				msgIndex: i,
				entry:    RecallEntry{Kind: "tool", Tool: msg.Name, CallID: msg.ToolCallID, Body: msg.Content},
				consumed: consumed[i],
			})
		case MessageRoleUser:
			spans := prompt.ResultEnvelopeSpans(msg.Content)
			for j := range spans {
				parsed, ok := ParseSubAgentResultEnvelope(msg.Content[spans[j].Start:spans[j].End])
				if !ok {
					// ResultEnvelopeSpans returns only envelopes that parsed, so
					// this is unreachable unless the parsers disagree.
					continue
				}
				span := spans[j]
				targets = append(targets, retainedResultTarget{
					msgIndex: i,
					span:     &span,
					entry:    RecallEntry{Kind: "agent", Tool: parsed.AgentType, CallID: parsed.CallID, Body: parsed.Inner},
					consumed: consumed[i],
				})
			}
		}
	}
	return targets
}

// estimateRetainedResultTokens measures one result body alone against the empty
// baseline, so its cost does not depend on its neighbours. The carrier is a user
// message because replay-safe projection drops an unpaired tool message.
func estimateRetainedResultTokens(ctx context.Context, req RunRequest, state RunState, baseline prompt.RequestTokenBudget, target retainedResultTarget, fit compactionFitRunner) (int, error) {
	carrier := state.WithConversation([]Message{{Role: MessageRoleUser, Content: target.entry.Body}})
	one, err := fit(ctx, req, carrier)
	if err != nil {
		return 0, err
	}
	tokens := one.EstimatedPromptTokens - baseline.EstimatedPromptTokens
	if tokens < 0 {
		return 0, nil
	}
	return tokens, nil
}

// elideRetainedResult replaces target's body with its recall marker, writing the
// archive file first so no marker exists without its file. It reports false and
// leaves the body verbatim when the body is not worth eliding or the write
// fails. It never inspects the body for marker text: any body, marker-shaped or
// not, is archived verbatim and elided on its size alone.
func elideRetainedResult(ctx context.Context, req RunRequest, out []Message, target retainedResultTarget) bool {
	name := RecallName(target.entry)
	planned := RecallMarker(name, req.RecallArchive.Path(name))
	if len(target.entry.Body) < retainedResultMinBytes || len(target.entry.Body) < retainedResultMarkerFactor*len(planned) {
		return false
	}
	path, err := req.RecallArchive.Write(ctx, target.entry)
	if err != nil {
		emitEvent(req.Events, recallArchiveWarning(err))
		return false
	}
	marker := RecallMarker(name, path)
	if target.span == nil {
		out[target.msgIndex].Content = marker
		return true
	}
	span := *target.span
	content := out[target.msgIndex].Content
	rewritten, ok := ReplaceSubAgentResultInner(content[span.Start:span.End], marker)
	if !ok {
		// The span parsed when the targets were collected, so this only guards
		// against a concurrent mutation; the written file is a harmless orphan.
		return false
	}
	out[target.msgIndex].Content = content[:span.Start] + rewritten + content[span.End:]
	return true
}

// The recall marker compaction writes in place of an elided result body. It
// names the recall file, its absolute path, and the bash byte-range commands for
// text that read truncates. internal/session parses this shape when it rewrites
// inherited recall paths, so the format lives here as the one source of truth.
const (
	// RecallMarkerPrefix starts a marker, before the recall file name.
	RecallMarkerPrefix = "[Tool output omitted from active context: recall "
	// RecallMarkerRead separates the recall file name from its read path.
	RecallMarkerRead = ", read "
	// RecallMarkerSuffix ends a marker, after the read path.
	RecallMarkerSuffix = "; for long lines use bash head -c/tail -c]"
)

// RecallMarker returns the text that replaces one elided result body.
func RecallMarker(name, path string) string {
	return RecallMarkerPrefix + name + RecallMarkerRead + path + RecallMarkerSuffix
}

// resultElisionWarning reports a failed result-size estimate as a compact
// warning. Elision stops after the first one.
func resultElisionWarning(err error) output.Event {
	return output.NewConversationWarningEvent(output.ConversationWarningKindCompact, "estimate retained result size: "+err.Error())
}
