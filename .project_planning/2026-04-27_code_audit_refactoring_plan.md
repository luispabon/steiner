# Code Audit & Refactoring Plan: steiner

## Audit Summary

`steiner` is a 119-file Go codebase (~24 kloc production, ~8 kloc tests) for a local-first TUI coding agent. The project builds cleanly (`go build ./...`, `go vet ./...`) and all 220 tests pass. However, organic growth has produced several files that far exceed maintainable size thresholds—most critically `internal/tui/content.go` (1,426 lines), `cmd/steiner/main.go` (1,173 lines), and `internal/tui/model.go` (1,069 lines). These god files mix unrelated concerns (CLI commands, runtime wiring, rendering, event handling) and contain duplicated helper logic that exists in three or four places across packages. Five packages have zero test coverage, and a number of errors are silently swallowed in production paths. The plan below orders 19 discrete, build-safe steps to structuralise the codebase without changing behaviour.

## Architecture Overview

**Current state:** `cmd/steiner/main.go` has become a dumping ground for commands, runtime, runner, schema construction, and approval logic, violating the project's own package-boundary rules. `internal/tui/content.go` and `model.go` mix Bubble Tea Update/View logic with markdown parsing, tool-call formatting, and layout math. `internal/output/plain.go` and `log.go` similarly mix rendering, event definitions, and logger setup. `internal/config/config.go` and `internal/provider/openai_compat.go` each contain struct definitions, wire formats, I/O, and business logic.

**Target state:**
- `cmd/steiner/` is split into focused files by domain (commands, runtime, runner, tools, approval, interactive, exec).
- `internal/tui/` is split by layer: event ingestion, rendering, input handling, and layout.
- `internal/output/` separates event types, constructors, rendering, and preview formatting.
- `internal/config/` separates config structs, custom types (`Duration`), patch application, and load orchestration.
- `internal/provider/` separates the HTTP client, OpenAI wire types, and SSE stream decoding.
- Shared helpers (deep-clone, text truncation, preview counting) live in one canonical location each.
- Error handling is consistent and no longer silent in critical paths.

## Refactoring Steps

---

### Step 1: Remove dead code and unused exported types

**Category**: convention-cleanup  
**Severity**: medium  
**Files**: `internal/tui/render.go`, `internal/tui/theme/steiner.go`, `internal/tui/theme/theme.go`, `internal/tui/keys.go`, `internal/tui/input.go`, `internal/tui/git.go`, `internal/prompt/types.go`, `internal/prompt/assembler.go`, `internal/agent/text_util.go`, `internal/output/context_report.go`, `cmd/steiner/main.go`  
**Depends on**: none

**Problem**: Several symbols and one entire file serve no purpose. `render.go` is empty. `ptrUint` is defined but never called. `ToolTagGlobGrep` is initialised but never mapped by the tool-tag renderer. `ConversationGenerationKind`, `ConversationGenerationState`, and `ConversationLineageState` are defined but referenced nowhere. `AssemblyDiagnostic` and the `Assembly.Diagnostics` field are always empty. `timeNowUTC` is a trivial wrapper used once. `countMessages` is `return len(messages)`. `interactiveInput` is never called in production. `resolveSelectedModel` is a no-op wrapper around `selectedModelConfig`.

**Action**:
- Delete `internal/tui/render.go`.
- Remove `ptrUint` from `theme/steiner.go`.
- Remove `ToolTagGlobGrep` field from `theme.Styles` and from `buildStylesInternal`.
- Remove `keyMap` struct, `defaultKeyMap`, and `hints()` method from `keys.go`; replace `hints()` usage with a free function or inline the string slice.
- Remove `handled` field from `inputAction` in `input.go` and update `input_test.go` to assert on specific action fields instead.
- Unexport and remove `Branch`, `Dirty`, `RepoRoot`, `Ready`, `Ahead` from `git.go` (zero usages).
- Remove `ConversationGenerationKind`, `ConversationGenerationState`, `ConversationLineageState` from `prompt/types.go`.
- Remove `AssemblyDiagnostic` type and `Diagnostics` field from `Assembly` in `prompt/types.go` and `assembler.go`.
- Inline `timeNowUTC` in `output/context_report.go`.
- Inline `countMessages` in `agent/text_util.go`.
- Remove `interactiveInput` from `cmd/steiner/main.go`.
- Replace all three call sites of `resolveSelectedModel` with `selectedModelConfig`, then delete `resolveSelectedModel`.

