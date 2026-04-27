# BACKLOG

Tickets derived from `docs/IDEAS.md`. Each ticket has a unique ID, title, description, acceptance criteria (when provided), status, and priority.

## Status Legend

| Status | Meaning |
|---|---|
| **Not started** | No work has begun on this ticket. |
| **In progress** | Work is actively being done. |
| **Finished** | Implementation is complete and verified. |

## Priority Legend

| Priority | Meaning |
|---|---|
| **High** | Must fix or ship; blocks other work or significantly impacts user experience. |
| **Medium** | Should be done; improves UX or correctness but not blocking. |
| **Low** | Nice-to-have; low urgency or speculative. |

---

## Immediate

### T001 — Remove "thinking_chunk" from app logs by default

**Area:** Logging  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** High

#### Description
"thinking_chunk" appears in app logs by default. Add a config flag to control it, disabled by default.

#### Acceptance Criteria
- [ ] Add `logging.thinking_chunk` config flag (BOOL)
- [ ] Default value is `false`
- [ ] When `false`, "thinking_chunk" is not emitted to logs
- [ ] When `true`, "thinking_chunk" is logged as before

---

### T002 — Move system prompt out of code into config

**Area:** Configuration / Prompting  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** High

#### Description
The system prompt is currently embedded in `internal/prompt/system.go`. Move it to a config file so users can customise it per-model.

#### Acceptance Criteria
- [ ] System prompt is no longer embedded in `internal/prompt/system.go`
- [ ] Prompt text is loaded from a config file
- [ ] Config supports per-model override
- [ ] Falls back to the current default when no config is provided

---

### T003 — Move other hardcoded model prompts into config

**Area:** Configuration / Prompting  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** High

#### Description
Search for other hardcoded model prompts (e.g. compaction prompts) and move them into the config system so they can be overridden per-model.

#### Acceptance Criteria
- [ ] All hardcoded model prompts are identified
- [ ] Each is moved into the config system
- [ ] Per-model override is supported for each
- [ ] Fallback to current defaults when no override is provided

---

### T004 — List files in folder/subfolders bypassing the agent

**Area:** UX / Navigation  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** High

#### Description
Allow the user to list files in the current folder or subfolders without invoking the agent.

#### Acceptance Criteria
- [ ] New keybind or command lists files in the current folder
- [ ] Supports recursive listing into subfolders
- [ ] Does not invoke the agent / model

---

### T005 — File auto-completion via @

**Area:** UX / Input  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** High

#### Description
Allow the user to type `@` in the prompt to trigger auto-completion of files on the filesystem relative to the project root.

#### Acceptance Criteria
- [ ] Typing `@` in the input triggers a file picker / auto-complete menu
- [ ] Menu lists files relative to the project root
- [ ] Supports filtering as the user types
- [ ] Selecting a file inserts the file path into the prompt

---

### T006 — Conversation pane scrollbar

**Area:** UI  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** High

#### Description
The conversation pane needs a scrollbar so the user can scroll through long conversations.

#### Acceptance Criteria
- [ ] Conversation pane scrolls when content exceeds visible area
- [ ] Scrollbar is visible or behaviour is obvious to the user

---

### T007 — Enable text selection in the TUI

**Area:** UI  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** High

#### Description
Currently the user cannot select text with the mouse. Investigate and fix so that text selection works.

#### Acceptance Criteria
- [ ] User can click-and-drag to select text in the TUI
- [ ] Selected text can be copied (e.g. via Ctrl+C or clipboard binding)

---

### T008 — Tool path blacklist

**Area:** Tools / Safety  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** High

#### Description
Traversal tools (SEARCH, GLOB) should support a blacklist of paths to avoid.

#### Acceptance Criteria
- [ ] Config option defines paths to exclude from SEARCH and GLOB
- [ ] Excluded paths are not returned by SEARCH or GLOB
- [ ] Blacklist supports glob patterns or exact paths

---

## Mid-term

### T009 — Implement approvals using huh

**Area:** UX / Tool Safety  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Medium

#### Description
Use https://github.com/charmbracelet/huh to implement tool approval prompts. Also explore using it for prompt command auto-suggestions.

