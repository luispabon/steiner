# Rigmarole Plan: T020, T021, T012, T013, T011, T009

## Request

Plan implementation work for the following backlog items from `.project_planning/BACKLOG.md`:

- `T020` - Tool to display file to user without model reading it
- `T021` - `/context` as overlay modal
- `T012` - `--exec` mode no streaming by default
- `T013` - Apply glamour to user prompts
- `T011` - Exit confirmation on `Ctrl+D` / `Ctrl+C`
- `T009` - Implement approvals using `huh`

User constraints and direction:

- Do not execute the plan in this document.
- The reusable overlay abstraction must be extracted from the file picker path, not the command palette path.
- The final plan should be detailed enough that simpler workers can implement it later in bounded steps.
- Keep package boundaries intact across `cmd/steiner`, `internal/tui`, `internal/output`, `internal/tool`, and `internal/agent`.
- Prefer minimal, composable changes over wide refactors.

## Overview

This work is best treated as one coordinated UX/infrastructure batch with a strict dependency order rather than six independent tickets.

The main coupling is:

1. `T012` changes the non-interactive execution path and is mostly isolated.
2. `T009` and `T011` share the new `huh` interaction primitives.
3. `T021` and `T020` share a reusable overlay viewer and should be built on the same overlay abstraction.
4. `T013` is presentation-only and should come after the transcript and overlay work stabilizes.

Current code-level constraints already confirmed:

- `--exec` always streams today because `internal/agent/model_call.go` prefers `StreamChatCompletion` whenever available.
- `/context` is currently handled locally in the TUI input parser but sends a markdown report back into the transcript via `output.NewContextReportEvent`.
- The TUI does not yet have a generic reusable overlay viewer abstraction.
- The closest reusable overlay starting point is `internal/tui/file_picker.go`; the command palette uses a different centered modal pattern and should not be the extraction source.
- Interactive approvals are currently text-entry driven through `approvalResponse` channels and approval pills in the transcript.
- The TUI already uses glamour for assistant markdown rendering, but user transcript rendering is still plain styled text.

Implementation strategy:

- Build the smallest shared primitives first.
- Keep each stage shippable and testable on its own.
- Ensure each worker step has one main responsibility and a narrow edit surface.

## Decisions

These decisions are fixed for implementation unless explicitly revised later:

- `T009` scope includes:
  - `huh`-based tool approvals in interactive mode
  - `huh`-based exit confirmation for `T011`
  - `huh`-based slash-command suggestions
  - replacing current dot-style waiting indicators with `huh` spinners where practical in the TUI
- `T020` is always available as a read-only built-in tool. It is not config-gated for v1.
- `T020` must not return file contents to the model. It may return metadata and an acknowledgement only.
- `T020` should fail cleanly in non-interactive contexts where no TUI overlay can be shown.
- `T021` keeps the current `/context` data source and content semantics. Only the presentation changes: overlay instead of transcript insertion.
- The reusable overlay base for `T021` must be extracted from the file picker implementation.
- `T012` adds `--enable-streaming`; default behavior for `--exec` becomes non-streaming.
- Interactive mode remains streaming by default.
- `T013` applies markdown rendering only to already-submitted user transcript content, not to the live textarea widget.

## Likely Code Areas

- `cmd/steiner/commands.go`
- `cmd/steiner/exec.go`
- `cmd/steiner/interactive.go`
- `cmd/steiner/approval.go`
- `cmd/steiner/runner.go`
- `cmd/steiner/runtime.go`
- `internal/agent/model_call.go`
- `internal/output/context_report.go`
- `internal/output/event_types.go`
- `internal/output/event_constructors.go`
- `internal/tool/executor.go`
- `internal/tool/builtin/builtins.go`
- `internal/tool/builtin/schema.go`
- `internal/tool/builtin/read.go`
- `internal/tui/model.go`
- `internal/tui/model_update.go`
- `internal/tui/model_input.go`
- `internal/tui/input.go`
- `internal/tui/file_picker.go`
- `internal/tui/content.go`
- `internal/tui/content_events.go`
- `internal/tui/content_render.go`
- `internal/tui/help.go`
- nearby tests in `cmd/steiner`, `internal/tui`, `internal/tool/builtin`, and `internal/output`

## Verification Strategy

### Sources

- `AGENTS.md`
- `go.mod`
- `Makefile`
- nearby existing tests in affected packages

### Commands