**Verification**:
- `go build ./...` passes.
- `go vet ./...` clean.
- `go test ./...` still passes (220 tests).

---

### Step 2: Move external-package tests to internal package and unexport symbols

**Category**: convention-cleanup  
**Severity**: medium  
**Files**: `internal/config/validate.go`, `internal/config/config.go`, `internal/prompt/compaction.go`, `internal/delegation/tool.go`, `internal/delegation/result.go`, `internal/delegation/scaffold.go`, `internal/output/plain.go`, plus associated `_test.go` files  
**Depends on**: Step 1

**Problem**: `Validate`, `NewDuration`, `SummarizeToolMessage`, `DelegateToolName`, `CheckOutputSize`, `ChildContext`, `ScaffoldChildContext`, `BuildChildRunRequest`, `InspectionSnapshot`, and `SummarizeInspection` are exported solely because their tests live in an external test package (`package delegation_test`, etc.). None have cross-package production callers.

**Action**:
- For each affected package, change the test files that reference these symbols from `package foo_test` to `package foo` so they can access unexported symbols.
- Then unexport: `Validate` → `validate`, `NewDuration` → `newDuration`, `SummarizeToolMessage` → `summarizeToolMessage`, `DelegateToolName` → `delegateToolName`, `CheckOutputSize` → `checkOutputSize`, `ChildContext` → `childContext`, `ScaffoldChildContext` → `scaffoldChildContext`, `BuildChildRunRequest` → `buildChildRunRequest`, `InspectionSnapshot` → `inspectionSnapshot`, `SummarizeInspection` → `summarizeInspection`.

**Verification**:
- `go build ./...` passes.
- `go test ./...` passes.

---

### Step 3: Extract shared JSON deep-clone helpers

**Category**: deduplication  
**Severity**: high  
**Files**: new `internal/tool/clone.go`; `cmd/steiner/main.go`; `internal/tool/executor.go`; `internal/tool/schema.go`  
**Depends on**: Step 1

**Problem**: Near-identical `map[string]any` deep-clone logic exists in three places: `cloneInput`/`cloneValue` in `main.go`, `cloneInputMap`/`cloneJSONValue` in `executor.go`, and `cloneSchemaMap`/`cloneSchemaValue` in `schema.go`. Fixes must be applied in multiple places.

**Action**:
- Create `internal/tool/clone.go` containing:
  ```go
  func CloneJSONValue(v any) any
  func CloneJSONMap(m map[string]any) map[string]any
  ```
- Replace the three duplicate implementations with calls to these helpers.
- Delete the old private clone functions from `main.go`, `executor.go`, and `schema.go`.

**Verification**:
- `go build ./...` passes.
- `go test ./internal/tool/...` passes.

---

### Step 4: Extract shared text-truncation and plural helpers

**Category**: deduplication  
**Severity**: medium  
**Files**: new `internal/output/textutil.go`; `internal/agent/text_util.go`; `internal/output/debug.go`; `internal/output/log.go`; `internal/output/context_report.go`  
**Depends on**: none

**Problem**: Four functions implement the same "trim whitespace, truncate to N chars, append `...`" pattern: `summarizeTextPreview` (agent), `truncateDiagnosticText` (debug), `truncatePreview` (log), `previewText` (context_report). Two plural helpers are also identical: `pluralSuffix` (log) and `pluralizeDiagnosticWord` (debug).

**Action**:
- Create `internal/output/textutil.go` with:
  ```go
  func TruncateWithEllipsis(s string, maxLen int) string
  func PluralSuffix(count int, singular, plural string) string
  ```
- Replace all four truncation implementations and both plural implementations with calls to these helpers.
- Delete the old private functions.

**Verification**:
- `go build ./...` passes.
- `go test ./internal/output/...` and `go test ./internal/agent/...` pass.

---

### Step 5: Consolidate preview change counting

**Category**: deduplication  
**Severity**: medium  
**Files**: `internal/output/preview.go` (expand), `internal/tui/content.go`, `internal/output/plain.go`  
**Depends on**: none

**Problem**: `countPreviewChanges` is duplicated verbatim in `internal/tui/content.go` and `internal/output/plain.go`, iterating over `PreviewDocument.Lines` to count additions and removals.

**Action**:
- Move the canonical implementation into `internal/output/preview.go` as an exported function:
  ```go
  func CountPreviewChanges(doc PreviewDocument) (adds, removes int)
  ```
- Replace the duplicate in `internal/tui/content.go` with `output.CountPreviewChanges(...)`.
- Replace the duplicate in `internal/output/plain.go` with a local call.
- Delete the old private copies.

