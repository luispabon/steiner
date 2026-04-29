# Rigmarole TUI Fixes Detailed Staged Plan

## Summary
This plan breaks the requested TUI fixes into implementation stages that can later be dispatched to low-cost subagents with minimal ambiguity. The stages are ordered by dependency so each worker can own a bounded slice of the codebase without redesigning the solution.

The target end state is:
- tool approvals remain inside the Bubble Tea UI and appear near the composer
- exit confirmation uses an in-app modal overlay
- context reports render as markdown and no longer clear the base screen
- `display_file` stops using a separate overlay and instead renders as a normal tool-call card with syntax-highlighted file preview, expanded by default
- `display_file` removes the `language` argument and accepts optional `offset` / `limit`

## Locked Decisions
- Approval UI will be TUI-native, not `huh`-driven.
- Approval UX will be an inline approval tray near the prompt, not a centered modal.
- Exit confirmation will be a centered modal overlay.
- Context overlay remains an overlay, but must render over the existing base screen instead of replacing it.
- `display_file` remains a built-in tool named `display_file`.
- `display_file` accepts `offset` and `limit`, not `start` / `end`.
- `offset` follows the repo's existing read semantics: 1-based, default `1`.
- `display_file` must not leak file contents into the model-visible tool result or conversation history.

## Cross-Cutting Constraints
- Keep package boundaries intact:
  - `cmd/steiner` owns interactive runtime wiring and approval responder plumbing.
  - `internal/tui` owns UI state, rendering, focus, and interaction.
  - `internal/tool/builtin` owns the built-in tool API and file-reading behavior.
  - `internal/output` owns structured preview formats.
  - `internal/agent` owns event emission and tool-result-to-conversation handling.
- Avoid broad redesign of unrelated TUI patterns.
- Preserve existing non-interactive approval behavior (`stdinApprovalResponder`) and existing tool preview behavior for tools other than `display_file`.
- Do not create a second general-purpose modal system if the existing `OverlayShell` can be extended cleanly.

## Stage Overview
1. Approval flow architecture and TUI state refactor
2. Exit modal and non-destructive centered overlay composition
3. Context overlay markdown rendering and overlay composition migration
4. `display_file` tool API and preview pipeline refactor
5. `display_file` transcript rendering and TUI cleanup
6. Final verification and regression sweep

## Stage 1: Approval Flow Architecture And TUI State Refactor

### Goal
Remove `huh` from interactive tool approval handling and replace the current free-form textarea approval mode with a visible, structured approval tray anchored above the composer.

### Primary Ownership
- `cmd/steiner/approval.go`
- `cmd/steiner/interactive.go`
- `internal/tui/app.go`
- `internal/tui/model.go`
- `internal/tui/model_update.go`
- `internal/tui/model_input.go`
- new focused TUI helper file if needed, e.g. `internal/tui/approval_tray.go`

### Current State To Replace
- Interactive approvals are handled by `huhApprovalResponder` in `cmd/steiner/approval.go`.
- The responder pauses Bubble Tea terminal ownership with `ReleaseTerminal` / `RestoreTerminal`.
- The TUI has `approvalState`, but approval interaction is still basically textarea input plus `y/n` parsing via `executeApprovalAction`.
- Approval visibility is weak because the actionable UI is not rendered as a dedicated component near the composer.

### Implementation Work
- Replace `huhApprovalResponder` with a TUI-backed responder that:
  - blocks synchronously on a response channel supplied by the TUI
  - preserves the session-local `always allow` cache keyed by tool name
  - returns `tool.ApprovalResponse` values for `allow once`, `always allow`, and `deny`
- Keep `stdinApprovalResponder` unchanged for exec/non-TUI mode.
- Extend `tui.Config` to carry a richer approval callback or response sink.
  - Replace the current `OnApproval func(bool)` with a callback shape that can distinguish:
    - allow once
    - always allow
    - deny
  - Recommended shape: a function that accepts an enum-like decision type or a richer struct.