#### Acceptance Criteria
- [ ] Tool approvals use huh for the UI
- [ ] Approvals still function correctly (approve / deny / always allow)
- [ ] (Optional) huh is used for prompt auto-suggestions

---

### T010 — Evaluate dive tools

**Area:** Tools  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Medium

#### Description
Look at tools provided by https://github.com/deepnoodle-ai/dive and consider replacing steiner's tools with those.

#### Acceptance Criteria
- [ ] dive tools are evaluated
- [ ] Decision documented: replace, augment, or defer
- [ ] If replacing, steiner's tools are swapped and tests pass

---

### T011 — Exit confirmation on Ctrl+D / Ctrl+C

**Area:** UX  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Medium

#### Description
When pressing Ctrl+D or Ctrl+C a second time, show a status message or modal asking the user if they're sure they want to exit.

#### Acceptance Criteria
- [ ] First Ctrl+D / Ctrl+C during idle does not exit
- [ ] A confirmation modal or status message appears
- [ ] Confirming exits; cancelling returns to normal

---

### T012 — --exec mode no streaming by default

**Area:** CLI / UX  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Medium

#### Description
The `--exec` mode should not stream responses by default. Add a `--enable-streaming` flag (disabled by default). When streaming is off, show a "waiting" message and display the full response once received.

#### Acceptance Criteria
- [ ] `--exec` mode does not stream by default
- [ ] `--enable-streaming` flag added
- [ ] When streaming is off, a "waiting for response" indicator is shown
- [ ] Full response is displayed atomically when available

---

### T013 — Apply glamour to user prompts

**Area:** UI / Markdown  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Medium

#### Description
Apply glamour markdown rendering to user prompts for better readability.

#### Acceptance Criteria
- [ ] User prompt text is rendered with glamour when it contains markdown
- [ ] Rendering does not break the input experience

---

## Long-term

### T014 — Native context-mode integration

**Area:** Integration  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Low

#### Description
Natively integrate steiner with context-mode.

#### Acceptance Criteria
- [ ] TODO

---

### T015 — Integrate with rtk

**Area:** Integration  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Low

#### Description
Integrate steiner with rtk.

#### Acceptance Criteria
- [ ] TODO

---

### T016 — Configurable skills folder(s) location

**Area:** Configuration  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Low

#### Description
Allow the user to configure the location(s) of skills folders.

#### Acceptance Criteria
- [ ] Config option specifies one or more skills folder paths
- [ ] Skills are discovered from all configured locations
- [ ] Default location is preserved when no config is provided

---

### T017 — Built-in commands for coding loop

**Area:** UX  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Low

#### Description
Provide built-in commands that support the coding loop workflow.

#### Acceptance Criteria
- [ ] TODO

---

### T018 — Delegation deferrals

**Area:** Delegation  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Low

#### Description
Implement delegation deferrals: background mode, re-promptable sessions, `touched_files` result field, parallel sub-agent capability. See `docs/DELEGATION_FUTURE.md`.

#### Acceptance Criteria
- [ ] Background mode for delegated sub-agents
- [ ] Re-promptable sessions
- [ ] `touched_files` result field on delegation
- [ ] Parallel sub-agent capability

---

### T019 — Command sandboxing

**Area:** Safety  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Low

#### Description
Add sandboxing for commands (e.g. bubblewrap, socat) similar to Claude and Codex. Reference: https://code.claude.com/docs/en/sandboxing

#### Acceptance Criteria
- [ ] Commands can be run inside a sandbox
- [ ] Sandboxing is configurable (enabled/disabled)
- [ ] Sandboxed commands are isolated from the host environment

---

### T020 — Tool to display file to user without model reading it

**Area:** Tools / UX  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Low

#### Description
Add a tool that lets the model request the agent to display a file to the user without the model having to read and regurgitate it first.

#### Acceptance Criteria
- [ ] New tool allows the model to request file display
- [ ] File is shown in the TUI without being streamed into the conversation
- [ ] Tool is opt-in or gated behind a config flag

---

### T021 — /context as overlay modal

**Area:** UX  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Low