**Verification**:
- `go build ./...` passes.
- `go test ./internal/output/... ./internal/tui/...` pass.

---

### Step 6: Split internal/config/config.go

**Category**: file-split  
**Severity**: critical  
**Files**: `internal/config/config.go` → `config.go`, `duration.go`, `patch.go`, `load.go`  
**Depends on**: Step 2 (unexports `NewDuration`)

**Problem**: `config.go` is 634 lines and mixes the root `Config` struct, a custom `Duration` type with YAML marshaling, a full `configPatch` mirror hierarchy, `apply*Patch` helpers, `Load` orchestration, CLI override logic, and path normalization.

**Action**:
- Create `internal/config/duration.go` and move: `Duration`, `NewDuration`/`newDuration`, `MustDuration`, `Duration()`, `IsZero()`, `String()`, `MarshalYAML()`, `UnmarshalYAML()`.
- Create `internal/config/patch.go` and move: all `*Patch` structs (`configPatch`, `schedulerPatch`, `modelPatch`, `limitsPatch`, `approvalPatch`, `subAgentPatch`, `toolConfigPatch`, `projectContextPatch`, `pathsPatch`, `loggingPatch`) and every `apply*Patch` helper.
- Create `internal/config/load.go` and move: `Load`, `LoadOptions`, `CLIOverrides`, `readConfigPatch`, `applyCLIOverrides`, `normalizePaths`.
- Leave in `config.go`: `Config` and small helpers (`copyStringAnyMap`, `environMap`).

**Verification**:
- `go build ./...` passes.
- `go test ./internal/config/...` passes.

---

### Step 7: Split internal/provider/openai_compat.go

**Category**: file-split  
**Severity**: critical  
**Files**: `internal/provider/openai_compat.go` → `openai_compat.go`, `openai_wire.go`, `openai_stream.go`  
**Depends on**: none

**Problem**: `openai_compat.go` is 596 lines and mixes the HTTP client, OpenAI wire-type hierarchy, request marshaling, response normalization, SSE stream decoding, and tool-call accumulation.

**Action**:
- Create `internal/provider/openai_wire.go` and move: all `openAI*` structs (`openAIRequest`, `openAIMessage`, `openAITool`, etc.), plus `toOpenAIMessage`, `normalizeMessage`, `normalizeToolCalls`, `normalizeChatResponse`, `marshalRequest`.
- Create `internal/provider/openai_stream.go` and move: `readSSEEvent`, `decodeChatStream`, `flushStreamState`, `finalizeToolCalls`, `extractThinkingDelta`, and the stream-consumption goroutine logic.
- Leave in `openai_compat.go`: `OpenAICompat` struct, `NewOpenAICompat`, `ChatCompletion`, `StreamChatCompletion`, `doChatCompletion`, `streamChatCompletion`, `acquire`, `release`.

**Verification**:
- `go build ./...` passes.
- `go test ./internal/provider/...` passes.

---

### Step 8: Split internal/output/log.go and plain.go

**Category**: file-split  
**Severity**: critical  
**Files**: `internal/output/log.go` → `log.go`, `event_types.go`, `event_constructors.go`; `internal/output/plain.go` → `plain.go`, `event_render.go`, `plain_preview.go`  
**Depends on**: Step 4 (removes helpers from plain.go)

**Problem**: `log.go` (598 lines) mixes event type constants, core `Event`/`EventSink` definitions, ~20 event structs, ~20 `New*Event` constructors, and logger setup. `plain.go` (780 lines) mixes `PlainRenderer` I/O locking, a 225-line `renderEvent` switch, preview formatting, theme/ANSI detection, and inspection summarization.

**Action**:
- Create `internal/output/event_types.go` and move: all event type string constants, `Event`, `EventSink`, `SinkFunc`, `NoopSink`, and all event struct definitions (`ModelCallStartedEvent`, `ToolCallFinishedEvent`, etc.).
- Create `internal/output/event_constructors.go` and move: all `New*Event` constructor functions.
- Create `internal/output/event_render.go` and move: `renderEvent`, `FormatEvent`, `stopReasonSummary`, and the per-event formatter branches.
- Create `internal/output/plain_preview.go` and move: preview caption/line helpers (`FormatFilePreview`, `formatEditDiffPreviewWithLimit`, etc.) and inspection summarization (`SummarizeInspection` → `summarizeInspection`).
- Leave in `log.go`: `SetupLogger`, `parseLevel`, and logger-related helpers.
- Leave in `plain.go`: `PlainRenderer` struct and its I/O methods (`Println`, `Printf`, `Render`, `Close`).

