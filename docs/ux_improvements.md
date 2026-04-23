
  # UX Improvements: Codex-Style Activity UI

  ## Summary

  Add a TUI-only activity layer that makes steiner feel closer to Codex during live runs. Build this entirely on top of the existing output.Event stream and Bubble Tea UI, without changing agent/tool
  contracts, provider flow, or plain --exec rendering.

  Target behavior:

  - Tool start shows a pending activity block with a colored dot and Calling <tool>...
  - The next line shows a tree-branch tool preview, e.g. └ bash("go test ./...")
  - Tool finish updates that same block from pending to completed instead of appending a second lifecycle line
  - Each activity block is separated from surrounding transcript entries by one empty line
  - While the model is thinking, the status bar shows an animated spinner and elapsed time: Working (mm:ss • esc to interrupt)

  ## Step-by-Step Implementation

  ### 1. Introduce structured tool activity segments in the TUI transcript

  Update internal/tui/content.go.

  Replace the current append-only treatment of tool events with structured transcript entries that can be updated in place.

  Add new segment kinds:

  - segmentToolActivity
  - segmentToolCommand

  Extend contentSegment with activity metadata fields:

  - activityID string
  - toolName string
  - toolStatus string
  - toolCommand string
  - toolError string

  Use these status values only:

  - pending
  - success
  - error

  Keep all existing behavior for:

  - assistant prose
  - assistant markdown
  - approvals
  - plain status lines
  - delegation entries

  Do not remove text from contentSegment; existing segment types should still rely on it.

  ### 2. Add stable matching for tool start/finish pairs

  Still in internal/tui/content.go.

  Add helper functions:

  - toolActivityKeyStart(payload output.ToolCallStartedEvent) string
  - toolActivityKeyFinish(payload output.ToolCallFinishedEvent) string
  - findToolActivityIndex(activityID string) int

  Matching rules:

  - Use CallID when present
  - Otherwise fall back to turn + tool name
  - Recommended fallback format: turn:<n> tool:<name>

  This key must be the only identifier used to update an existing tool row.

  ### 3. Add helpers to append and complete tool activity blocks

  Still in internal/tui/content.go.

  Add:

  - appendToolActivityStart(payload output.ToolCallStartedEvent)
  - completeToolActivity(payload output.ToolCallFinishedEvent)

  Start behavior:

  - Finish any in-progress assistant streaming first
  - Append one segmentToolActivity with toolStatus = pending
  - Append one segmentToolCommand immediately after it
  - Both segments share the same activityID

  Finish behavior:

  - Find the matching segmentToolActivity
  - If found, update only that segment’s state
  - Do not append another lifecycle status row
  - Leave the command segment unchanged
  - If no matching start exists, synthesize a completed block so the transcript still makes sense

  Synthesis fallback:

  - Append a completed segmentToolActivity
  - If a preview can be produced, append a segmentToolCommand
  - If not, render only the activity line, but still give it the same blank-line separation as a normal block

  ### 4. Replace current tool event formatting in AppendEvent

  Update AppendEvent in internal/tui/content.go.

  For output.EventTypeToolCallStarted:

  - finish streaming
  - decode output.ToolCallStartedEvent
  - call appendToolActivityStart
  - return

  For output.EventTypeToolCallFinished:

  - finish streaming
  - decode output.ToolCallFinishedEvent
  - call completeToolActivity
  - return

  Do not use output.FormatEvent(event) for TUI tool rows anymore.

  All non-tool event handling should stay as close as possible to current behavior.

  ### 5. Add concise tool preview formatting

  Still in internal/tui/content.go.

  Add helper functions:

  - formatToolCommandPreview(tool string, args map[string]any) string
  - stringArg(args map[string]any, key string) string
  - compactJSON(v any) string
  - truncatePreview(s string, max int) string

  Format rules:

  - bash -> prefer command, render bash("<command>")
  - read -> prefer path, render read("<path>")
  - write -> prefer path, render write("<path>")
  - edit -> prefer path, render edit("<path>")
  - search -> prefer query, render search("<query>")
  - fallback -> <tool>(<compact json>)

  Constraints:

  - Single logical line only
  - Never pretty-print JSON
  - Truncate long previews
  - Recommended limit: 120 chars including ellipsis
  - If tool name is empty, use tool

  Do not add broader tool-specific logic beyond these cases.

  ### 6. Render tool activity blocks with exact spacing rules

  Update transcript rendering in internal/tui/content.go.

  Required visual structure for a normal tool block:

  - first line: status row
  - second line: command preview row prefixed with └
  - then one empty line

  Spacing rules:

  - No blank line between the two lines of the block
  - Exactly one blank line after the block
  - Existing non-tool transcript segments keep their current newline behavior

  Recommended implementation:

  - segmentToolActivity renders with one trailing newline
  - segmentToolCommand renders with two trailing newlines

  If a synthesized completion block has no command preview:

  - render the activity row with two trailing newlines

  ### 7. Define the exact tool lifecycle copy

  Implement these literal strings in internal/tui/content.go.

  Pending:

  - • Calling <tool>...

  Success:

  - • Called

  Error:

  - • Called (<tool> error)

  Command preview:

  - └ <formatted preview>

  Copy rules:

  - Only pending rows mention the tool name directly in the main status line
  - Completed rows stay short
  - Error rows stay concise
  - Do not inline full tool error payloads in the transcript row

  ### 8. Add dedicated theme styles for activity and spinner states

  Update:

  - internal/tui/theme/theme.go
  - internal/tui/theme/catppuccin.go

  Extend theme.Styles with:

  - ActivityPending
  - ActivitySuccess
  - ActivityError
  - ActivityCommand
  - StatusSpinner

  Color choices:

  - ActivityPending and StatusSpinner use the existing peach tone already used for sidebar titles
  - ActivitySuccess uses green
  - ActivityError uses red
  - ActivityCommand uses a muted foreground

  Do not change:

  - assistant prose styling
  - markdown styling
  - sidebar layout
  - status bar base background

  ### 9. Add working-state fields to the status bar model

  Update internal/tui/statusbar.go.

  Extend statusState with:

  - working bool
  - workingStartedAt time.Time
  - spinnerFrame int

  Add helper methods:

  - spinnerGlyph() string
  - elapsedLabel(now time.Time) string
  - workingLabel(now time.Time) string

  Spinner frames:

  - Use ASCII-safe sequence: |, /, -, \

  Elapsed format:

  - Always mm:ss
  - Zero-padded
  - Use elapsed time since workingStartedAt

  ### 10. Update status bar rendering logic

  Still in internal/tui/statusbar.go.

  Preserve the existing general format of model/turn/context/hints, but add a special working block when working == true.

  Recommended order:

  - model ... | turn ... | ctx ... | <spinner> Working (mm:ss • esc to interrupt)

  Rules:

  - Use StatusSpinner style for the spinner glyph
  - Keep the rest of the working text visually distinct but consistent with the status bar
  - If approval is active, do not render working state
  - If not working, fall back to the current status rendering behavior

  ### 11. Add Bubble Tea tick handling for spinner animation

  Update internal/tui/model.go.

  Add:

  - type workingTickMsg struct{}
  - func workingTickCmd() tea.Cmd

  Tick interval:

  - 125ms

  Tick behavior:

  - Each tick advances spinnerFrame
  - If still working, schedule another tick
  - If no longer working, stop scheduling ticks

  Do not introduce a permanently running ticker.

  ### 12. Start and stop the spinner from model-call events

  Update applyEvent in internal/tui/model.go.

  On output.ModelCallStartedEvent:

  - set m.status.working = true
  - set workingStartedAt = event.Timestamp if zero
  - reset spinnerFrame = 0

  On output.ModelCallFinishedEvent:

  - set working = false
  - clear workingStartedAt
  - reset spinnerFrame

  On output.ApprovalRequestedEvent:

  - clear working state immediately
  - keep existing approval prompt behavior

  On output.RunFinishedEvent and output.StopReasonEvent:

  - clear working state

  Do not infer working state from RunStartedEvent. Only real model-call events should control it.

  ### 13. Wire tick scheduling into Update

  Update Update in internal/tui/model.go.

  Required flow:

  - runtimeEventMsg still routes through applyEvent
  - If that event begins a working phase, return the first workingTickCmd()
  - When receiving workingTickMsg:
      - if status.working == true, advance the spinner and return the next workingTickCmd()
      - otherwise do nothing

  Keep this isolated from:

  - scrolling logic
  - help overlay
  - input handling
  - skill toggles
  - approval text entry

  ### 14. Preserve viewport and transcript behavior

  Still in internal/tui/model.go.

  Do not change:

  - auto-scroll rules
  - mouse wheel behavior
  - sidebar visibility/layout
  - input history
  - skill toggling
  - model switching
  - approval entry flow

  Expected result:

  - tool lifecycle updates appear in the transcript
  - completed tool calls update in place
  - if the user has scrolled upward, updates should respect existing autoScroll logic rather than forcibly snapping to bottom

  ### 15. Keep plain stream output unchanged

  Do not modify:

  - internal/output/plain.go
  - internal/output/stream.go
  - plain --exec flow in cmd/steiner/main.go

  This plan is explicitly TUI-only. The current exec renderer remains the baseline.

  ### 16. Add unit tests for tool activity rendering

  Extend internal/tui/content_test.go.

  Add tests for:

  - tool start creates exactly two linked segments
  - tool finish updates an existing activity row rather than appending a second lifecycle line
  - rendered pending block contains:
      - Calling <tool>...
      - └ <preview>
      - one empty separator line after the block
  - fallback key works when CallID is absent
  - unknown tools use compact JSON fallback formatting
  - long previews truncate with ...
  - finish without prior start still renders a sensible completed block
  - error completion renders error-state copy and does not dump the full error payload

  Keep existing delegation tests intact.

  ### 17. Add unit tests for working-state status bar behavior

  Extend internal/tui/model_test.go or add a focused status-bar test file if cleaner.

  Add tests for:

  - ModelCallStartedEvent enables working state
  - ModelCallFinishedEvent disables working state
  - ApprovalRequestedEvent clears working state
  - status bar view contains Working ( while active
  - elapsed formatting handles:
      - 00:00
      - 00:59
      - 01:00
      - 12:34
  - spinner frame sequence rotates correctly

  Use fixed times in tests. Do not rely on sleeping.

  ### 18. Add model-flow integration tests for the TUI

  Extend internal/tui/model_test.go.

  Add one full interaction test:

  1. create model
  2. send RunStartedEvent
  3. send TurnStartedEvent
  4. send ModelCallStartedEvent
  5. send ToolCallStartedEvent
  6. assert pending activity block is rendered
  7. send ToolCallFinishedEvent
  8. assert the same block now shows Called
  9. assert no duplicate lifecycle line was appended
  10. send ModelCallFinishedEvent
  11. assert working state is cleared

  Add one approval interruption test:

  1. send ModelCallStartedEvent
  2. assert working state is true
  3. send ApprovalRequestedEvent
  4. assert working state is false
  5. assert approval state is active

  ### 19. Format and verify after implementation

  After implementation, run:

  1. gofmt -w internal/tui/content.go internal/tui/model.go internal/tui/statusbar.go internal/tui/theme/theme.go internal/tui/theme/catppuccin.go internal/tui/content_test.go internal/tui/model_test.go
  2. go test ./internal/tui ./cmd/steiner
  3. If those pass, run go test ./...

  Do not run wider formatting over unrelated files.

  ## Internal Interface Changes

  Internal only:

  - theme.Styles gains new activity/spinner styles
  - statusState gains working/timer/spinner fields
  - contentSegment gains optional tool-activity metadata
  - TUI Update handles a new internal tick message

  No public contract changes:

  - no new CLI flags
  - no event schema changes
  - no provider changes
  - no tool execution changes
  - no plain stream changes

  ## Acceptance Criteria

  The work is complete when:

  - interactive TUI tool calls appear as a two-line pending block
  - completion updates the existing block rather than appending a second lifecycle row
  - there is one empty line after each activity block
  - the status bar shows a live spinner and elapsed timer during model work
  - approval mode suppresses the spinner
  - assistant prose/markdown rendering still behaves as before
  - plain --exec output is unchanged
  - targeted TUI tests pass, then broader Go tests pass

  ## Assumptions and Defaults

  - Scope is interactive TUI only
  - Spinner stays ASCII-safe
  - Completed label is exactly Called
  - Tool errors use concise error-state copy rather than a full inline payload
  - Tool previews are intentionally shallow and constrained to a few common tools plus compact JSON fallback
