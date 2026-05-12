package agent

import (
	"context"
	"fmt"
	"path/filepath"
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

// MutationRecorder tracks file mutations for context enrichment.
type MutationRecorder interface {
	RecordMutation(path string)
}

// CompactionRecorder records that a compaction event occurred.
type CompactionRecorder interface {
	RecordCompaction(turn int)
}

// EpochResetter resets the masking epoch after compaction.
type EpochResetter interface {
	ResetEpoch(turn int)
}

// EventSinkSetter accepts an event sink for diagnostics emission.
type EventSinkSetter interface {
	SetEventSink(sink output.EventSink)
}

// PreambleProvider returns a cached system preamble string.
type PreambleProvider interface {
	CachedSystemPreamble(override string, scratchpadEnabled bool, delegationEnabled bool) string
}

// ToolResultIngestor processes tool results for context shaping.
type ToolResultIngestor interface {
	ObserveToolResult(turn int, toolName string, input map[string]any, content string) string
}

// AssistantResponseIngestor processes assistant responses for context shaping.
type AssistantResponseIngestor interface {
	IngestAssistantResponse(turn int, content string) (string, string)
}

// ScaffoldInferrer runs scaffold inference to update scratchpad state.
type ScaffoldInferrer interface {
	ShouldRunScaffoldInference(state RunState, compactionCount int) bool
	ApplyScaffoldInference(turn int, content string) (bool, string)
	ScaffoldPromptState() string
}

// CompactionStrategyProvider returns the configured compaction strategy.
type CompactionStrategyProvider interface {
	CompactionStrategy() config.CompactionStrategy
}

// MaskingWindowProvider returns the configured masking window size in turns.
type MaskingWindowProvider interface {
	MaskingWindow() int
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

// SmartContextManager applies ingestion-time shaping to tool output so the
// active conversation starts in a compact, signal-rich form.
type SmartContextManager struct {
	readAnnotations    bool
	configApplied      bool
	compactionStrategy config.CompactionStrategy
	events             output.EventSink
	fileTracker        FileTracker
	scratchpad         ScratchpadManager
	epoch              EpochManager
	// cachedPreamble holds the system preamble built once per session.
	// Both inputs (override string, scratchpadEnabled bool) are session-constants,
	// so building the string once prevents unnecessary allocations and keeps
	// the bytes byte-identical across turns, preserving KV cache hits.
	cachedPreamble string
}

// CachedSystemPreamble returns the system preamble string, building it once
// and caching it for the lifetime of the manager.
func (s *SmartContextManager) CachedSystemPreamble(override string, scratchpadEnabled bool, delegationEnabled bool) string {
	if s.cachedPreamble == "" {
		s.cachedPreamble = prompt.SystemPreamble(override, scratchpadEnabled, delegationEnabled).Content
	}
	return s.cachedPreamble
}

// CompactionStrategy returns the configured compaction strategy.
func (s *SmartContextManager) CompactionStrategy() config.CompactionStrategy {
	return s.compactionStrategy
}

// MaskingWindow returns the configured masking window in turns, defaulting to 5.
func (s *SmartContextManager) MaskingWindow() int {
	return s.epoch.MaskingWindow()
}

// PostIngestion normalizes tool output in the loaded conversation.
func (s *SmartContextManager) PostIngestion(_ context.Context, state RunState) (RunState, error) {
	next := state.Clone()
	next.Conversation = s.normalizeIngestedMessages(next.TurnCount, next.Conversation)
	next.Lineage = newConversationLineage(next.Conversation)
	s.epoch.UpdateMinVisibleTurn(next.Conversation)
	s.epoch.InitializeFromTurnCount(next.TurnCount)
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
	s.epoch.InitializeFromTurnCount(next.TurnCount)
	currentTurn := next.TurnCount + 1
	previousBoundary := s.epoch.MaskBoundary()
	previousStartTurn := s.epoch.epochStartTurn
	trigger := ""
	if s.epoch.ShouldAdvance(currentTurn, next) {
		trigger = s.epoch.Advance(currentTurn)
	}
	window := s.epoch.MaskingWindow()
	masked := maskConversationBeforeTurn(conversation, s.epoch.MaskBoundary())
	s.epoch.emitMaskingDiagnostics(currentTurn, window, previousBoundary, previousStartTurn, trigger, conversation, masked)
	next.Conversation = masked
	next.Lineage = next.Lineage.WithCurrentMessages(masked)
	s.epoch.UpdateMinVisibleTurn(masked)
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
	s.scratchpad.OnTurnComplete(turnIndex, scratchpadCalled)
}

// IngestToolResult shapes a newly produced tool result before it enters the
// active conversation history.
func (s *SmartContextManager) IngestToolResult(turn int, toolName, content string) string {
	return s.observeToolResult(turn, toolName, nil, content)
}

// RecordMutation bumps the in-memory file generation for a successful
// steiner-originated mutation.
func (s *SmartContextManager) RecordMutation(path string) {
	s.fileTracker.RecordMutation(path)
}

// ResetEpoch resets the current masking epoch after compaction so retained
// conversation starts from a clean boundary.
func (s *SmartContextManager) ResetEpoch(turn int) {
	s.fileTracker.PruneBeforeTurn(turn)
	s.epoch.Reset(turn, "compaction")
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
	s.scratchpad.SetEventSink(sink)
	s.epoch.SetEventSink(sink)
}

// NewContextManager constructs the appropriate ContextManager for the given
// mode. An unrecognised mode falls back to NaiveContextManager.
func NewContextManager(mode string, cfg ...config.ContextManagementConfig) ContextManager {
	if mode == "smart" {
		manager := &SmartContextManager{}
		manager.scratchpad.mode = config.ScratchpadModeScaffoldOnly
		if len(cfg) > 0 {
			manager.epoch.maskingWindowTurns = cfg[0].MaskingWindowTurns
			manager.readAnnotations = cfg[0].ReadAnnotations
			manager.compactionStrategy = cfg[0].CompactionStrategy
			if cfg[0].ScratchpadMode != "" {
				manager.scratchpad.mode = cfg[0].ScratchpadMode
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

func (s *SmartContextManager) observeToolResult(turn int, toolName string, input map[string]any, content string) string {
	shaped := tool.ShapeIngestedToolResult(toolName, content)
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "scratchpad":
		return s.scratchpad.IngestToolResult(turn, shaped)
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
				dropped := s.epoch.MinVisibleTurn() > 0 && observation.PreviousRead.LastTurn < s.epoch.MinVisibleTurn()
				masked := s.epoch.MaskBoundary() > 0 && observation.PreviousRead.LastTurn < s.epoch.MaskBoundary()
				if dropped || masked {
					next = shaped
					observation.Action = "full"
					observation.Reason = "previous read no longer visible in context"
				}
			}
		}
		s.emitFileAnnotationDiagnostics(turn, result, observation, shaped, next)
		update, facts := s.fileTracker.ObserveToolResult(turn, toolName, nil, next)
		// Supplement with suppression fact that ObserveToolResult cannot infer from
		// content alone — it requires the full observation computed above.
		if observation.Action == "full" && observation.Reason == "previous read no longer visible in context" {
			path := sanitizeScratchpadPath(result.Path)
			if path != "" {
				facts = append(facts, fmt.Sprintf("read %s: full content (previous read turn %d no longer visible)", path, observation.PreviousRead.LastTurn))
			}
		}
		s.applyFileTrackerUpdate(update, facts)
		return next
	case "edit", "write", "apply_patch":
		update, facts := s.fileTracker.ObserveToolResult(turn, toolName, input, shaped)
		s.applyFileTrackerUpdate(update, facts)
		return shaped
	case "bash":
		update, facts := s.fileTracker.ObserveToolResult(turn, toolName, input, shaped)
		s.applyFileTrackerUpdate(update, facts)
		return shaped
	default:
		update, facts := s.fileTracker.ObserveToolResult(turn, toolName, input, shaped)
		s.applyFileTrackerUpdate(update, facts)
		return shaped
	}
}

func (s *SmartContextManager) applyFileTrackerUpdate(update workingFileUpdate, facts []string) {
	s.scratchpad.SetWorkingFile(update.Path, update.LastAction)
	s.scratchpad.AppendDecisionFacts(facts)
}

func (s *SmartContextManager) appendDecisionFact(fact string) {
	s.scratchpad.AppendDecisionFact(fact)
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

func (s *SmartContextManager) annotationsEnabled() bool {
	if !s.configApplied {
		return true
	}
	return s.readAnnotations
}

func (s *SmartContextManager) enrichContextState(state RunState) ContextState {
	next := state.Context.Clone()
	next.TurnCount = state.TurnCount
	s.scratchpad.SyncState(state, &next)
	next.Scratchpad = s.scratchpad.Render()
	return next
}

// ScaffoldPromptState returns the rendered scratchpad state for scaffold inference.
func (s *SmartContextManager) ScaffoldPromptState() string {
	return s.scratchpad.ScaffoldPromptState()
}

// ShouldRunScaffoldInference reports whether scaffold inference should run for
// the current state and compaction count.
func (s *SmartContextManager) ShouldRunScaffoldInference(state RunState, compactionCount int) bool {
	return s.scratchpad.ShouldRunScaffoldInference(state, compactionCount)
}

// ApplyScaffoldInference parses scaffold inference output and updates the scratchpad.
func (s *SmartContextManager) ApplyScaffoldInference(turn int, content string) (bool, string) {
	return s.scratchpad.ApplyScaffoldInference(turn, content)
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

func shapeIngestedToolResultForContextManager(cm ContextManager, turn int, toolName string, input map[string]any, content string) string {
	if ingestor, ok := cm.(ToolResultIngestor); ok {
		return ingestor.ObserveToolResult(turn, toolName, input, content)
	}
	return content
}

func processAssistantResponseForContextManager(cm ContextManager, turn int, content string) (string, string) {
	if ingestor, ok := cm.(AssistantResponseIngestor); ok {
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
			summary := summarizeCallArguments(call.Name, call.Arguments)
			if summary == "" {
				out = append(out, strings.TrimSpace(call.Name))
				continue
			}
			out = append(out, strings.TrimSpace(call.Name)+" "+summary)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func summarizeCallArguments(toolName string, arguments map[string]any) string {
	if len(arguments) == 0 {
		return ""
	}
	root := summarizeSummaryRoot(arguments)
	parts := make([]string, 0, 3)
	for _, key := range []string{"cwd", "path", "pattern", "command", "offset", "limit"} {
		value, ok := arguments[key]
		if !ok {
			continue
		}
		summary := summarizeCallArgumentValue(toolName, key, value, root)
		if summary == "" {
			continue
		}
		parts = append(parts, key+"="+summary)
	}
	return strings.TrimSpace(strings.Join(parts, ", "))
}

func summarizeSummaryRoot(arguments map[string]any) string {
	if arguments == nil {
		return ""
	}
	cwd, _ := arguments["cwd"].(string)
	cwd = strings.TrimSpace(cwd)
	if filepath.IsAbs(cwd) {
		return cwd
	}
	return ""
}

func summarizeCallArgumentValue(toolName, key string, value any, root string) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	switch key {
	case "cwd", "path":
		return sanitizeScratchpadPathWithRoot(text, root)
	case "command":
		if strings.EqualFold(strings.TrimSpace(toolName), "bash") {
			return summarizeBashCommand(text, root)
		}
		return summarizeTextValue(text)
	case "offset", "limit":
		return summarizeTextValue(text)
	default:
		return summarizeTextValue(text)
	}
}

func summarizeTextValue(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if len(text) <= 40 {
		return text
	}
	return text[:40] + "..."
}

func summarizeBashCommand(command, root string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	command = strings.Join(strings.Fields(command), " ")
	command = stripLeadingCdWrapper(command)
	command = redactRootPrefix(command, root)
	command = strings.TrimSpace(strings.Join(strings.Fields(command), " "))
	if len(command) <= 80 {
		return command
	}
	return command[:80] + "..."
}

func stripLeadingCdWrapper(command string) string {
	trimmed := strings.TrimSpace(command)
	if !strings.HasPrefix(strings.ToLower(trimmed), "cd ") {
		return command
	}
	for _, sep := range []string{" && ", " ; ", " || "} {
		if idx := strings.Index(trimmed, sep); idx >= 0 {
			return strings.TrimSpace(trimmed[idx+len(sep):])
		}
	}
	return command
}

func redactRootPrefix(text, root string) string {
	root = strings.TrimSpace(root)
	if root == "" || text == "" {
		return text
	}
	root = filepath.Clean(root)
	if text == root {
		return "."
	}
	text = strings.ReplaceAll(text, root+string(filepath.Separator), "")
	return strings.ReplaceAll(text, root, "")
}

func sanitizeScratchpadPath(path string) string {
	return sanitizeScratchpadPathWithRoot(path, "")
}

func sanitizeScratchpadPathWithRoot(path, root string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	root = strings.TrimSpace(root)
	if root != "" {
		root = filepath.Clean(root)
		if path == root {
			return "."
		}
		if trimmed := strings.TrimPrefix(path, root+string(filepath.Separator)); trimmed != path {
			return trimmed
		}
	}
	return filepath.Base(path)
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
	s.scratchpad.Reset()
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
