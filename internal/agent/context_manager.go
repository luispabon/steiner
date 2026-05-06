package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/tool"
)

// Compactor reduces an oversize conversation to fit the model token budget.
type Compactor interface {
	Compact(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate) (CompactionOutcome, error)
}

// ContextManager is the pipeline hook interface for active context management.
// PostIngestion runs once after the initial conversation is loaded.
// PreAssembly runs before each turn's prompt is assembled.
// OnTurnComplete is called after each turn with whether the scratchpad tool was called.
type ContextManager interface {
	PostIngestion(ctx context.Context, state RunState) (RunState, error)
	PreAssembly(ctx context.Context, state RunState) (RunState, error)
	OnTurnComplete(turnIndex int, scratchpadCalled bool)
}

// NaiveContextManager is a pass-through implementation that leaves state
// unchanged. It preserves the existing compaction behaviour entirely.
type NaiveContextManager struct{}

// PostIngestion returns the state unchanged.
func (n *NaiveContextManager) PostIngestion(_ context.Context, state RunState) (RunState, error) {
	return state, nil
}

// PreAssembly returns the state unchanged.
func (n *NaiveContextManager) PreAssembly(_ context.Context, state RunState) (RunState, error) {
	return state, nil
}

// OnTurnComplete is a no-op for the naive manager.
func (n *NaiveContextManager) OnTurnComplete(_ int, _ bool) {}

// RecordMutation is a no-op for the naive manager.
func (n *NaiveContextManager) RecordMutation(_ string) {}

// SmartContextManager applies ingestion-time shaping to tool output so the
// active conversation starts in a compact, signal-rich form.
type SmartContextManager struct {
	maskingWindowTurns int
	readAnnotations    bool
	configApplied      bool
	compactionStrategy config.CompactionStrategy
	scratchpadMode     config.ScratchpadMode
	events             output.EventSink
	fileTracker        FileTracker
	scratchpad         Scratchpad
	scratchpadFailures int
	epochMaskBoundary  int
	epochStartTurn     int
	// minVisibleTurn tracks the lowest turn number whose messages are still
	// fully visible to the model (not masked, not compacted away). Updated
	// during PostIngestion and PreAssembly. Used alongside epochMaskBoundary
	// to gate file read annotations.
	minVisibleTurn          int
	contextPressureTrigger  func(currentTurn int, state RunState) bool
	lastScaffoldFingerprint string
	// cachedPreamble holds the system preamble built once per session.
	// Both inputs (override string, scratchpadEnabled bool) are session-constants,
	// so building the string once prevents unnecessary allocations and keeps
	// the bytes byte-identical across turns, preserving KV cache hits.
	cachedPreamble string
}

// CachedSystemPreamble returns the system preamble string, building it once
// and caching it for the lifetime of the manager.
func (s *SmartContextManager) CachedSystemPreamble(override string, scratchpadEnabled bool) string {
	if s.cachedPreamble == "" {
		s.cachedPreamble = prompt.SystemPreamble(override, scratchpadEnabled).Content
	}
	return s.cachedPreamble
}

// Compact selects the configured compaction strategy for smart context
// management.
func (s *SmartContextManager) Compact(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate) (CompactionOutcome, error) {
	switch s.compactionStrategy {
	case config.CompactionStrategyDrop:
		return dropCompactor{retainTurns: defaultDropRetainTurns}.Compact(ctx, req, state, turn, candidate)
	case config.CompactionStrategyHybrid:
		return hybridCompactor{maskingWindowTurns: s.maskingWindow()}.Compact(ctx, req, state, turn, candidate)
	case config.CompactionStrategySummarize, "":
		fallthrough
	default:
		return summarizeCompactor{}.Compact(ctx, req, state, turn, candidate)
	}
}

// PostIngestion normalizes tool output in the loaded conversation.
func (s *SmartContextManager) PostIngestion(_ context.Context, state RunState) (RunState, error) {
	next := state.Clone()
	next.Conversation = s.normalizeIngestedMessages(next.TurnCount, next.Conversation)
	next.Lineage = newConversationLineage(next.Conversation)
	s.minVisibleTurn = minTurnInMessages(next.Conversation)
	s.initializeEpochFromTurnCount(next.TurnCount)
	return next, nil
}