#### Description
Make the `/context` command behave like the `?` keybind — open an overlay modal immediately instead of waiting for the current turn to finish. Display information in a table instead of lists of lists.

#### Acceptance Criteria
- [ ] `/context` opens an overlay modal immediately
- [ ] Does not wait for the current turn to finish
- [ ] Context information is displayed in a table format

---

### T022 — Session persistence and resuming

**Area:** Persistence  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Low

#### Description
Implement session persistence and the ability to resume sessions.

#### Acceptance Criteria
- [ ] Conversations are persisted to disk
- [ ] User can list and resume previous sessions
- [ ] Resumed session restores full conversation history

---

### T023 — Chat, plan, and build modes

**Area:** UX / Modes  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Low

#### Description
Add "chat", "plan", and "build" modes. Investigate implementation via system prompt changes. Ties into sandboxing (do sandboxing first).

#### Acceptance Criteria
- [ ] Three modes are available: chat, plan, build
- [ ] Each mode has a distinct system prompt
- [ ] Mode switching is supported via a command or keybind

---

### T024 — Re-implement core using dive

**Area:** Architecture  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Low

#### Description
Consider re-implementing core functionality using https://github.com/deepnoodle-ai/dive.

#### Acceptance Criteria
- [ ] Decision documented: re-implement, partial adoption, or defer
- [ ] If re-implementing, tests pass and behaviour is preserved

---

## Bugs — Prompt

### T025 — ESC does not stop streaming or cancel conversation

**Area:** Bug / Prompt  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** High

#### Description
Pressing ESC does not stop the current streaming response or cancel the conversation.

#### Acceptance Criteria
- [ ] Pressing ESC during streaming aborts the response
- [ ] Conversation is cancelled and user returns to prompt

---

### T026 — Tool approvals broken during streaming

**Area:** Bug / Tool Safety  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** High

#### Description
Tool approvals don't work, presumably because input is inhibited during streaming. Stop inhibiting input so users can prep their next prompt during streaming.

#### Acceptance Criteria
- [ ] Tool approvals work while streaming is active
- [ ] Input is not inhibited during streaming
- [ ] User can approve/deny tool calls at any time

---

### T027 — Ctrl+C does not work during active conversation

**Area:** Bug / Prompt  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** High

#### Description
Ctrl+C doesn't work during an active conversation, possibly the same root cause as tool approvals being broken.

#### Acceptance Criteria
- [ ] Ctrl+C during an active conversation cancels it
- [ ] User returns to a clean prompt

---

### T028 — Shift+Enter does not work

**Area:** Bug / Input  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** High

#### Description
Shift+Enter does not insert a newline in the prompt.

#### Acceptance Criteria
- [ ] Shift+Enter inserts a newline in the input prompt

---

### T029 — Minus symbol causes visual line break in prompt

**Area:** Bug / Input  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Medium

#### Description
Typing a minus `-` symbol makes the prompt wrap to the next line visually, though the content is still on one line when sent.

#### Acceptance Criteria
- [ ] Typing `-` does not cause a visual line break in the prompt

---

## Bugs — UI

### T030 — High CPU usage during streaming

**Area:** Bug / Performance  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** High

#### Description
Steiner uses substantial CPU during streaming. When responses stream for a long time (e.g. video transcripts with thinking blocks), tokens appear on screen slower than the API delivers them, and steiner continues rendering for minutes after the API finishes.

#### Acceptance Criteria
- [ ] CPU usage during streaming is reasonable (no busy-waiting)
- [ ] Rendering does not continue long after the API response is complete

---

### T031 — Compaction progress bar never finishes

**Area:** Bug / UI  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Medium

#### Description
The "Compacting summarizing context" progress bar never stops even after compaction succeeds. It should change to a full green bar and the label should change to "Compacting finished".

#### Acceptance Criteria
- [ ] Progress bar reaches 100% on successful compaction
- [ ] Label changes to "Compacting finished" (or equivalent)
- [ ] Bar does not remain indeterminate

---

## Bugs — Sidebar

### T032 — Model host does not change when switching models

**Area:** Bug / Sidebar  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Medium