- `gofmt -w <files>`
- `go test ./internal/tui -run TestName`
- `go test ./internal/tool/... -run TestName`
- `go test ./cmd/steiner -run TestName`
- `go test ./...`
- `go build ./...`
- `go vet ./...`

### Tiers

- Cheap
  - `gofmt -w <files>`
  - package-local targeted tests
- Medium
  - `go build ./...`
  - `go vet ./...`
- Expensive
  - `go test ./...`

### Default Verification Timing

- After each step:
  - format only touched files
  - run the narrowest relevant package tests
- After each stage:
  - run the broader tests for all packages touched by that stage
- End of implementation:
  - `go test ./...`
  - `go build ./...`
  - `go vet ./...`

### Assumptions

- Existing tests in `internal/tui/model_test.go`, `internal/tui/content_test.go`, `internal/tui/help_test.go`, `internal/tool/*_test.go`, and `cmd/steiner/main_test.go` are the main extension points.
- No additional lint system beyond repo-documented Go checks is required for this batch.

## Execution Order

The stages below are intentionally ordered and should be executed in sequence:

1. Stage 0: Lock the shared infrastructure boundaries
2. Stage 1: `T012` non-streaming `--exec`
3. Stage 2: Reusable overlay extraction from file picker
4. Stage 3: `T021` `/context` overlay
5. Stage 4: `T020` display-file built-in and UI event path
6. Stage 5: `T009` `huh` approvals and TUI spinner adoption
7. Stage 6: `T011` exit confirmation
8. Stage 7: `T013` glamour rendering for user prompts
9. Stage 8: final cleanup and integration verification

Each later stage may assume the earlier stage contracts exist.

## Stage 0: Lock Shared Infrastructure Boundaries

### Goal

Create explicit internal contracts so later workers do not improvise architecture mid-flight.

### Worker Scope

- Primary packages: `internal/tui`, `internal/output`, `cmd/steiner`, `internal/tool/builtin`

### Steps

1. Define the overlay extraction target.
   - Identify the file picker logic that is genuinely reusable:
     - framed overlay shell
     - width/height handling
     - footer rendering
     - scroll-window rendering pattern
     - overlay placement over the base TUI
   - Keep file-search-specific logic in `file_picker.go`.
   - Do not pull palette-specific command logic into the shared abstraction.

2. Define the non-streaming exec contract.
   - Add a run-level way to express “streaming preferred” vs “final response only”.
   - Keep provider interfaces unchanged if possible.
   - Prefer changing the agent/model-call selection logic over adding a new provider method.

3. Define the user-only file-display contract.
   - Introduce a dedicated output event type for “display file in UI”.
   - Keep file contents out of tool results and out of conversation history.
   - Let the TUI be responsible for rendering the file after it receives the event.

4. Define the interactive `huh` boundary.
   - `cmd/steiner/interactive.go` remains the owner of runtime orchestration.
   - `internal/tui` remains the owner of in-app UI state and presentation.
   - If `huh` needs terminal-level interaction, isolate it so approval/confirmation logic does not bleed into unrelated TUI code.

### Acceptance Criteria

- Later stages can point at named internal contracts instead of inventing new pathways.
- No implementation behavior changes are required yet beyond any minimal scaffolding needed to support later steps.

## Stage 1: T012 Non-Streaming `--exec`

### Goal

Make `--exec` non-streaming by default and add `--enable-streaming` as an opt-in.

### Worker Scope

- Primary packages: `cmd/steiner`, `internal/agent`

### Steps

1. Extend CLI flags.
   - Add `enableStreaming bool` to `cliFlags`.
   - Register `--enable-streaming` in `cmd/steiner/commands.go`.
   - Scope the flag description explicitly to `--exec` behavior.

2. Thread exec streaming preference into the run path.
   - Add an explicit field on the run request or adjacent internal config used by `cliRunner`.
   - Interactive mode should set streaming enabled.
   - `--exec` should set streaming from the new CLI flag, defaulting to disabled.

3. Update model-call behavior.
   - In `internal/agent/model_call.go`, choose `ChatCompletion` first when streaming is disabled.
   - Preserve current fallback behavior when stream APIs are unavailable or fail in the opposite direction only if needed for compatibility.
   - Do not regress event emission for API request / response bookkeeping.

4. Add non-streaming waiting output.
   - In `runExecMode`, emit a simple human-visible waiting line before the model call when streaming is disabled.
   - Once the final response is available, emit the assistant reply atomically once.
   - Avoid chunk events in the non-streaming path.