// PreAssembly applies non-destructive prompt-time masking on a copy of state.
func (s *SmartContextManager) PreAssembly(_ context.Context, state RunState) (RunState, error) {
	next := state.Clone()
	s.resetTaskStateIfNeeded(&next)
	next.Context = s.enrichContextState(next)
	conversation := next.Lineage.SummaryPrefixStrippedMessages()
	if len(conversation) == 0 {
		conversation = next.Conversation
	}
	s.initializeEpochFromTurnCount(next.TurnCount)
	currentTurn := next.TurnCount + 1
	previousBoundary := s.epochMaskBoundary
	previousStartTurn := s.epochStartTurn
	trigger := ""
	if s.shouldAdvanceEpoch(currentTurn, next) {
		trigger = s.advanceEpoch(currentTurn)
	}
	window := s.maskingWindow()
	masked := fromProviderMessages(prompt.MaskConversationBeforeTurn(toProviderMessages(conversation), s.epochMaskBoundary))
	s.emitMaskingDiagnostics(currentTurn, window, previousBoundary, previousStartTurn, trigger, conversation, masked)
	next.Conversation = masked
	next.Lineage = next.Lineage.WithCurrentMessages(masked)
	s.minVisibleTurn = minTurnInMessages(masked)
	return next, nil
}

// IngestAssistantResponse returns the assistant content unchanged.
// Scratchpad state is now captured exclusively via tool results; failure
// counting has moved to OnTurnComplete.
func (s *SmartContextManager) IngestAssistantResponse(_ int, content string) (string, string) {
	return content, ""
}

// OnTurnComplete tracks whether the scratchpad tool was called this turn and
// emits a warning event when three consecutive turns have been missed.
func (s *SmartContextManager) OnTurnComplete(turnIndex int, scratchpadCalled bool) {
	if s.scratchpadMode != config.ScratchpadModeHybrid {
		return
	}
	if scratchpadCalled {
		s.scratchpadFailures = 0
		return
	}
	s.scratchpadFailures++
	if s.scratchpadFailures >= 3 {
		note := "scratchpad tool not called in 3+ consecutive turns"
		emitEvent(s.events, output.NewScratchpadEvent(turnIndex, false, s.scratchpad.Render(), s.scratchpadFailures, note))
	}
}

// IngestToolResult shapes a newly produced tool result before it enters the
// active conversation history.
func (s *SmartContextManager) IngestToolResult(turn int, toolName, content string) string {
	return s.observeToolResult(turn, toolName, nil, content)
}

// RecordMutation bumps the in-memory file generation for a successful
// steiner-originated mutation.
func (s *SmartContextManager) RecordMutation(path string) {
	s.fileTracker.BumpGeneration(path)
}

// ResetEpoch resets the current masking epoch after compaction so retained
// conversation starts from a clean boundary.
func (s *SmartContextManager) ResetEpoch(turn int) {
	s.resetEpoch(turn, "compaction")
}

// RecordCompaction appends a scaffold-managed compaction fact.
func (s *SmartContextManager) RecordCompaction(turn int) {
	s.appendDecisionFact(fmt.Sprintf("compaction occurred at turn %d", turn))
}

// ObserveToolResult records heuristic context derived from a tool result.
func (s *SmartContextManager) ObserveToolResult(turn int, toolName string, input map[string]any, content string) string {
	return s.observeToolResult(turn, toolName, input, content)
}

// SetEventSink installs the sink used for context-management diagnostics.
func (s *SmartContextManager) SetEventSink(sink output.EventSink) {
	s.events = sink
}

// NewContextManager constructs the appropriate ContextManager for the given
// mode. An unrecognised mode falls back to NaiveContextManager.
func NewContextManager(mode string, cfg ...config.ContextManagementConfig) ContextManager {
	if mode == "smart" {
		manager := &SmartContextManager{scratchpadMode: config.ScratchpadModeScaffoldOnly}
		if len(cfg) > 0 {
			manager.maskingWindowTurns = cfg[0].MaskingWindowTurns
			manager.readAnnotations = cfg[0].ReadAnnotations
			manager.compactionStrategy = cfg[0].CompactionStrategy
			if cfg[0].ScratchpadMode != "" {
				manager.scratchpadMode = cfg[0].ScratchpadMode
			}
			manager.configApplied = true
		}
		return manager
	}
	return &NaiveContextManager{}
}

