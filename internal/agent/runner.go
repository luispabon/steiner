package agent

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

const (
	maxRunnerRetries          = 3
	maxRunnerRateLimitRetries = 10
)

// Runner executes the main turn loop for an agent run.
type Runner struct{}

// NewRunner creates a new agent runner with default limits and state.
func NewRunner() *Runner {
	return &Runner{}
}

// Run executes req until the loop completes, stops, or fails. The returned
// state never carries image payloads: whatever exit path the run takes, every
// remaining image is stripped before Run returns.
func (r *Runner) Run(ctx context.Context, req RunRequest) (RunState, error) {
	req = normalizeRunRequest(req)
	state, err := r.run(ctx, req)
	return finalizeImagesAtRunExitForRequest(req, state), err
}

//nolint:gocyclo // turn loop branches are intentionally explicit
func (r *Runner) run(ctx context.Context, req RunRequest) (RunState, error) {
	state := initializeRunState(req)
	if validated, done, err := validateRunRequest(ctx, req, state); err != nil {
		return validated, err
	} else if done {
		return validated, nil
	}

	var err error
	state, err = postIngestionState(ctx, req, state)
	if err != nil {
		state.StopReason = StopReasonError
		return state, fmt.Errorf("post ingestion: %w", err)
	}

	basePrompt := prepareBasePrompt(req)
	p := newTurnProgressor(req, basePrompt, r.compactConversationForBudget)
	if state.ParentUsage != nil {
		p.lastUsage = state.ParentUsage.Usage
		p.statefulFloor = state.ParentUsage.StatefulFloor
	}
	runnerRetries := 0

	for {
		if stopped, done := stopRunBeforeTurn(ctx, req, state); done {
			return stopped, nil
		}

		state = injectTurnBudgetNoticeIfDue(state, req)

		turnCtx := ctx
		var cancel context.CancelFunc
		if req.Limits.TurnTimeout > 0 {
			turnCtx, cancel = context.WithTimeoutCause(ctx, req.Limits.TurnTimeout, errTurnTimeout)
		}
		outcome := p.advance(turnCtx, state)
		if cancel != nil {
			cancel()
		}
		state = outcome.State
		// Drain the inbox once at each turn boundary.
		wake := false
		if req.DrainInbox != nil {
			drain := req.DrainInbox()
			wake = drain.Wake
			if drain.Message != nil {
				state.Conversation = append(state.Conversation, *drain.Message)
				state.Lineage = state.Lineage.WithAppendedMessages([]Message{*drain.Message})
			}
			if drain.UserText != "" {
				emitEvent(req.Events, output.NewSteerReceivedEvent(drain.UserText, outputImageBlocks(drain.Images)))
			}
		}
		if outcome.Error != nil {
			if shouldRetry, retryErr := handleTransientProviderRetry(ctx, req.Events, state.TurnCount, outcome.Error, &runnerRetries); shouldRetry {
				if retryErr != nil {
					if ctx.Err() != nil {
						// Cancelled during backoff: report like any other cancellation.
						state.StopReason = StopReasonCancelled
						emitStop(req.Events, state, nil)
						return state, nil
					}
					emitStop(req.Events, state, outcome.Error)
					return state, &ReportedError{Err: outcome.Error}
				}
				continue
			}
			if ule, ok := provider.AsUsageLimit(outcome.Error); ok {
				if ule.Model == "" {
					ule.Model = req.ResolvedModel.DisplayRef()
				}
				if state.StopReason == StopReasonError {
					state.StopReason = StopReasonUsageLimit
				}
			}
			emitStop(req.Events, state, outcome.Error)
			return state, &ReportedError{Err: outcome.Error}
		}
		runnerRetries = 0
		if outcome.Stop {
			// If a wake delivery arrived during an assistant-only completion,
			// continue so it is actually sent.
			if wake && state.StopReason == StopReasonComplete {
				continue
			}
			if state.StopReason == StopReasonComplete {
				emitStop(req.Events, state, nil)
			}
			return state, nil
		}
	}
}

func normalizeRunRequest(req RunRequest) RunRequest {
	if req.ContextManager == nil {
		req.ContextManager = NewContextStateManager()
	}
	req.ContextManager.SetEventSink(req.Events)
	return req
}

func initializeRunState(req RunRequest) RunState {
	var conversation []Message
	if len(req.SourceConversation) > 0 {
		conversation = cloneMessages(req.SourceConversation)
	} else {
		conversation = fromProviderMessages(req.Prompt.Conversation)
	}
	state := RunState{
		Conversation: conversation,
		Lineage:      seedRunLineage(req.SeedLineage, conversation),
		ParentUsage:  seedParentUsage(req.SeedParentUsage, req),
	}
	state.TurnCount = initialConversationTurnCount(conversation)
	state.BudgetStartTurn = state.TurnCount
	return state
}