- Expand `approvalState` so it is no longer just passive metadata.
  - Add selected action index or explicit selected decision.
  - Add any view-specific text derived from preview truncation if needed.
  - Keep the displayed preview string sourced from the approval event payload.
- Add an approval tray renderer in `internal/tui`.
  - Position it just above the input/status region.
  - Show:
    - tool name
    - approval mode label if useful
    - preview summary
    - action choices: `Allow once`, `Always allow`, `Deny`
  - Make it visually distinct from the transcript and sidebar.
  - Keep it readable at narrow widths by wrapping preview text and collapsing chrome before collapsing actions.
- Update keyboard routing while approval is active.
  - Approval tray should consume left/right or up/down movement plus `Tab` if convenient.
  - `Enter` confirms the selected decision.
  - `Esc` denies the request.
  - While approval is active:
    - textarea editing should not be the source of approval input
    - non-approval run input remains blocked as it is now
- Remove or bypass the current `approve> ` textarea prompt mode.
  - The textarea should remain visually present but not become the approval input surface.
  - Reset prompt/placeholder back to normal after the approval resolves.
- Keep approval transcript semantics stable where useful.
  - Continue showing approval requested/resolved information in the transcript via approval pill/events unless that conflicts with the new UI.
  - The new tray is the active control surface; the transcript remains historical context only.

### Acceptance Criteria
- Triggering a tool approval never exposes the plain terminal or hides the main steiner window.
- The approval UI appears near the composer and is immediately actionable without typing `yes` or `no`.
- `Always allow` applies only for the current session and only to the selected tool name.
- Cancelling with `Esc` denies the approval.
- Existing non-interactive approval behavior still works.

### Worker Notes For Cheap Subagents
- Worker can stay inside the approval/runtime seam plus TUI key handling.
- Worker must not touch `display_file` or overlay composition in this stage.
- Worker should avoid redesigning transcript rendering beyond what is necessary for approval UI behavior.

### Suggested Verification
- `go test ./cmd/steiner ./internal/tui -run Approval`
- add/update unit tests for:
  - TUI approval selection and confirmation
  - `always allow` cache behavior
  - denial via `Esc`
  - no dependency on `huh` terminal handoff during approval path

## Stage 2: Exit Modal And Non-Destructive Centered Overlay Composition

### Goal
Keep exit confirmation inside the TUI and introduce a reusable centered overlay composition path that preserves the underlying screen instead of replacing it with whitespace.

### Primary Ownership
- `internal/tui/overlay.go`
- `internal/tui/model.go`
- `internal/tui/model_update.go`
- `cmd/steiner/interactive.go`
- new focused modal file if useful, e.g. `internal/tui/exit_modal.go`

### Current State To Replace
- Exit confirmation is handled by `runHuhExitConfirmForm` in `cmd/steiner/huh_form.go`.
- The current centered overlay rendering for context/file viewer uses `lipgloss.Place(..., WithWhitespaceChars(" "))`, which clears the screen underneath.

### Implementation Work
- Add a reusable centered overlay compositor to `OverlayShell` or a nearby TUI helper.
  - Inputs:
    - base rendered screen
    - overlay rendered box
    - current terminal dimensions
  - Behavior:
    - overlay is centered
    - existing underlying text remains visible outside overlay bounds
    - overlay rows replace only the intersecting rectangular area
  - Prefer implementing this similarly to `PlaceBottomAnchored`, but centered.
- Add explicit exit modal state to the TUI model.
  - Recommended state:
    - `open bool`
    - `selected int` or `confirm bool`
  - Actions:
    - `Exit`
    - `Cancel`
- Update key routing precedence.
  - Exit modal should take focus above approval tray, context overlay, file picker, and normal input.
  - `Ctrl+C` / `Ctrl+D` when idle should open the modal instead of invoking `huh`.
  - `Esc` while modal is open should cancel and close it.
  - `Enter` confirms the selected modal action.