**Verification**:
- `go build ./...` passes.
- `go test ./internal/output/...` passes.

---

### Step 9: Split internal/tui/content.go

**Category**: file-split  
**Severity**: critical  
**Files**: `internal/tui/content.go` → `content.go`, `content_events.go`, `content_render.go`, `content_tool.go`, `content_markdown.go`  
**Depends on**: Step 5 (removes `countPreviewChanges` duplication from content.go)

**Problem**: `content.go` is 1,426 lines and contains event ingestion (`AppendEvent`), 10+ segment type definitions, all rendering logic (markdown, tool calls, diff previews, approval pills, compaction banners), markdown parsing helpers, and argument summarization.

**Action**:
- Create `internal/tui/content_events.go` and move: `contentSegmentKind`, `contentSegment`, `contentBuffer`, `AppendEvent`, `AppendLine`, `AppendUser`, `Clear`, `AppendInterrupted`, `finishStreaming`, and all streaming state helpers.
- Create `internal/tui/content_render.go` and move: `renderSegment`, `renderToolCall`, `renderApprovalPill`, `renderCompactionBanner`, `renderMarkdown`, `markdownRenderer`, and all `buildXxxLines` / `renderXxxDocument` helpers.
- Create `internal/tui/content_tool.go` and move: `summarizeArgs`, `previewBodyKind`, `inferBodyKind`, `toolTagStyle`, `toolTagBgHex`, `renderToolBody`, `cloneToolArguments`, `previewDocument`, `previewContentLineCount`.
- Create `internal/tui/content_markdown.go` and move: `nextCompleteMarkdownBlock`, `completeFencedBlockEnd`, `cutFirstLine`, `fenceDelimiter`, `matchesFence`, `isStandaloneMarkdownLine`, `isMarkdownLikeBlock`, `appendMarkdownBlock`.
- Leave in `content.go`: shared constants (`markdownRenderPadding`) and any remaining small helpers such as `shouldSuppressLine`, `formatStopReasonEvent`, `formatApprovalEvent`, `formatDelegationEvent`, `pluralTurns`.

**Verification**:
- `go build ./...` passes.
- `go test ./internal/tui/...` passes.

---

### Step 10: Split internal/tui/model.go

**Category**: file-split  
**Severity**: critical  
**Files**: `internal/tui/model.go` → `model.go`, `model_update.go`, `model_input.go`, `model_events.go`, `model_layout.go`  
**Depends on**: Step 1 (removes dead `maxInt` helper)

**Problem**: `model.go` is 1,069 lines and contains the `Model` struct, constructor, `Init`, `Update`, `View`, layout math, event application, input handling, mouse handling, and numerous small helpers.

**Action**:
- Create `internal/tui/model_update.go` and move: the `Update` method and all `tea.Msg` case handlers (`tickMsg`, `paletteSetAccentMsg`, `historyLoadedMsg`, etc.).
- Create `internal/tui/model_input.go` and move: `handleEnter` and all slash-command dispatch logic (`/model`, `/clear`, `/compact`, etc.).
- Create `internal/tui/model_events.go` and move: `applyEvent` and its payload switch.
- Create `internal/tui/model_layout.go` and move: `layout`, `syncViewport`, `scrollUp`, `scrollDown`, `handleMouse`, `handleLeftClick`.
- Leave in `model.go`: `Model` struct, `newModel`, `Init`, `View`, and small helpers not yet extracted.

**Verification**:
- `go build ./...` passes.
- `go test ./internal/tui/...` passes.

---

### Step 11: Split cmd/steiner/main.go

**Category**: file-split  
**Severity**: critical  
**Files**: `cmd/steiner/main.go` → `main.go`, `commands.go`, `runtime.go`, `runner.go`, `tools.go`, `approval.go`, `interactive.go`, `exec.go`  
**Depends on**: Step 3 (removes clone helpers from main.go)

**Problem**: `main.go` is 1,173 lines and mixes Cobra commands, runtime wiring, agent runner, tool schema construction, model resolution, approval responders, event cloning, and both interactive and exec mode entry points.