// seedRunLineage returns the run's single flat generation over conversation.
// It carries the seed's latest backend state only when the seed's latest
// generation matches conversation; otherwise the run starts with no backend
// state and the provider reseeds from the conversation.
func seedRunLineage(seed ConversationLineage, conversation []Message) ConversationLineage {
	lineage := newConversationLineage(conversation)
	latest, ok := seed.Latest()
	if !ok || !reflect.DeepEqual(latest.FullMessages(), lineage.FullMessages()) {
		return lineage
	}
	return lineage.withBackendState(lineage.latestGenerationID(), latest.BackendState, false)
}

func validateRunRequest(ctx context.Context, req RunRequest, state RunState) (RunState, bool, error) {
	if req.Provider == nil {
		state.StopReason = StopReasonError
		return state, false, errors.New("provider is required")
	}
	if req.Executor == nil {
		state.StopReason = StopReasonError
		return state, false, errors.New("tool executor is required")
	}
	if err := ctx.Err(); err != nil {
		state.StopReason = StopReasonCancelled
		emitStop(req.Events, state, nil)
		return state, true, nil
	}
	return state, false, nil
}

func postIngestionState(ctx context.Context, req RunRequest, state RunState) (RunState, error) {
	// PostIngestion rebuilds the lineage without backend state. It leaves the
	// messages unchanged, so the run's backend state still applies.
	backend := state.Lineage.latestBackendState()
	next, err := req.ContextManager.PostIngestion(ctx, state)
	next.Lineage = next.Lineage.withBackendState(next.Lineage.latestGenerationID(), backend, false)
	return next, err
}

func prepareBasePrompt(req RunRequest) prompt.AssemblyOptions {
	basePrompt := req.Prompt
	basePrompt.Conversation = nil
	// Cache the system preamble once per session so every turn sends the
	// byte-identical string, preventing KV cache busting on local servers.
	manager := req.ContextManager
	if manager == nil {
		manager = NewContextStateManager()
	}
	basePrompt.CachedPreamble = manager.CachedSystemPreamble(
		basePrompt.PromptOverrides.System,
		basePrompt.DelegationEnabled,
		basePrompt.AsyncSubAgents,
		basePrompt.OrchestrationLevel,
		basePrompt.AdvisorEnabled,
		basePrompt.LSPEnabled,
		basePrompt.WorkflowMode,
		basePrompt.PromptOverrides.SystemSuffix,
		basePrompt.SandboxEnabled,
		basePrompt.SandboxWritableMounts,
	)
	return basePrompt
}

func stopRunBeforeTurn(ctx context.Context, req RunRequest, state RunState) (RunState, bool) {
	if err := ctx.Err(); err != nil {
		state.StopReason = StopReasonCancelled
		emitStop(req.Events, state, nil)
		return state, true
	}
	if req.Limits.MaxTurns > 0 && state.TurnCount >= req.Limits.MaxTurns {
		state.StopReason = StopReasonMaxTurns
		emitStop(req.Events, state, nil)
		return state, true
	}
	if req.Limits.MaxTokens > 0 && state.TokenCount >= req.Limits.MaxTokens {
		state.StopReason = StopReasonMaxTokens
		emitStop(req.Events, state, nil)
		return state, true
	}
	return state, false
}

func initialConversationTurnCount(messages []Message) int {
	maxTurn := 0
	for _, message := range messages {
		if message.Turn > maxTurn {
			maxTurn = message.Turn
		}
	}
	return maxTurn
}

func handleTransientProviderRetry(ctx context.Context, events output.EventSink, turn int, err error, retries *int) (shouldRetry bool, sleepErr error) {
	delay, ok := provider.RetryableProviderError(err)
	if !ok {
		return false, nil
	}
	limit := maxRunnerRetries
	if provider.IsRateLimitError(err) {
		limit = maxRunnerRateLimitRetries
	}
	if *retries >= limit {
		return false, nil
	}
	*retries++
	if delay < 5*time.Second {
		delay = 5 * time.Second
	}
	emitEvent(events, output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{
		Turn:     turn,
		Severity: "warning",
		Message:  fmt.Sprintf("provider returned transient error, retrying turn in %s (attempt %d/%d): %s", delay, *retries, limit, err),
	}))
	return true, runnerRetrySleep(ctx, delay)
}

var runnerRetrySleepFn = runnerRetrySleepDefault

func runnerRetrySleep(ctx context.Context, delay time.Duration) error {
	return runnerRetrySleepFn(ctx, delay)
}

func runnerRetrySleepDefault(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