func (s *SmartContextManager) normalizeIngestedMessages(turn int, messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]Message, len(messages))
	for i, message := range messages {
		out[i] = s.normalizeIngestedMessage(turn, message)
	}
	return out
}

func (s *SmartContextManager) normalizeIngestedMessage(turn int, message Message) Message {
	if message.Role != MessageRoleTool {
		return message
	}
	if strings.TrimSpace(message.Content) == "" {
		return message
	}
	// Loaded tool history should keep each message's recorded turn so distinct
	// reads do not collapse onto one shared session turn.
	messageTurn := message.Turn
	if messageTurn <= 0 {
		messageTurn = turn
	}
	if messageTurn <= 0 {
		return message
	}
	message.Content = s.IngestToolResult(messageTurn, message.Name, message.Content)
	return message
}

func (s *SmartContextManager) syncScaffoldState(state RunState, next *ContextState) {
	if next == nil {
		return
	}
	s.scratchpad.SessionState = compactSessionState(next.TurnCount, next.CompactionCount)
}

func (s *SmartContextManager) observeToolResult(turn int, toolName string, input map[string]any, content string) string {
	shaped := tool.ShapeIngestedToolResult(toolName, content)
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "scratchpad":
		next, ok := parseScratchpadToolResult(shaped, s.scratchpad)
		if ok {
			s.scratchpad = next
			s.scratchpad.LastAction = "scratchpad updated"
			s.scratchpadFailures = 0
			emitEvent(s.events, output.NewScratchpadEvent(turn, true, s.scratchpad.Render(), 0, ""))
			emitEvent(s.events, output.NewScratchpadUpdatedEvent(output.ScratchpadUpdatedEvent{
				Intent:    s.scratchpad.Intent,
				Decisions: s.scratchpad.Decisions,
				Open:      s.scratchpad.Open,
				Next:      s.scratchpad.Next,
			}))
		}
		return `{"ok":true}`
	case "read":
		result, _ := parseReadResult(shaped)
		next, observation := s.fileTracker.ObserveRead(turn, shaped, s.annotationsEnabled())
		// Visibility gate: if the original read turn is no longer safely
		// referenceable, suppress the annotation and return full content so the
		// model isn't confused by a dangling turn reference.
		if observation.Action == "annotated" {
			if observation.PreviousRead.LastTurn <= 0 {
				next = shaped
				observation.Action = "full"
				observation.Reason = "previous read turn not safely referenceable"
			} else {
				dropped := s.minVisibleTurn > 0 && observation.PreviousRead.LastTurn < s.minVisibleTurn
				masked := s.epochMaskBoundary > 0 && observation.PreviousRead.LastTurn < s.epochMaskBoundary
				if dropped || masked {
					next = shaped
					observation.Action = "full"
					observation.Reason = "previous read no longer visible in context"
				}
			}
		}
		s.emitFileAnnotationDiagnostics(turn, result, observation, shaped, next)
		s.observeReadHeuristics(turn, result, observation, next)
		return next
	case "edit", "write", "apply_patch":
		s.observeMutationHeuristics(turn, toolName, input, shaped)
		return shaped
	case "bash":
		s.observeBashHeuristics(turn, input, shaped)
		return shaped
	default:
		s.observeGenericToolHeuristics(turn, toolName, shaped)
		return shaped
	}
}

func (s *SmartContextManager) observeReadHeuristics(turn int, result readResult, observation fileObservation, content string) {
	path := strings.TrimSpace(result.Path)
	if path == "" {
		return
	}
	s.updateWorkingFile(path, turn, "read", fmt.Sprintf("read %s (%s)", path, result.rangeSummary()))
	if observation.Action == "annotated" || strings.Contains(content, "file unchanged since turn") {
		s.appendDecisionFact(fmt.Sprintf("read annotation: %s", summarizeTextPreview(content, 96)))
	}
	if observation.Action == "full" && observation.Reason == "previous read no longer visible in context" {
		s.appendDecisionFact(fmt.Sprintf("read %s: full content (previous read turn %d no longer visible)", path, observation.PreviousRead.LastTurn))
	}
}