**Action**:
- Create `cmd/steiner/commands.go` and move: `newRootCommand`, `newVersionCommand`, `newConfigCommand`, `newToolsCommand`, `newSkillsCommand`.
- Create `cmd/steiner/runtime.go` and move: `cliRuntime`, `cliFlags`, `defaultBuildRuntime`, `buildRuntime` var, `closeRuntime`, `openApprovalInput`, `joinClosers`.
- Create `cmd/steiner/runner.go` and move: `cliRunner`, `runResult`, `cliRunner.Run`, `toProviderConversation`, `cloneEvents`, `cloneEvent`, `isRetainedDiagnosticEvent`.
- Create `cmd/steiner/tools.go` and move: `runtimeRegistry`, `coreToolDefinitions`, `toolTimeout`, `schemaObject`, `requiredStringProperty`, `optionalStringProperty`, `registryToolSpecs`. Also move `schemaObject` and helpers to `internal/tool/schema.go` if they do not depend on CLI state.
- Create `cmd/steiner/approval.go` and move: `channelApprovalResponder`, `stdinApprovalResponder`.
- Create `cmd/steiner/state.go` and move: `interactiveSkills`, `requestSnapshotStore`, `newInteractiveSkills`.
- Create `cmd/steiner/interactive.go` and move: `runInteractiveMode`.
- Create `cmd/steiner/exec.go` and move: `runExecMode`, `readPromptFromInput`, `lastAssistantReply`.
- Create `cmd/steiner/model.go` and move: `selectedModelConfig`, `selectedModelConfigByAlias`, `modelAliasNames`, `modelContextSizes`, `resolveSelectedModel` (already deleted in Step 1, so just the remaining model helpers).
- Leave in `main.go`: `main`, `version`, and package-level vars (`newScheduler`, `newOpenAICompat`).

**Verification**:
- `go build ./...` passes.
- `go test ./cmd/steiner/...` passes.

---

### Step 12: Extract shared model-call execution pattern

**Category**: deduplication  
**Severity**: high  
**Files**: `internal/agent/model_call.go`, `internal/agent/compaction.go`  
**Depends on**: none

**Problem**: `completeModelCall` (model_call.go) and `completeCompactionCall` (compaction.go) are nearly identical: check budget, emit API request event, try streaming, fall back to non-streaming, emit API response event.

**Action**:
- In `internal/agent/model_call.go`, create an unexported helper:
  ```go
  func executeChatRequest(
      ctx context.Context,
      provider provider.Provider,
      turn int,
      req provider.ChatRequest,
      budget prompt.ModelTokenBudget,
      events output.EventSink,
      isCompaction bool,
  ) (provider.ChatResponse, error)
  ```
- Replace the bodies of `completeModelCall` and `completeCompactionCall` with calls to this helper, passing the appropriate event types and budget function (`FitRequest` vs `FitCompactionRequest`).

**Verification**:
- `go test ./internal/agent/...` passes.

---

### Step 13: Add Registry.ToProviderSpecs and deduplicate conversion

**Category**: deduplication  
**Severity**: high  
**Files**: `internal/tool/registry.go`, `cmd/steiner/runner.go` (after Step 11), `internal/delegation/scaffold.go`  
**Depends on**: Step 11

**Problem**: `registryToolSpecs` in `cmd/steiner/main.go` and `childProviderTools` in `internal/delegation/scaffold.go` both iterate `tool.Registry.Definitions()` to build `[]provider.ToolSpec`. One deep-clones the schema map; the other does not, creating inconsistency.

**Action**:
- Add to `internal/tool/registry.go`:
  ```go
  func (r *Registry) ToProviderSpecs() []provider.ToolSpec
  ```
  Implement it with consistent schema cloning using the shared clone helpers from Step 3.
- Replace `registryToolSpecs` in `cmd/steiner/runner.go` with `rt.registry.ToProviderSpecs()`.
- Replace `childProviderTools` in `internal/delegation/scaffold.go` with `registry.ToProviderSpecs()`.
- Delete the old duplicate functions.

**Verification**:
- `go build ./...` passes.
- `go test ./internal/tool/... ./internal/delegation/...` pass.

---

### Step 14: Extract LastAssistantMessage helper

**Category**: deduplication  
**Severity**: medium  
**Files**: `internal/agent/message_convert.go` (or new file), `cmd/steiner/exec.go` (after Step 11), `internal/delegation/result.go`, `internal/delegation/task.go`  
**Depends on**: Step 11

**Problem**: Four sites implement an inline loop to find the last assistant message in a conversation slice.

**Action**:
- Add to `internal/agent/message_convert.go`:
  ```go
  func LastAssistantMessage(msgs []provider.Message) (provider.Message, bool)
  ```