#### Description
When switching models via `/model blah`, the host for the new active model doesn't change.

#### Acceptance Criteria
- [ ] Switching models via `/model` updates both the model name and host
- [ ] API calls go to the correct host for the selected model

---

### T033 — Modified files never update after application loads

**Area:** Bug / Sidebar  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Medium

#### Description
The modified files list in the sidebar never updates after the application loads. It should load at startup and update after every tool call and turn end.

#### Acceptance Criteria
- [ ] Modified files are populated at application startup
- [ ] Modified files list updates after every tool call
- [ ] Modified files list updates at the end of every turn

---

## Bugs — Compaction

### T034 — Full compaction result lost on second API request

**Area:** Bug / Compaction  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** High

#### Description
The system message containing the full compacted content is lost on the second API request after compaction. The first request has 4 messages (system prompt, AGENTS.md, retained context state JSON, full compaction), but the model responds as if the full compaction wasn't there. After tool calls, a third request is sent with only 3 messages — the full compaction message is missing entirely.

Questions:
- Why is the full compaction result getting lost from the conversation?
- What's the point of the truncated "retained context state" system message?

#### Acceptance Criteria
- [ ] Full compaction result is included in all API requests after compaction
- [ ] Conversation history is not corrupted across compaction boundary
- [ ] Retained context state message is reviewed for necessity

---

### T035 — Compaction can break with large tool call responses

**Area:** Bug / Compaction / Safety  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** High

#### Description
Compaction can break if a tool call results in a lot of data being dumped into the context. Need to ensure a tool call response being added to the context does not exceed the safety limit, and trigger compaction proactively.

#### Acceptance Criteria
- [ ] Tool call responses are checked against the safety limit before being added to context
- [ ] Compaction is triggered when a tool call response would exceed the limit
- [ ] No compaction errors occur due to oversized context

---

## Bugs — Tools

### T036 — Tool path exclusion not enforced

**Area:** Bug / Tools  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Medium

#### Description
Ensure that certain directories and files can be excluded from tools, particularly SEARCH and GLOB. (Related to T008.)

#### Acceptance Criteria
- [ ] Excluded paths are respected by SEARCH
- [ ] Excluded paths are respected by GLOB
- [ ] Exclusion config is applied consistently across all traversal tools

---

## Bugs — Messy

### T037 — Markdown view rendering is garbled

**Area:** Bug / Markdown  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Medium

#### Description
Markdown rendering looks garbled — code blocks with YAML look fine during streaming but collapse to the left once finished. Newlines seem lost. A workaround exists (parse markdown after streaming finishes) but it's not fully fixed.

#### Acceptance Criteria
- [ ] Code blocks render correctly after streaming finishes
- [ ] Newlines are preserved in rendered markdown
- [ ] No left-alignment collapse after streaming completes

---

## Implementing

### T038 — Colourised diff on file modification

**Area:** Feature / UX  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Low

#### Description
When modifying a file, steiner should show a colourised diff of the change. This should be entirely agent-side (not involving the model) with syntax highlighting appropriate to the file type.

#### Acceptance Criteria
- [ ] File modifications show a colourised diff in the TUI
- [ ] Diff is generated agent-side without model involvement
- [ ] Syntax highlighting is applied based on file type

---

### T039 — Syntax-highlighted display of new files

**Area:** Feature / UX  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Low

#### Description
When writing a whole new file, steiner should display the file with syntax highlighting appropriate to the file type.

#### Acceptance Criteria
- [ ] New files are displayed in the TUI with syntax highlighting
- [ ] Highlighting is appropriate to the file's language / format

---

### T040 — Evaluate existing TUI libraries for diff/highlight support

**Area:** Investigation  
**Source:** docs/IDEAS.md  
**Status:** Not started  
**Priority:** Low

#### Description
Investigate whether glamour or any of the other TUI libraries already support colourised diffs and syntax highlighting, or if a new dependency is needed.

#### Acceptance Criteria
- [ ] Survey of existing dependencies is documented
- [ ] Decision: use existing library, add new dependency, or build custom
- [ ] Chosen approach is feasible within the project's constraints
