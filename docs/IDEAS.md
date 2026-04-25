> This file is a scratchpad, not a source of truth. Product direction and accepted plans live in `docs/PRD.md`, `docs/ROADMAP.md`, `docs/INITIAL_IMPLEMENTATION_PLAN.md`, and the implemented code.

## Improvements

* We need to natively integrate with context-mode
* We need to integrate with rtk
* System prompt:
  - Right now, embedded on internal/prompt/system.go. Move this to a config file somewhere
  - Make it configurable on the user's config file, per-model. Default to above when not there
* Delegation deferrals (background mode, re-promptable sessions, `touched_files` result field, parallel-sub-agent capability): see `docs/DELEGATION_FUTURE.md`.
* Look into sandboxing for commands (bubblewrap, socat?) like claude and codex https://code.claude.com/docs/en/sandboxing
* I want to add a tool so that a model can request the agent to display a file to the user without the model having to read it first and spit it out
* We keep an example config file at dist/config.yaml documenting every single option available
* I would like it so that the /context command behaves like the ? keybind - it's opened in an overlaid modal immediately instead of waiting for the current turn to finish. The information within should display on a table instead of lists of lists
* The conversation pane needs a scrollbar
* We need to think about session persistence and resuming sessions
* The user prompt area should look like opencode's - a box, with a left coloured border (same orange as the headers on the sidebar), a padding of 1 character then the various status messages on the bottom line with their labels on the same orange as the left border
* Currently, we can't select text at all. Investigate how to enable text selection with the mouse

* When pressing ctlr+d or ctrl+c again to exit, a status message or modal dialog should appear asking the user if they're sure
* The --exec mode should not do response streaming, it looks like shit. It should inform that we're waiting for a response, then display it once it's there
* We need a "plan" and a "build" mode - investigate how to implement (system prompt maybe?). See how it ties up with sandboxing (do that first)
* Consider re-implementing using https://github.com/deepnoodle-ai/dive
* Apply glamour markdown rendering to user prompts


## Bugs

* The modified files seems to never update after the application loads. It should load during startup, and after every tool call and turn end
* Markdown view's format seems garbled, things like code blocks with YAML look fine during rendering then just collapse to the left once finished. Also a lot of newlines seem lost - TODO: workaround in place (parse markdown after streaming is finished), not fully fixed
* When tool calls fail, currently the app gets in a weird state where it doesn't tell the model about it and it just waits for a model response forever or until we cancel
* When we send a message, and we're waiting for the API to start returning, we need some sort of spinner or indication that stuff is happening
* On the approval tool, the A for ALWAYS doesn't work, it denies the tool run


* Investigate this tool failure:
  === 2026-04-25T20:36:36.318980453Z tool_call_started ===
{
  "turn": 1,
  "tool": "bash",
  "call_id": "932496587",
  "arguments": {
    "command": "gh pr create --title \"feat: comprehensive TUI redesign and feature expansion\" --body \"This PR implements an extensive overhaul of the Steiner TUI, following a multi-stage design plan. Key improvements include:\n\n#### 🎨 UI/UX Redesign (TUI)\n* **New Design System:** Implemented an OKLCH color system with extended themes and design tokens.\n* **Sidebar Overhaul:** Introduced a new card-based sidebar layout including repository information, modified files tracking, model/context status, and brand identity.\n* **Enhanced Interaction:** Added mouse support for collapsing/expanding tool and thinking segments, command palette overlay (\\`Ctrl+P\\`), and Esc-key interrupts for input.\n* **Improved Content Rendering:** Redesigned user bubbles, assistant markdown styling, and tool call rows with collapsible cards, tag pills, approval indicators, and diff blocks.\n* **Status Bar Updates:** Added status bar key chips and a \\`/model\\` switch segment for easier navigation.\n\n#### 🛠️ Feature Enhancements\n* **Search \u0026 Context:** Improved the search tool with result limits, directory exclusions, and an increased buffer; expanded project budget management.\n* **Persistence:** Implemented file-based navigation for prompt history persistence.\n* **Configuration \u0026 Settings:** Added a new \\`prefs\\` package supporting accent presets and model switching via \\`/model\\`.\n* **Logging:** Introduced an enabled flag in the logging configuration.\n\n#### ⚙️ Refactors \u0026 Maintenance\n* Refactored TUI content rendering logic to support the new multi-stage design.\n* Updated provider configurations (e.g., Qwen) and cleaned up accidental binary commits.\""
  }
}

=== 2026-04-25T20:36:36.730608853Z tool_call_finished ===
{
  "turn": 1,
  "tool": "bash",
  "call_id": "932496587",
  "error": "tool execution failed: bash: command_failed: exit status 1: exit=1"
}

=== 2026-04-25T20:36:36.730695836Z stop_reason ===
{
  "reason": "error",
  "turn": 1,
  "error": "tool execution failed: bash: command_failed: exit status 1: exit=1",
  "summary": "run failed",
  "action": "inspect the reported error and retry"
}

=== 0001-01-01T00:00:00Z stop_reason ===
{
  "reason": "Error: tool execution failed: bash: command_failed: exit status 1: exit=1"
}



## Implementing

* When modifying a file, steiner should show a colourised diff of the change, this should not involve the model but be entirely agent-side. Code should have syntax-highlighting appropriate to the file type
* When writing a whole new file, steiner should display the file, syntax-highlighted appropriate to the file type
* Is it possible that "glamour", the markdown renderer, or any of the other tui libraries we have already supports this? Or do we need to introduce a new dependency for this? If so, investigate what's the best possible go library we can use for this