5. Keep approval behavior stable in exec mode.
   - Do not convert exec approvals to `huh`.
   - Preserve stdin-based approval flow for one-shot CLI sessions.

### Acceptance Criteria

- `steiner --exec "..."` does not stream by default.
- `steiner --exec --enable-streaming "..."` preserves the current streaming behavior.
- Non-streaming exec mode shows a waiting message, then the full assistant response at once.
- Interactive mode behavior is unchanged by this stage.

### Recommended Tests

- CLI flag parsing test for `--enable-streaming`
- `cmd/steiner` tests for default non-streaming exec behavior
- targeted tests around `internal/agent/model_call.go` for stream selection behavior

## Stage 2: Reusable Overlay Extraction From File Picker

### Goal

Extract a reusable overlay viewer foundation from the file picker implementation and placement behavior.

### Worker Scope

- Primary package: `internal/tui`

### Steps

1. Extract a generic overlay frame/view shell.
   - Move reusable overlay frame concerns out of `file_picker.go`.
   - Preserve the existing look and feel of the file picker.
   - Shared shell should handle:
     - open/closed state
     - dimensions
     - title/header line
     - optional divider
     - body region
     - footer/help chips

2. Extract reusable list/viewport rendering helpers only where clearly justified.
   - Keep search/filter logic file-picker-local.
   - Avoid over-abstracting into a generic UI framework.
   - The target is “shared overlay chrome and placement”, not “one overlay type for everything”.

3. Extract overlay placement from `Model.View`.
   - Reuse the file picker’s current bottom-anchored overlay placement model for the new shared abstraction.
   - Keep centered overlays available where already used, but do not force the file-picker-derived viewer into the palette’s centered layout.

4. Adapt file picker to use the shared overlay shell.
   - File picker behavior and appearance should remain materially unchanged.
   - This step proves the extraction is real rather than parallel duplicated code.

### Acceptance Criteria

- File picker uses the new shared overlay shell.
- Overlay extraction came from file picker logic, not palette logic.
- Shared overlay code is sufficiently reusable for a read-only viewer and `/context` modal.

### Recommended Tests

- extend `internal/tui/file_picker_test.go`
- add or extend `internal/tui/model_test.go` for overlay placement

## Stage 3: T021 `/context` Overlay

### Goal

Convert `/context` from transcript output to an immediate overlay modal while keeping the current report content.

### Worker Scope

- Primary packages: `internal/tui`, `cmd/steiner`, `internal/output`

### Steps

1. Split “build context report” from “render context report into transcript”.
   - Keep `output.BuildContextReport` as the source of truth for report content.
   - Stop treating `/context` as an event that must become transcript content in interactive mode.

2. Add TUI-owned context overlay state.
   - Store the current rendered context report string in TUI state.
   - Add open/close behavior and any needed scroll position.
   - Keep `/context` handled locally from `parseInput` / `handleEnter`.

3. Change the interactive callback path.
   - `OnContextInspect` should produce or fetch the same report content as today.
   - The result should populate overlay state instead of emitting a transcript segment.
   - The overlay should open immediately, independent of turn completion timing.

4. Render the overlay through the new shared shell.
   - Use the shared overlay extracted from file picker.
   - Render the existing report content as-is.
   - Do not redesign the underlying data model in this stage.

5. Preserve non-interactive behavior where applicable.
   - If `/context` is only interactive, keep it that way.
   - If any tests or command paths depend on context report events elsewhere, avoid breaking them unnecessarily.

### Acceptance Criteria

- Typing `/context` opens an overlay immediately.
- The displayed information is the same report content currently generated today.
- The report no longer appears as transcript content during interactive use.
- Overlay supports close and scroll behavior.

### Recommended Tests

- update `TestModelHandlesContextCommandLocally`
- add TUI tests for open, close, and viewport behavior
- preserve existing report builder tests in `internal/output`

## Stage 4: T020 Display-File Built-In and UI Event Path

### Goal

Add a built-in tool that asks the TUI to display a file to the user without sending file contents back to the model.

### Worker Scope

- Primary packages: `internal/tool/builtin`, `internal/output`, `internal/tui`, `cmd/steiner`

### Steps

1. Add the built-in tool schema and registration.
   - Add a new built-in tool, preferably `display_file`.
   - Register it in `internal/tool/builtin/builtins.go`.
   - Add a schema in `internal/tool/builtin/schema.go`.
   - Keep the v1 schema minimal:
     - required `path`
     - optional display hints only if implementation truly needs them

