package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/usagestats"
)

const (
	maxRunnerRetries          = 3
	maxRunnerRateLimitRetries = 10
)

// ToolExecutor runs a named tool invocation for the agent loop.
type ToolExecutor interface {
	Execute(ctx context.Context, toolName, callID string, input map[string]any) (any, error)
}

// usageRecorder records per-call token usage for cache-hit-rate analytics.
// It is satisfied by *usagestats.Recorder; nil means recording is disabled.
type usageRecorder interface {
	Record(usagestats.Observation)
}

// RunRequest carries all parameters needed for a single agent run.
type RunRequest struct {
	Provider       provider.Provider
	Executor       ToolExecutor
	Tools          []provider.ToolSpec
	Prompt         prompt.AssemblyOptions
	ModelBudget    prompt.ModelTokenBudget
	ResolvedModel  provider.ResolvedModel
	MaxTokens      *int
	Limits         Limits
	Events         output.EventSink
	ContextManager *ContextStateManager

	// StreamingPreferred signals whether the caller wants streaming responses.
	// When false, ChatCompletion is tried first and streaming is used only as a
	// fallback. Interactive mode sets this to true; --exec defaults to false.
	StreamingPreferred bool

	// CaveHuman makes the model speak tersely and avoid AI-writing tells.
	CaveHuman bool

	// PromptCacheKey is a stable identifier sent to the provider to route
	// requests to the same cache shard across turns. Empty disables it.
	PromptCacheKey string

	// CompactionLogPath is an optional file path for logging compaction request/response pairs.
	// When non-empty, compaction calls write their full API request and final response to this file.
	CompactionLogPath string

	// DrainSteers drains all queued between-turn steering messages.
	// Non-nil only in interactive mode; sub-agents receive nil.
	DrainSteers func() []SteerMessage

	// UsageRecorder records cache-hit-rate observations per usage-bearing model
	// response. Nil disables recording (tests, unwired paths).
	UsageRecorder usageRecorder

	// UsageSource identifies which call surface this run represents, for
	// session-scoped usage attribution. The zero value is usagestats.SourceParent.
	UsageSource usagestats.Source

	// VisionCapabilities tracks per-model vision capability for the session.
	// Nil disables capability-driven retry logic (tests, unwired paths preserve old behavior).
	VisionCapabilities *VisionCapabilities
}

// Runner executes the main turn loop for an agent run.
type Runner struct{}

// NewRunner creates a new agent runner with default limits and state.
func NewRunner() *Runner {
	return &Runner{}
}