func (s *SmartContextManager) observeMutationHeuristics(turn int, toolName string, input map[string]any, content string) {
	var result struct {
		Path   string `json:"path"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return
	}
	path := strings.TrimSpace(result.Path)
	if path == "" && input != nil {
		if rawPath, ok := input["path"].(string); ok {
			path = strings.TrimSpace(rawPath)
		}
	}
	if path != "" {
		s.updateWorkingFile(path, turn, toolName, fmt.Sprintf("%s %s: %s", toolVerb(toolName), path, summarizeTextPreview(result.Output, 96)))
	}
	if toolName == "edit" {
		s.appendDecisionFact(fmt.Sprintf("edited %s: %s", path, summarizeTextPreview(result.Output, 96)))
	}
}

func (s *SmartContextManager) observeBashHeuristics(turn int, input map[string]any, content string) {
	var result struct {
		ExitCode  int    `json:"exit_code"`
		Truncated bool   `json:"truncated"`
		Output    string `json:"output"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return
	}

	command := ""
	if input != nil {
		command, _ = input["command"].(string)
	}
	command = strings.TrimSpace(command)
	preview := summarizeTextPreview(result.Output, 96)
	if preview == "" {
		preview = strings.TrimSpace(result.Message)
	}
	if preview == "" {
		preview = fmt.Sprintf("exit_code=%d", result.ExitCode)
	}

	if isTestCommand(command) {
		status := "failed"
		if result.ExitCode == 0 {
			status = "passed"
		}
		s.appendDecisionFact(fmt.Sprintf("tests %s: %s", status, summarizeTextPreview(command, 80)))
	}
	s.scratchpad.LastAction = fmt.Sprintf("bash: %s", preview)
}

func (s *SmartContextManager) observeGenericToolHeuristics(turn int, toolName string, content string) {
	s.scratchpad.LastAction = fmt.Sprintf("%s: %s", strings.TrimSpace(toolName), summarizeTextPreview(content, 80))
	_ = turn
}

func (s *SmartContextManager) updateWorkingFile(path string, turn int, toolName, lastAction string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	s.scratchpad.WorkingFile = path
	s.scratchpad.LastAction = lastAction
	if strings.TrimSpace(toolName) != "" {
		s.scratchpad.LastAction = lastAction
	}
	_ = turn
}

func (s *SmartContextManager) appendDecisionFact(fact string) {
	fact = strings.TrimSpace(fact)
	if fact == "" || strings.EqualFold(fact, "none") {
		return
	}
	combined := strings.TrimSpace(s.scratchpad.Decisions)
	if combined != "" {
		combined += "\n" + fact
	} else {
		combined = fact
	}
	for len(combined) > decisionsMaxBytes {
		idx := strings.Index(combined, "\n")
		if idx < 0 {
			combined = combined[len(combined)-decisionsMaxBytes:]
			break
		}
		combined = strings.TrimSpace(combined[idx+1:])
	}
	s.scratchpad.Decisions = combined
}

func toolVerb(toolName string) string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "edit":
		return "edited"
	case "write":
		return "wrote"
	case "apply_patch":
		return "patched"
	default:
		return "updated"
	}
}

func isTestCommand(command string) bool {
	command = strings.ToLower(strings.TrimSpace(command))
	if command == "" {
		return false
	}
	switch {
	case strings.Contains(command, "go test"),
		strings.Contains(command, "pytest"),
		strings.Contains(command, "cargo test"),
		strings.Contains(command, "npm test"),
		strings.Contains(command, "pnpm test"),
		strings.Contains(command, "yarn test"),
		strings.Contains(command, "bun test"),
		strings.Contains(command, "make test"):
		return true
	}
	if strings.HasPrefix(command, "test ") || strings.Contains(command, " test ") {
		return true
	}
	return false
}

func (s *SmartContextManager) maskingWindow() int {
	if s.maskingWindowTurns <= 0 {
		return 5
	}
	return s.maskingWindowTurns
}

func (s *SmartContextManager) initializeEpochFromTurnCount(turnCount int) {
	if turnCount <= 0 {
		return
	}
	if s.epochStartTurn > 0 || s.epochMaskBoundary > 0 {
		return
	}
	window := s.maskingWindow()
	s.epochStartTurn = turnCount
	s.epochMaskBoundary = turnCount - window
	if s.epochMaskBoundary < 0 {
		s.epochMaskBoundary = 0
	}
}