- Replace the `runHuhExitConfirmForm` flow in `cmd/steiner/interactive.go`.
  - `OnExitRequested` should continue notifying the TUI.
  - The TUI should send back a confirm/cancel result to interactive runtime, or runtime should observe a modal-confirm event via callback.
  - Remove dependence on `tea.Program.ReleaseTerminal` / `RestoreTerminal` for exit confirmation.
- Keep actual application exit behavior unchanged once confirmed:
  - `teaProgram.Quit()`
  - return from `runInteractiveMode`

### Acceptance Criteria
- Idle `Ctrl+C` / `Ctrl+D` opens an in-app modal rather than switching to a plain terminal prompt.
- The modal is centered and does not erase the rest of the screen.
- Confirming exit quits exactly as before.
- Cancelling exit returns to the idle TUI with no residual modal state.

### Worker Notes For Cheap Subagents
- Worker should not implement context markdown or `display_file`.
- Worker can reuse overlay shell and model routing patterns already present for file list and context overlay.

### Suggested Verification
- `go test ./internal/tui ./cmd/steiner -run Exit`
- add/update tests for:
  - idle exit request opens modal
  - confirm path quits
  - cancel path closes modal
  - rendered view still contains transcript/sidebar markers underneath modal

## Stage 3: Context Overlay Markdown Rendering And Overlay Composition Migration

### Goal
Make the context overlay render markdown and stop clearing the screen underneath.

### Primary Ownership
- `internal/tui/context_overlay.go`
- `internal/tui/model.go`
- `internal/tui/model_update.go`
- `internal/tui/model_test.go`

### Current State To Replace
- `renderContextOverlay` splits raw text into lines and renders it as plain text.
- Context overlay is shown through `lipgloss.Place(..., WithWhitespaceChars(" "))`, which blanks the base view.

### Implementation Work
- Migrate context overlay rendering to the same centered overlay compositing helper added in Stage 2.
- Replace manual line rendering in `renderContextOverlay` with markdown rendering.
  - Use the existing `contentBuffer.renderMarkdown` path or extract a shared markdown rendering helper if direct reuse is awkward.
  - Keep overlay-local scroll behavior.
- Decide scroll model against rendered markdown, not source raw line count.
  - Minimum acceptable implementation:
    - render markdown to text at current inner width
    - split rendered output into visual lines
    - maintain `scrollOffset` against those rendered lines
  - Avoid scrolling against pre-render source markdown lines because wrapping would make the overlay jump inconsistently.
- Update `contextOverlayState`.
  - Replace `content`, `lineCount` assumptions if necessary with cached rendered lines or render-on-demand logic.
  - Recompute wrapped markdown when width changes.
- Keep current interaction contract:
  - open on context report event
  - close on `Esc`
  - scroll on arrow keys / page keys already supported by overlay handling

### Acceptance Criteria
- Markdown formatting in context reports is visibly rendered, including headings, lists, code fences, and inline code.
- Opening the context overlay leaves the underlying transcript/sidebar visible outside the overlay box.
- Scrolling remains stable after width changes and on long reports.

### Worker Notes For Cheap Subagents
- Worker should reuse the Stage 2 overlay compositor rather than inventing another placement path.
- Worker should not modify tool preview rendering in this stage.

### Suggested Verification
- `go test ./internal/tui -run Context`
- add/update tests for:
  - markdown content appears rendered
  - overlay close on `Esc`
  - base screen text survives while overlay is open

## Stage 4: `display_file` Tool API And Preview Pipeline Refactor

### Goal
Refactor `display_file` so it no longer drives a dedicated overlay and instead produces structured preview data through the normal tool-call event pipeline, while keeping file contents out of model-visible tool results.