- Replace the inline loops in `cmd/steiner/exec.go` (`lastAssistantReply`), `internal/delegation/result.go` (`BuildResult`), and `internal/delegation/task.go` (`SpawnDelegate`, `lastAssistantMessage`) with calls to this helper.
- Delete the old private functions/loops.

**Verification**:
- `go build ./...` passes.
- `go test ./internal/agent/... ./internal/delegation/... ./cmd/steiner/...` pass.

---

### Step 15: Extract complex functions in agent runner

**Category**: function-extraction  
**Severity**: high  
**Files**: `internal/agent/runner.go`  
**Depends on**: Step 12 (shared model-call helper stabilises runner dependencies)

**Problem**: `Runner.Run` is ~170 lines with a `for` loop containing 5+ error branches, compaction, model calls, and tool execution. A 6-line cancellation-check + error-return block is copy-pasted five times.

**Action**:
- Extract `runTurn(ctx, req, state, basePrompt) (RunState, error)` containing one turn iteration.
- Extract `handleModelResponse(ctx, req, state, response) (RunState, error)` to process the model's reply and dispatch tool calls.
- Extract `executeToolCalls(ctx, req, state, calls) (RunState, error)` to run the tool executor and append results.
- Extract `handleRunError(ctx, events, state, err) (RunState, error)` containing the repeated block:
  ```go
  if cancelled, ok := contextCancellationState(ctx, state); ok {
      emitStop(events, cancelled, nil)
      return cancelled, nil
  }
  state.StopReason = StopReasonError
  emitStop(events, state, err)
  return state, err
  ```
- Replace all five copies in `Run` with `handleRunError(...)`.

**Verification**:
- `go test ./internal/agent/... -run TestRunner` passes.

---

### Step 16: Extract complex functions in TUI model and content

**Category**: function-extraction  
**Severity**: high  
**Files**: `internal/tui/model_update.go`, `internal/tui/model_input.go`, `internal/tui/content_events.go` (after Steps 9–10)  
**Depends on**: Steps 9, 10

**Problem**: `Update` is a 213-line switch over `tea.Msg` types. `handleEnter` is a 145-line switch over input actions. `AppendEvent` is a 194-line switch over event types. `renderSegment` is a 79-line switch over segment kinds. Each case contains inline logic that obscures the control flow.

**Action**:
- In `model_update.go`, extract each `tea.Msg` case into a dedicated method: `handleTickMsg`, `handleKeyMsg`, `handleWindowSizeMsg`, `handlePaletteToggleThinkingMsg`, etc.
- In `model_input.go`, extract each slash-command branch into `executeClearAction`, `executeCompactAction`, `executeModelAction`, `executeSubmitAction`, etc.
- In `content_events.go`, extract each event-type branch into `appendModelCallStartedEvent`, `appendToolCallStartedEvent`, `appendAssistantChunkEvent`, etc.
- In `content_render.go`, extract each segment-kind case into `renderAssistantSegment`, `renderToolCallSegment`, `renderApprovalPillSegment`, etc.

**Verification**:
- `go build ./...` passes.
- `go test ./internal/tui/...` passes.

---

### Step 17: Normalize error handling in critical paths

**Category**: error-handling  
**Severity**: high  
**Files**: `internal/agent/runner.go`, `internal/agent/model_call.go`, `internal/output/file_log.go`, `internal/history/writer.go`, `cmd/steiner/interactive.go` (after Step 11), `cmd/steiner/runtime.go` (after Step 11), `internal/provider/openai_compat.go`, `internal/tui/content.go` / `content_render.go`  
**Depends on**: Steps 8, 11

**Problem**: Errors are silently swallowed in multiple production paths: `json.Marshal` in `formatToolError`, token estimation in `tokenCount`, every write in `file_log.go`, `Seek` in `history/writer.go`, `historyWriter.Load()` in `main.go`, `io.ReadAll` in `readErrorResponse`, git subprocess failures in `git.go`, and glamour render failures in `content.go`.