// Run executes req until the loop completes, stops, or fails.
//
//nolint:gocyclo // turn loop branches are intentionally explicit
func (r *Runner) Run(ctx context.Context, req RunRequest) (RunState, error) {
	req = normalizeRunRequest(req)
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
	runnerRetries := 0

	for {
		if stopped, done := stopRunBeforeTurn(ctx, req, state); done {
			return stopped, nil
		}

		turnCtx := ctx
		var cancel context.CancelFunc
		if req.Limits.TurnTimeout > 0 {
			turnCtx, cancel = context.WithTimeout(ctx, req.Limits.TurnTimeout)
		}
		outcome := p.advance(turnCtx, state)
		if cancel != nil {
			cancel()
		}
		state = outcome.State
		// Drain all queued steering messages at turn boundary.
		hadSteers := false
		if req.DrainSteers != nil {
			steers := req.DrainSteers()
			if len(steers) > 0 {
				hadSteers = true
				merged := mergeSteers(steers)
				state.Conversation = append(state.Conversation, merged)
				state.Lineage = state.Lineage.WithAppendedMessages([]Message{merged})
				emitEvent(req.Events, output.NewSteerReceivedEvent(merged.Content))
			}
		}
		if outcome.Error != nil {
			if shouldRetry, retryErr := handleTransientProviderRetry(ctx, req.Events, state.TurnCount, outcome.Error, &runnerRetries); shouldRetry {
				if retryErr != nil {
					emitStop(req.Events, state, outcome.Error)
					return state, outcome.Error
				}
				continue
			}
			emitStop(req.Events, state, outcome.Error)
			return state, outcome.Error
		}
		runnerRetries = 0
		if outcome.Stop {
			// If the user queued steers during an assistant-only completion,
			// continue so the queued messages are actually sent.
			if hadSteers && state.StopReason == StopReasonComplete {
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
	conversation := fromProviderMessages(req.Prompt.Conversation)
	state := RunState{
		Conversation: conversation,
		Lineage:      newConversationLineage(conversation),
		Context:      fromPromptContext(req.Prompt.ContextState),
	}
	state.TurnCount = initialConversationTurnCount(conversation)
	return state
}

func validateRunRequest(ctx context.Context, req RunRequest, state RunState) (RunState, bool, error) {
	if req.Provider == nil {
		state.StopReason = StopReasonError
		return state, false, fmt.Errorf("provider is required")
	}
	if req.Executor == nil {
		state.StopReason = StopReasonError
		return state, false, fmt.Errorf("tool executor is required")
	}
	if err := ctx.Err(); err != nil {
		state.StopReason = StopReasonCancelled
		emitStop(req.Events, state, nil)
		return state, true, nil
	}
	return state, false, nil
}

func postIngestionState(ctx context.Context, req RunRequest, state RunState) (RunState, error) {
	return req.ContextManager.PostIngestion(ctx, state)
}

func prepareBasePrompt(req RunRequest) prompt.AssemblyOptions {
	basePrompt := req.Prompt
	basePrompt.Conversation = nil
	// Plumb the merged cave/human prompt mode through to prompt assembly.
	basePrompt.CaveHuman = req.CaveHuman
	// Cache the system preamble once per session so every turn sends the
	// byte-identical string, preventing KV cache busting on local servers.
	manager := req.ContextManager
	if manager == nil {
		manager = NewContextStateManager()
	}
	basePrompt.CachedPreamble = manager.CachedSystemPreamble(
		basePrompt.PromptOverrides.System,
		basePrompt.DelegationEnabled,
		basePrompt.AdvisorEnabled,
		basePrompt.WorkflowMode,
		basePrompt.CaveHuman,
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

func formatToolError(err error) string {
	var tee *tool.ToolExecutionError
	if ok := errors.As(err, &tee); ok {
		details := map[string]any{
			"exit_code": tee.ExitCode,
			"stdout":    tee.Output.Stdout.Preview,
			"stderr":    tee.Output.Stderr.Preview,
		}
		if tee.Output.Stdout.Summary() != "" || tee.Output.Stderr.Summary() != "" {
			details["stdout"] = tee.Output.Stdout.Summary()
			details["stderr"] = tee.Output.Stderr.Summary()
		}
		return marshalToolErrorEnvelope(tee.Kind, tee.Message, details)
	}
	return marshalToolErrorEnvelope("tool_error", err.Error(), nil)
}

func marshalToolErrorEnvelope(kind, message string, details map[string]any) string {
	envelope := tool.JSONEnvelope{
		OK: false,
		Error: &tool.JSONEnvelopeError{
			Kind:    kind,
			Message: message,
			Details: details,
		},
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Sprintf(`{"ok":false,"error":{"kind":"%s","message":"%s"}}`, kind, message)
	}
	return string(data)
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

var imageMarkerRe = regexp.MustCompile(`\[Image (\d+)\]`)

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

// mergeSteers combines multiple steer messages into a single user Message.
// Image markers in later steers are renumbered to follow prior steer images.
func mergeSteers(steers []SteerMessage) Message {
	if len(steers) == 1 {
		return Message{Role: MessageRoleUser, Content: steers[0].Text, Images: steers[0].Images}
	}
	var texts []string
	var images []ImageBlock
	offset := 0
	for i, s := range steers {
		if i > 0 {
			s.Text = renumberMarkers(s.Text, offset)
		}
		texts = append(texts, s.Text)
		images = append(images, s.Images...)
		offset += len(s.Images)
	}
	return Message{Role: MessageRoleUser, Content: strings.Join(texts, "\n\n"), Images: images}
}

// renumberMarkers finds [Image N] markers in text and adds offset to N.
// The marker format is [Image N] where N is 1-indexed (matching internal/tui/image_markers.go).
func renumberMarkers(text string, offset int) string {
	return imageMarkerRe.ReplaceAllStringFunc(text, func(match string) string {
		var n int
		_, _ = fmt.Sscanf(match, "[Image %d]", &n)
		return fmt.Sprintf("[Image %d]", n+offset)
	})
}