### Primary Ownership
- `internal/tool/builtin/display_file.go`
- `internal/tool/builtin/input.go`
- `internal/tool/builtin/schema.go`
- `internal/tool/output.go`
- `internal/agent/tool_result.go`
- `internal/agent/runner.go`
- `internal/output/event_types.go`
- `internal/output/event_constructors.go`
- `internal/output/preview.go`

### Current State To Replace
- `display_file` emits `output.NewDisplayFileEvent(absPath, in.Language)` directly to the TUI.
- The TUI opens a dedicated file-viewer overlay and reads the file from disk itself.
- `DisplayFileInput` still contains `Language`.
- The regular tool preview pipeline derives preview from model-visible result text, which cannot be used for `display_file` because contents must remain hidden from the model.

### Implementation Work
- Change `DisplayFileInput`.
  - Remove `Language`.
  - Add:
    - `Offset int \`json:"offset,omitempty"\``
    - `Limit int \`json:"limit,omitempty"\``
- Add a `NormalizeDisplayFile` helper near the other input normalizers.
  - `offset <= 0` becomes `1`
  - `limit <= 0` becomes a default line window
  - cap `limit` to a fixed max to keep preview bounded
  - use a dedicated default/max for `display_file`; do not silently reuse `read` constants unless that produces the intended UX
- Update `DisplayFileSchema`.
  - remove `language`
  - add `offset`
  - add `limit`
  - keep `additionalProperties: false`
- Refactor `display_file` handler behavior.
  - Stop emitting `DisplayFileEvent`.
  - Validate and resolve path as today.
  - Read the file in the tool handler, because this is now the only place that can safely create preview data while preserving model/result separation.
  - Slice to the requested line window before preview generation.
  - Build a structured preview from:
    - display path
    - sliced contents
    - inferred syntax
  - Return:
    - model-visible metadata result only
    - UI-only preview via an extended execution result structure
- Extend tool-result transport.
  - Recommended change:
    - add `Preview any` or `Preview output.ToolPreview` to `tool.ExecutionResult`
    - propagate it through `agent.normalizeToolResult`
    - let `Runner.executeToolCalls` prefer explicit preview from the normalized result before falling back to `output.BuildToolPreview(...)`
  - Keep fallback preview generation for all existing tools unchanged.
- Extend `output.ToolPreview` as needed for `display_file`.
  - Preferred approach: reuse `ToolPreviewKindReadFile` or introduce a dedicated `ToolPreviewKindDisplayFile`.
  - Recommended choice: introduce `ToolPreviewKindDisplayFile` so the TUI can treat it differently, especially for default-expanded rendering and captions.
  - Fields needed:
    - `Path`
    - `Language`
    - `Contents`
    - optional range metadata such as `Offset` and `Returned`
- Update preview format builders if needed so syntax is inferred from path and sliced contents.
- Remove dead event path once replacement is in place:
  - `output.NewDisplayFileEvent`
  - `EventTypeDisplayFile`
  - `DisplayFilePayload`
  - any tests or docs tied only to the overlay flow

### Acceptance Criteria
- Calling `display_file` no longer emits a dedicated overlay event.
- The model-visible tool result contains only metadata, not file contents.
- The tool event carries enough structured preview data for the TUI to render the file inline.
- `display_file` accepts `offset` and `limit` and uses them to limit previewed lines.
- Syntax highlighting is inferred without any `language` argument.

### Worker Notes For Cheap Subagents
- Worker should stay in builtin/output/agent pipeline files and avoid TUI rendering changes beyond what is necessary to keep tests compiling.
- Worker must not reintroduce file contents into tool conversation messages.
- Worker should leave actual inline rendering behavior for Stage 5.

### Suggested Verification
- `go test ./internal/tool/builtin ./internal/output ./internal/agent -run DisplayFile`
- add/update tests for:
  - schema shape
  - normalization of `offset` / `limit`
  - bounded preview generation
  - explicit preview propagation through tool execution and event emission
  - absence of file contents in tool result content

## Stage 5: `display_file` Transcript Rendering And TUI Cleanup