**Action**:
- In `internal/agent/runner.go` (`formatToolError`): check `json.Marshal` error and return a safe fallback string (`{"ok":false,"error":"marshal failed"}`) instead of ignoring it.
- In `internal/agent/model_call.go` (`tokenCount`): return the error from `provider.EstimateChatRequestTokens` instead of silently returning `0`. Update callers to log or tolerate the error.
- In `internal/output/file_log.go`: change `_, _ = fmt.Fprintf(...)` to check errors and at least log them (or make the internal writer return error to its caller).
- In `internal/history/writer.go` (`TrimAfterAppend`): check and return `w.file.Seek` errors.
- In `cmd/steiner/interactive.go`: do not discard `historyWriter.Load()` errors; log them through the configured logger or event sink.
- In `cmd/steiner/runtime.go` (`closeRuntime`): check and log `historyWriter.Close()` and `rt.close()` errors.
- In `internal/provider/openai_compat.go` (`readErrorResponse`): capture `io.ReadAll` error and wrap it.
- In `internal/provider/openai_compat.go` (`StreamChatCompletion`): do not drop the original error type when converting to string; wrap with `errors.New(errText)` or return a typed error.
- In `internal/tui/content.go` / `content_render.go`: log glamour render errors through a logger instead of silently falling back to plain text.
- In `internal/tui/git.go`: log git command errors instead of silently swallowing them.
- Standardise on the wrapping pattern: `fmt.Errorf("<lowercase verb phrase describing action>: %w", err)`.

**Verification**:
- `go build ./...` passes.
- `go vet ./...` clean.
- `go test ./...` passes.

---

### Step 18: Final convention cleanup

**Category**: convention-cleanup  
**Severity**: low  
**Files**: Multiple across codebase  
**Depends on**: Steps 1–17

**Problem**: Small convention drifts remain: `maxInt` shadows Go 1.21 built-in `max`; octal literals use both `0755` and `0o755`; `cliRuntime` has a field named `close` that shadows the builtin; missing godoc on heavily-used exported constructors; inconsistent receiver names.

**Action**:
- Replace all `maxInt(...)` calls with `max(...)`, delete `maxInt`.
- Standardise all octal file-mode literals to `0o` prefix (`0o755`, `0o644`).
- Rename `cliRuntime.close` to `cliRuntime.closeFn` or `cliRuntime.cleanup`.
- Add brief godoc comments to exported constructors missing them: `config.Load`, `config.Validate` (if re-exported), `prompt.Assemble`, `prompt.GatherProjectContext`, `output.BuildContextReport`, `output.NewContextReportEvent`, and the `New*Event` family.
- Ensure receiver names are 1–2 letters and consistent within each type.
- Run `gofmt -w` on all modified files.

**Verification**:
- `go build ./...` passes.
- `go vet ./...` clean.
- `go test ./...` passes.

---

### Step 19: Update AGENTS.md

**Category**: agents-md-update  
**Severity**: medium  
**Files**: `AGENTS.md`  
**Depends on**: Steps 1–18

**Problem**: The existing `AGENTS.md` is 81 lines and omits several sections from the standards: file size guidance, concrete error-handling pattern, import grouping, interface ownership, testing expectations for zero-coverage packages, and a change-workflow section.

**Action**:
- Add a **File organisation** section:
  - "Keep production `.go` files under 300 lines; split at 500 lines."
  - "Name files with snake_case (`foo_bar.go`), not camelCase."
  - "Place tests alongside source (`foo_test.go` next to `foo.go`)."
- Add a **Code style → Error handling** subsection:
  - "Wrap every error with `fmt.Errorf("<action>: %w", err)` using a lowercase verb phrase."
  - "Never swallow errors with `_ = someFunc()` in production paths."
  - "Use sentinel errors sparingly; prefer wrapped errors."
- Add a **Code style → Imports** subsection:
  - "Group imports: stdlib, blank-line, third-party, blank-line, internal (`github.com/luispabon/steiner/...`)."
- Add an **Architecture boundaries → Interface ownership** subsection:
  - "Interfaces are defined by the consumer, not the implementor."
  - "Keep interfaces small (1–3 methods). Avoid header interfaces."
- Expand **Testing expectations**:
  - "Every package under `internal/` must have tests. Currently `history`, `tui/theme`, and `tui/prefs` are gaps."
  - "Favour table-driven tests. Thread `context.Context` through testable helpers."
- Expand **Change workflow**:
  - "Each change must leave `go build ./...` and `go test ./...` passing."
  - "Run `gofmt -w` before finishing any edit."
  - "Prefer `edit` for in-place mutations."
- Add a **Package guidance** note:
  - "Do not create `util`, `helper`, or `common` packages. Place shared helpers in the package that owns the domain (e.g. JSON cloning lives in `internal/tool`)."

**Verification**:
- Read `AGENTS.md` and verify it is under 200 lines and every rule is actionable.

---

## AGENTS.md Recommendations

### Missing sections to add