func (s *SmartContextManager) shouldAdvanceEpoch(currentTurn int, state RunState) bool {
	window := s.maskingWindow()
	if currentTurn-s.epochStartTurn >= window {
		return true
	}
	if s.contextPressureTrigger == nil {
		return false
	}
	return s.contextPressureTrigger(currentTurn, state)
}

func (s *SmartContextManager) advanceEpoch(currentTurn int) string {
	window := s.maskingWindow()
	trigger := "turn_count"
	if s.contextPressureTrigger != nil && currentTurn-s.epochStartTurn < window {
		trigger = "context_pressure"
	}
	s.epochMaskBoundary = currentTurn - window
	if s.epochMaskBoundary < 0 {
		s.epochMaskBoundary = 0
	}
	s.epochStartTurn = currentTurn
	return trigger
}

func (s *SmartContextManager) resetEpoch(turn int, trigger string) {
	s.fileTracker.PruneBeforeTurn(turn)
	s.epochMaskBoundary = 0
	s.epochStartTurn = turn
	s.emitEpochResetDiagnostic(turn, trigger)
}

func (s *SmartContextManager) annotationsEnabled() bool {
	if !s.configApplied {
		return true
	}
	return s.readAnnotations
}

func (s *SmartContextManager) enrichContextState(state RunState) ContextState {
	next := state.Context.Clone()
	next.TurnCount = state.TurnCount
	s.syncScaffoldState(state, &next)
	next.Scratchpad = s.scratchpad.Render()
	return next
}

func (s *SmartContextManager) scaffoldPromptState() string {
	return s.scratchpad.Render()
}

func (s *SmartContextManager) shouldRunScaffoldInference(state RunState, compactionCount int) bool {
	if s.scratchpadMode != config.ScratchpadModeScaffoldOnly {
		return false
	}
	fingerprint := s.scaffoldFingerprint(state, compactionCount)
	if fingerprint == s.lastScaffoldFingerprint {
		return false
	}
	s.lastScaffoldFingerprint = fingerprint
	return true
}

func (s *SmartContextManager) applyScaffoldInference(turn int, content string) (bool, string) {
	next, parsed, note := parseScaffoldInferenceResult(content, s.scratchpad)
	if !parsed {
		emitEvent(s.events, output.NewScratchpadEvent(turn, false, s.scratchpad.Render(), 0, note))
		return false, note
	}
	s.scratchpad = next
	emitEvent(s.events, output.NewScratchpadEvent(turn, true, s.scratchpad.Render(), 0, ""))
	emitEvent(s.events, output.NewScratchpadUpdatedEvent(output.ScratchpadUpdatedEvent{
		Intent:    s.scratchpad.Intent,
		Decisions: s.scratchpad.Decisions,
		Open:      s.scratchpad.Open,
		Next:      s.scratchpad.Next,
	}))
	return true, ""
}

func (s *SmartContextManager) scaffoldFingerprint(state RunState, compactionCount int) string {
	toolSummary := ""
	if recent := summarizeRecentToolCalls(state.Lineage.FullMessages(), 1); len(recent) > 0 {
		toolSummary = recent[0]
	}
	workingFile := strings.TrimSpace(s.scratchpad.WorkingFile)
	return fmt.Sprintf("compaction=%d|working=%s|tool=%s", compactionCount, workingFile, toolSummary)
}

func (s *SmartContextManager) emitFileAnnotationDiagnostics(turn int, result readResult, observation fileObservation, original, shaped string) {
	if s.events == nil {
		return
	}
	if strings.TrimSpace(result.Path) == "" {
		return
	}

	notes := []string{fmt.Sprintf("range=%s", result.rangeSummary())}
	if strings.TrimSpace(original) != strings.TrimSpace(shaped) {
		notes = append(notes, "annotation produced")
	}
	notes = append(notes, observation.Notes...)

	previousTurn := 0
	if observation.HadPrevious {
		previousTurn = observation.PreviousRead.LastTurn
	}
	emitEvent(s.events, output.NewFileAnnotationEvent(
		turn,
		strings.TrimSpace(result.Path),
		observation.Action,
		observation.Reason,
		previousTurn,
		notes...,
	))
}