### Goal
Render `display_file` as a normal tool-call card in the transcript, expanded by default, with syntax-highlighted file preview, and remove obsolete overlay viewer code.

### Primary Ownership
- `internal/tui/content_events.go`
- `internal/tui/content_tool.go`
- `internal/tui/content_render.go`
- `internal/tui/model.go`
- `internal/tui/model_update.go`
- `internal/tui/file_viewer.go`
- related TUI tests

### Current State To Replace
- `EventTypeDisplayFile` opens `fileViewer`.
- `fileViewer` is a separate overlay with its own state, scrolling, and rendering.
- Tool call segments default to `collapsed: true`.
- The TUI currently recomputes preview from raw result even when `payload.Preview` exists.

### Implementation Work
- Remove `fileViewer` state and event handling.
  - Delete or retire `fileViewerState`.
  - Remove `m.fileViewer` from `Model`.
  - Remove `renderFileViewer`, `handleFileViewerKey`, `openFileViewer`, and related event branches.
- Update `applyEvent` in `internal/tui/model_events.go`.
  - Stop treating display-file as a special overlay-opening event.
  - Let the normal tool started/finished events drive transcript rendering.
- Update tool preview consumption in `contentBuffer.appendToolCallFinishedEvent`.
  - If `payload.Preview.Kind` is non-empty and non-plain, use it directly.
  - Only fall back to `output.BuildToolPreview(...)` when no explicit preview was supplied.
- Make `display_file` default-expanded.
  - In `appendToolCallStartedEvent`, set `collapsed` false when tool is `display_file`.
  - Preserve current default collapsed behavior for other tools.
- Render `display_file` using the existing file-preview rendering path.
  - Either map it to `bodyKind == "file"` through `previewBodyKind`, or add an explicit `display_file` branch that still renders through file preview helpers.
  - Caption should clearly distinguish it from `read` and `write`.
  - Suggested caption format:
    - `<path> · display file preview · N lines`
    - optionally include line window when non-default, e.g. `lines 40-120`
- Keep transcript collapse/expand behavior consistent after the default-open initial state.
  - User should still be able to collapse it later if normal tool cards support that interaction.
- Remove overlay-specific tests and replace them with transcript rendering tests.

### Acceptance Criteria
- `display_file` appears in the transcript like a tool call, not as an overlay.
- It is expanded by default on first render.
- It uses syntax-highlighted file preview rendering.
- Other tools keep their current rendering behavior and default collapse state.
- No file-viewer overlay code remains on the active path.

### Worker Notes For Cheap Subagents
- Worker should not alter approval tray or exit modal behavior.
- Worker can delete dead file-viewer code once transcript rendering path is confirmed.

### Suggested Verification
- `go test ./internal/tui -run 'DisplayFile|ToolPreview|FileViewer'`
- add/update tests for:
  - explicit preview beats recomputed preview
  - `display_file` segment starts expanded
  - rendered transcript includes expected caption and highlighted content
  - no overlay opens on display-file events because that event path no longer exists

## Stage 6: Final Verification And Regression Sweep

### Goal
Run cross-stage cleanup, ensure dead code is removed, and confirm no regressions in existing TUI and tool flows.

### Primary Ownership
- whichever worker or reviewer performs final integration

### Implementation Work
- Remove obsolete `huh` approval and exit-confirm usage from the interactive path.
  - It is acceptable to leave low-level helper files temporarily only if they are unused and harmless, but preferred end state is to remove dead interactive `huh` form code and references.
- Remove obsolete display-file overlay event/types/tests.
- Re-run formatting on all touched Go files.
- Run targeted tests first, then broaden to full suite.
- Review for boundary leaks:
  - no TUI-only preview logic in `internal/tool`
  - no model-visible file contents from `display_file`
  - no terminal release/restore in interactive approval/exit flow