| Section | What to add |
|---------|-------------|
| **File organisation** | Max 300-line guidance, 500-line split threshold, snake_case filenames, test placement. |
| **Error handling pattern** | `fmt.Errorf("action: %w", err)` wrapping rule; no silent swallowing; sentinel errors only when necessary. |
| **Import grouping** | Stdlib → third-party → internal, separated by blank lines. |
| **Interface ownership** | Consumer defines interfaces; keep them small; no header interfaces. |
| **Testing expectations** | List packages that currently lack tests (`history`, `tui/theme`, `tui/prefs`, `cmd/steiner-core-tools`) as gaps to close. |
| **Change workflow** | Build and test must pass after every change; `gofmt -w` before finishing; one concern per commit. |
| **Anti-patterns** | No `util`/`helper`/`common` packages; no test-induced exports (unexport and use internal tests). |

### Existing rules to tighten

| Rule | Current wording | Tightened wording |
|------|-----------------|-------------------|
| Error handling | "Return errors instead of panicking..." | "Return errors instead of panicking. Wrap every error with `fmt.Errorf("<action>: %w", err)` using a lowercase verb phrase. Never swallow errors with `_ = fn()` in production paths." |
| Package boundaries | "Keep package boundaries intact..." | "Keep package boundaries intact. `cmd/` may import `internal/`; `internal/tui` may import `internal/output`; `internal/agent` must not import `internal/tui`. Interfaces are defined by the consumer." |
| Working loop | "Prefer targeted tests... then broaden..." | "Prefer targeted tests for changed packages, then `go test ./...`. Every package under `internal/` must have tests." |

### Conventions to encode based on this audit

- **File size**: 300 warn, 500 split.
- **Octal literals**: use `0o` prefix.
- **Builtin identifiers**: do not shadow `close`, `max`, `min`.
- **Unexported-by-default**: if a symbol has no cross-package consumers, unexport it. Move tests to the internal package if needed.
- **Godoc**: every exported symbol must have a comment starting with its name.

---

## Notes

### Risks and caveats

1. **Test-package migration (Step 2)** — Moving tests from `package delegation_test` to `package delegation` is safe for compilation but changes test scope. Ensure no test was relying on black-box access patterns that would break when moved to the same package.
2. **TUI file splits (Steps 9–10)** — `content.go` and `model.go` are heavily interdependent through unexported types (`contentBuffer`, `Model`). Splitting them into files in the *same package* preserves access and is safe; do not move these types to a new subpackage or you will break internal method access.
3. **main.go split (Step 11)** — `cmd/steiner` is a `package main`. Splitting into multiple files in the same package is trivially safe, but take care with package-level vars (`buildRuntime`, `newScheduler`, `newOpenAICompat`) that are mutated by tests. Keep those in `main.go` or `runtime.go` and ensure test setup still reaches them.
4. **Error-handling changes (Step 17)** — Some swallowed errors were intentionally silent (e.g. git failures when not in a repo). Changing these to log errors is safe but may increase log noise. Verify that the logger is configured before these code paths run.
5. **Bubble Tea Update extraction (Step 16)** — Extracting `tea.Msg` handlers into methods is structurally safe, but be careful with named return values or deferred mutations inside `Update`. Preserve the exact control flow (return values, `tea.Cmd` slices) to avoid TUI regressions.
6. **Shared helpers (Steps 3–5)** — Creating new files in existing packages (`internal/tool/clone.go`, `internal/output/textutil.go`) is preferred over new packages. The project's `AGENTS.md` already warns against `util`/`helper` grab-bags; keep helpers in the domain package that owns them.

### Follow-up work not in this plan

- **Add tests to zero-coverage packages**: `internal/history`, `cmd/steiner-core-tools`, `internal/tui/theme`, `internal/tui/prefs`. These are flagged but not planned here.
- **Config patch generic helper**: The `apply*Patch` helpers in `config/patch.go` are ~120 lines of identical `if patch.Field != nil { dst.Field = *patch.Field }` boilerplate. A generic helper or reflection-based approach could collapse this further, but it is lower priority than the file splits.
- **Provider HTTP deduplication**: `doChatCompletion` and `streamChatCompletion` still share body marshaling and header setup after Step 7. A shared `postChatCompletions` helper is possible but touches the critical provider path; defer until the file split is stable.
- **Core-tools path validation**: `cmd/steiner-core-tools/write.go` and `read.go` lack path validation. This is a security gap if the binary is invoked directly. Centralise validation in `internal/tool/policy.go` and import it from core tools, or ensure the executor is always the entry point.