2. Implement tool handler.
   - Validate the path with the existing path policy.
   - Resolve path safely relative to workspace rules.
   - Return a metadata-only result:
     - normalized display path
     - success acknowledgement
     - possibly viewer hint fields
   - Do not include file content in the returned JSON result.

3. Add an output event for UI-only file display.
   - The event payload should carry path and view options only.
   - Emit it from the interactive execution path after the tool succeeds.
   - Keep event semantics explicit so the TUI can render the file from disk itself.

4. Add TUI file-view overlay.
   - Build a read-only file viewer overlay using the shared overlay shell from Stage 2.
   - The viewer should read the file locally on receipt/open, not from the model response.
   - Support at minimum:
     - path display
     - file text body
     - scroll
     - close

5. Handle unsupported modes.
   - In `--exec` and other non-TUI contexts, return a bounded failure message stating interactive file display is unavailable.
   - Do not silently fall back to `read`; let the model decide how to recover.

6. Keep transcript clean.
   - Do not append file content as assistant or tool transcript content.
   - If a status line is needed, keep it terse and metadata-only.

### Acceptance Criteria

- Model can call the new built-in tool in interactive mode.
- The user sees the requested file in an overlay without its contents entering model-visible tool output.
- The tool remains read-only and path-policy-respecting.
- Non-interactive mode fails cleanly with a bounded message.

### Recommended Tests

- new builtin tests for schema, registration, and metadata-only results
- output event tests for the new file-display event
- TUI tests for viewer overlay behavior and transcript non-leakage

## Stage 5: T009 Huh Approvals and TUI Spinner Adoption

### Goal

Replace the current interactive approval UX with `huh`, add slash-command suggestions through `huh`, and use `huh` spinners where the TUI currently shows waiting indicators.

### Worker Scope

- Primary packages: `cmd/steiner`, `internal/tui`

### Steps

1. Add `huh` dependency and isolate its usage.
   - Add the dependency to `go.mod`.
   - Contain `huh`-specific code in a small number of focused files.
   - Avoid spreading raw `huh` constructs throughout unrelated model logic.

2. Convert interactive approvals.
   - Replace free-text approval entry with a `huh` approval dialog.
   - Support:
     - approve once
     - deny once
     - always allow, if supported by current approval policy path
   - Keep the existing approval event emission and tool execution contract intact.

3. Decide and implement “always allow” persistence behavior.
   - If current config/policy cannot persist a session-level override directly, implement a session-local allow cache keyed appropriately.
   - Do not silently write permanent config files as part of “always allow”.

4. Add slash-command suggestions via `huh`.
   - Use the existing completion candidate source from `buildCompletionCandidates`.
   - Scope suggestions to slash-command entry, not arbitrary prompt completions.
   - Preserve current command parsing semantics.

5. Replace current waiting dots with `huh` spinners where feasible.
   - Apply to streaming / waiting states surfaced in the TUI.
   - Keep rendering lightweight; do not create a second conflicting spinner system.
   - Preserve clear status text for thinking/tool/response phases.

6. Preserve non-interactive approval flow.
   - Do not change `stdinApprovalResponder`.
   - `huh` integration is interactive-mode-only in this stage.

### Acceptance Criteria

- Interactive tool approvals use `huh`.
- Approve / deny / always allow behavior works correctly.
- Slash-command suggestions are available through the new `huh` path.
- Current dot spinner states are replaced by `huh` spinner presentation in the TUI where those waiting indicators appear today.
- Exec mode approval behavior remains line-oriented and stable.

### Recommended Tests

- interactive approval tests in `cmd/steiner` or TUI-adjacent integration tests
- unit tests around approval state transitions
- command suggestion tests sourced from existing completion candidates

## Stage 6: T011 Exit Confirmation

### Goal

Use the same `huh` interaction family to confirm exit on `Ctrl+C` / `Ctrl+D` while idle.

### Worker Scope

- Primary package: `internal/tui`
- Secondary package: `cmd/steiner` only if runtime wiring is needed

### Steps

1. Add explicit idle-exit confirmation state.
   - First `Ctrl+C` / `Ctrl+D` during idle should not quit immediately.
   - It should trigger the confirmation dialog path.

2. Reuse the `huh` confirmation mechanism.
   - Prefer a shared wrapper/helper also used by approvals where sensible.
   - Keep the exit flow separate from active-run interrupt flow.