### Acceptance Criteria
- All requested bugs are covered by code changes and tests.
- No obsolete overlay/event code remains on live paths.
- Full Go test suite passes, or remaining failures are unrelated and explicitly documented.

### Suggested Verification Commands
- `gofmt -w <changed files>`
- `go test ./cmd/steiner ./internal/tui ./internal/tool/builtin ./internal/output ./internal/agent`
- `go test ./...`
- `go build ./...`
- `go vet ./...`

## Suggested Subagent Dispatch Strategy

### Dispatch Order
1. Stage 1
2. Stage 2
3. Stage 3
4. Stage 4
5. Stage 5
6. Stage 6 review/integration

### Parallelism Guidance
- Stages 1 and 2 should not run in parallel unless one worker is strictly limited to `cmd/steiner` approval plumbing and the other to generic TUI overlay composition.
- Stage 3 depends on Stage 2 because it reuses the centered overlay compositor.
- Stage 4 can begin once the overall TUI direction is stable; it does not depend on Stage 2 or 3 implementation details.
- Stage 5 depends on Stage 4 because it consumes explicit preview payloads and removes the old display-file overlay path.
- Final integration should happen only after Stages 1 through 5 land cleanly.

### Cheap Worker Ownership Boundaries
- Worker A: Stage 1 approval runtime + approval tray
- Worker B: Stage 2 overlay compositor + exit modal
- Worker C: Stage 3 context overlay markdown migration
- Worker D: Stage 4 display-file API + preview pipeline
- Worker E: Stage 5 display-file transcript rendering + TUI cleanup
- Integrator/Reviewer: Stage 6 regression sweep and final cleanup

### Merge Risk Notes
- Highest merge-conflict area:
  - `internal/tui/model.go`
  - `internal/tui/model_update.go`
  - `internal/tui/model_events.go`
- To reduce collisions:
  - keep Stage 1 and Stage 2 changes separated by concern where possible
  - let Stage 5 rebase after Stage 4 and after any TUI-state changes from Stages 1-3

## Detailed Test Matrix

### Approval Flow
- approval request opens tray with correct tool and preview
- left/right or up/down changes selected action
- `Enter` returns `allow once`
- `Enter` returns `always allow`
- `Esc` returns deny
- active run blocks normal prompt submission while approval tray is open
- approval resolution restores standard prompt placeholder and state

### Exit Modal
- idle `Ctrl+C` opens modal instead of quitting immediately
- idle `Ctrl+D` opens modal instead of quitting immediately
- modal `Cancel` closes without exiting
- modal `Exit` quits
- overlay keeps visible base content outside modal bounds

### Context Overlay
- markdown headings, lists, code fences, and inline code render styled output
- overlay opens from context report event
- overlay closes on `Esc`
- overlay scrolling works on long rendered markdown
- base transcript/sidebar remains visible outside overlay

### `display_file` API / Data Flow
- schema rejects `language`
- schema accepts `path`, `offset`, `limit`
- negative or zero `offset` normalizes to `1`
- missing or zero `limit` normalizes to default
- large `limit` caps to max
- preview carries sliced file contents and inferred syntax
- result content returned to the model is metadata-only

### `display_file` TUI Rendering
- tool card is created through normal tool-call event flow
- tool card starts expanded
- caption distinguishes display preview from read/write preview
- preview renders syntax-highlighted lines
- preview reflects requested line window
- no file overlay is opened

### Regression Coverage
- ordinary `read` preview still renders as before
- `write` and `edit` previews still render as before
- `glob`, `ls`, `grep`, and `bash` previews still render as before
- file picker overlay still works
- palette overlay still works
- help toggle still works

## Assumptions
- It is acceptable to introduce one or more small new focused TUI files if they keep `model.go` from growing too much.
- It is acceptable to add a dedicated preview kind for `display_file` if that simplifies rendering and default-expanded behavior.
- No external library upgrade is required; the current Bubble Tea / Lip Gloss / Glamour stack is sufficient.