func (s *SmartContextManager) emitEpochResetDiagnostic(turn int, trigger string) {
	if s.events == nil {
		return
	}
	emitEvent(s.events, output.NewContextMaskingEvent(
		turn,
		"",
		"reset",
		"epoch boundary reset",
		s.maskingWindow(),
		s.epochMaskBoundary,
		s.epochStartTurn,
		0,
		trigger,
		"reset",
	))
}

func (s *SmartContextManager) emitMaskingDiagnostics(turn, window, previousBoundary, previousStartTurn int, trigger string, original, masked []Message) {
	if s.events == nil || len(original) == 0 || len(original) != len(masked) {
		return
	}
	newlyMaskedTurns := map[int]struct{}{}
	for i := range original {
		if strings.TrimSpace(original[i].Content) == strings.TrimSpace(masked[i].Content) {
			continue
		}
		action := "masked"
		reason := "older than masking window"
		epochStatus := "previously_masked"
		toolName := strings.TrimSpace(original[i].Name)
		switch original[i].Role {
		case MessageRoleAssistant:
			if strings.TrimSpace(masked[i].Content) != "" && strings.TrimSpace(masked[i].Content) != strings.TrimSpace(original[i].Content) {
				action = "trimmed"
				reason = "older assistant prose"
			}
		case MessageRoleTool:
			if toolName == "" {
				toolName = "tool"
			}
			reason = "older tool result"
		default:
			action = "masked"
		}
		if trigger != "" && original[i].Turn > 0 && original[i].Turn >= previousBoundary && original[i].Turn < s.epochMaskBoundary {
			epochStatus = "newly_masked"
			newlyMaskedTurns[original[i].Turn] = struct{}{}
		}
		notes := []string{fmt.Sprintf("message_index=%d", i)}
		if original[i].ToolCallID != "" {
			notes = append(notes, "tool_call_id="+original[i].ToolCallID)
		}
		emitEvent(s.events, output.NewContextMaskingEvent(turn, toolName, action, reason, window, s.epochMaskBoundary, s.epochStartTurn, 0, trigger, epochStatus, notes...))
	}
	if trigger != "" {
		emitEvent(s.events, output.NewContextMaskingEvent(
			turn,
			"",
			"advance",
			"epoch boundary advanced",
			window,
			s.epochMaskBoundary,
			s.epochStartTurn,
			len(newlyMaskedTurns),
			trigger,
			"advance",
			fmt.Sprintf("previous_boundary=%d", previousBoundary),
			fmt.Sprintf("previous_start_turn=%d", previousStartTurn),
		))
	}
}

func shapeIngestedToolResultForContextManager(cm ContextManager, turn int, toolName string, input map[string]any, content string) string {
	type toolResultIngestor interface {
		ObserveToolResult(turn int, toolName string, input map[string]any, content string) string
	}
	if ingestor, ok := cm.(toolResultIngestor); ok {
		return ingestor.ObserveToolResult(turn, toolName, input, content)
	}
	return content
}

func processAssistantResponseForContextManager(cm ContextManager, turn int, content string) (string, string) {
	type assistantIngestor interface {
		IngestAssistantResponse(turn int, content string) (string, string)
	}
	if ingestor, ok := cm.(assistantIngestor); ok {
		return ingestor.IngestAssistantResponse(turn, content)
	}
	return content, ""
}