3. Preserve active-run interruption semantics.
   - During an active conversation, `Ctrl+C` / `Ctrl+D` should still map to interruption behavior, not idle exit confirmation.
   - Do not regress adjacent input-routing fixes already planned elsewhere in the repo.

4. Close the loop cleanly.
   - Confirm exits the app.
   - Cancel returns to normal idle input state with no stale modal state.

### Acceptance Criteria

- First idle `Ctrl+C` / `Ctrl+D` does not exit.
- Confirmation appears through the new dialog flow.
- Confirm exits; cancel returns to normal.
- Active-run interrupt behavior is not regressed.

### Recommended Tests

- extend `internal/tui/model_test.go` for idle vs active key handling

## Stage 7: T013 Glamour Rendering for User Prompts

### Goal

Render markdown-like submitted user prompts with glamour while keeping the input experience unchanged.

### Worker Scope

- Primary package: `internal/tui`

### Steps

1. Add markdown-like detection for user segments.
   - Reuse or adapt the assistant-side markdown heuristics rather than inventing a second unrelated detector.
   - Keep heuristics conservative enough to avoid over-rendering plain text.

2. Add a user markdown render path.
   - Extend user segment rendering so markdown-like submitted prompts use glamour.
   - Keep distinct user styling so user messages remain visually identifiable from assistant output.

3. Preserve plain user rendering for non-markdown.
   - Plain prompts should continue to use the simple block style.
   - Live textarea editing must remain untouched.

4. Check multiline and wrapping behavior.
   - Ensure markdown rendering does not break user transcript layout, wrapping, or spacing.

### Acceptance Criteria

- Submitted user prompts render with glamour when they contain markdown-like structure.
- Plain text prompts still render plainly.
- The input widget behavior is unchanged.

### Recommended Tests

- extend `internal/tui/content_test.go`
- add render tests for markdown-like and plain user content

## Stage 8: Final Cleanup and Integration Verification

### Goal

Verify that the stages compose correctly and that no transcript, event, or modal regressions were introduced.

### Worker Scope

- Cross-package verification only

### Steps

1. Run stage-specific regression tests.
   - `internal/tui`
   - `internal/tool/...`
   - `cmd/steiner`

2. Run broad repo verification.
   - `go test ./...`
   - `go build ./...`
   - `go vet ./...`

3. Perform a targeted manual QA checklist if execution later occurs.
   - interactive approval
   - `/context` overlay
   - file display overlay
   - idle exit confirmation
   - non-streaming `--exec`
   - user markdown transcript rendering

### Acceptance Criteria

- No failing package-local or repo-wide Go checks.
- No regressions in file picker behavior after overlay extraction.
- No leakage of file contents into tool results for `display_file`.
- No regression in interactive vs non-interactive approval behavior.

## Worker Handoff Rules

Use these rules when assigning the later execution to simpler workers:

- One worker per step or tightly-related pair of steps, not one worker per ticket.
- Do not assign the same files to parallel workers unless the overlap is trivial and intentional.
- Require each worker to:
  - state the exact files they intend to touch
  - implement only the assigned step
  - run only the narrowest relevant tests first
  - avoid broad opportunistic refactors
- Preferred worker grouping:
  - Worker A: Stage 1
  - Worker B: Stage 2
  - Worker C: Stage 3
  - Worker D: Stage 4
  - Worker E: Stage 5
  - Worker F: Stage 6
  - Worker G: Stage 7
  - Integrator: Stage 8

## Risks and Watchouts

- The `huh` integration may compete with Bubble Tea input ownership if introduced carelessly.
- Overlay extraction can sprawl if it tries to unify every modal instead of just shared file-picker-derived viewer behavior.
- `display_file` must not accidentally serialize file content into tool results, output previews, logs, or transcript segments.
- Changing non-streaming exec behavior must not double-emit final assistant content through both direct response printing and existing event sinks.
- User markdown rendering can blur assistant/user visual distinction if styling is not kept explicit.

## Open Questions Resolved by Default

These are implementation defaults, not active questions:

- The new display tool name is assumed to be `display_file`.
- “Always allow” for approvals should be session-scoped unless existing policy/config mechanisms already support a cleaner non-persistent override.
- `/context` overlay should render the existing report content rather than redesigning the data presentation in this batch.
- The reusable overlay should prioritize file viewer and context report use cases; palette unification is out of scope.