func summarizeRecentToolCalls(messages []Message, limit int) []string {
	if limit <= 0 {
		return nil
	}
	var out []string
	for i := len(messages) - 1; i >= 0 && len(out) < limit; i-- {
		message := messages[i]
		if message.Role != MessageRoleAssistant || len(message.ToolCalls) == 0 {
			continue
		}
		for j := len(message.ToolCalls) - 1; j >= 0 && len(out) < limit; j-- {
			call := message.ToolCalls[j]
			out = append(out, call.Name+" "+summarizeCallArguments(call.Arguments))
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func summarizeCallArguments(arguments map[string]any) string {
	if len(arguments) == 0 {
		return ""
	}
	parts := make([]string, 0, 3)
	for _, key := range []string{"path", "pattern", "command", "offset", "limit"} {
		value, ok := arguments[key]
		if !ok {
			continue
		}
		parts = append(parts, key+"="+summarizeTextValue(value))
	}
	return strings.TrimSpace(strings.Join(parts, ", "))
}

func summarizeTextValue(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if len(text) <= 40 {
		return text
	}
	return text[:40] + "..."
}

// minTurnInMessages returns the smallest positive Turn value across all
// messages, or 0 if no messages have a Turn set.
func minTurnInMessages(messages []Message) int {
	minTurn := 0
	for _, m := range messages {
		if m.Turn > 0 {
			if minTurn == 0 || m.Turn < minTurn {
				minTurn = m.Turn
			}
		}
	}
	return minTurn
}

const decisionsMaxBytes = 2000
const decisionsMaxLines = 24

func parseScratchpadToolResult(content string, previous Scratchpad) (Scratchpad, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return previous, false
	}

	next := previous

	if v, ok := decodeScratchpadString(raw, "intent"); ok {
		next.Intent = v
	} else {
		return previous, false
	}
	if v, ok := decodeScratchpadString(raw, "decisions"); ok {
		next.Decisions = appendDecisionFactText(previous.Decisions, v)
	} else {
		return previous, false
	}
	if v, ok := decodeScratchpadString(raw, "open"); ok {
		next.Open = v
	} else {
		return previous, false
	}
	if v, ok := decodeScratchpadString(raw, "next"); ok {
		next.Next = v
	} else {
		return previous, false
	}
	return next, true
}

func parseScaffoldInferenceResult(content string, previous Scratchpad) (Scratchpad, bool, string) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return previous, false, "invalid scaffold inference JSON: " + err.Error()
	}

	next := previous
	if v, ok := decodeScratchpadString(raw, "intent"); ok {
		next.Intent = v
	}
	if v, ok := decodeScratchpadString(raw, "next"); ok {
		next.Next = v
	}
	return next, true, ""
}

func decodeScratchpadString(raw map[string]json.RawMessage, key string) (string, bool) {
	value, ok := raw[key]
	if !ok {
		return "", false
	}
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return "", false
	}
	return text, true
}

func appendDecisionFactText(existing, fact string) string {
	fact = strings.TrimSpace(fact)
	if fact == "" || strings.EqualFold(fact, "none") {
		return strings.TrimSpace(existing)
	}
	lines := splitDecisionFacts(existing)
	lines = append(lines, fact)
	lines = boundDecisionFacts(lines)
	return strings.Join(lines, "\n")
}

func splitDecisionFacts(existing string) []string {
	existing = strings.TrimSpace(existing)
	if existing == "" {
		return nil
	}
	parts := strings.Split(existing, "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func boundDecisionFacts(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	for len(out) > decisionsMaxLines {
		out = out[1:]
	}
	for len(out) > 0 && len(strings.Join(out, "\n")) > decisionsMaxBytes {
		if len(out) == 1 {
			line := out[0]
			if len(line) > decisionsMaxBytes {
				out[0] = line[len(line)-decisionsMaxBytes:]
			}
			break
		}
		out = out[1:]
	}
	return out
}

func (s *SmartContextManager) resetTaskStateIfNeeded(state *RunState) {
	if state == nil {
		return
	}
	message, ok := latestUserMessage(state.Lineage.FullMessages())
	if !ok && len(state.Conversation) > 0 {
		message, ok = latestUserMessage(state.Conversation)
	}
	if !ok || !shouldResetTaskState(message.Content) {
		return
	}

	s.scratchpad.Intent = ""
	s.scratchpad.Decisions = ""
	s.scratchpad.Open = ""
	s.scratchpad.Next = ""
	s.scratchpad.WorkingFile = ""
	s.scratchpad.LastAction = ""
	s.scratchpadFailures = 0

	state.Context.ActiveFocus = nil
	state.Context.UnresolvedWork = nil
	state.Context.FileTrackerSummary = nil
	state.Context.RecentToolCalls = nil
}

func latestUserMessage(messages []Message) (Message, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == MessageRoleUser {
			return messages[i], true
		}
	}
	return Message{}, false
}

func shouldResetTaskState(content string) bool {
	switch normalizeDirectiveText(content) {
	case "commit changes", "run tests", "review this", "stop":
		return true
	default:
		return false
	}
}

func normalizeDirectiveText(content string) string {
	content = strings.ToLower(strings.TrimSpace(content))
	content = strings.TrimRight(content, ".!?")
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "please ")
	return strings.TrimSpace(content)
}
