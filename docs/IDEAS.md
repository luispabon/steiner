> This file is a scratchpad, not a source of truth. Product direction and accepted plans live in `docs/PRD.md`, `docs/ROADMAP.md`, `docs/INITIAL_IMPLEMENTATION_PLAN.md`, and the implemented code.

## Improvements

### Immediate
* Conversation entries for
* Remove "thinking_chunk" by default from app logs. Instead, set a config flag to enable or disable, and disable by default. Eg `logging.thinking_chunk: BOOL`
* System prompt:
  - Right now, embedded on internal/prompt/system.go. Move this to a config file somewhere
  - Make it configurable on the user's config file, per-model. Default to above when not there
  - Look for other hardcoded model prompts (like compaction's) and move into config as well and allow overridding through model config
* Be able to list files on the folder or subfolders bypassing the agent
* Be able to reference a file via @, where @ will trigger auto completion of files on the filesystem relative to the project root
* The conversation pane needs a scrollbar
* Currently, we can't select text at all. Investigate how to enable text selection with the mouse
* The user prompt area should look like opencode's - a box, with a left coloured border (same orange as the headers on the sidebar), a padding of 1 character then the various status messages on the bottom line with their labels on the same orange as the left border

### Mid-term
* Implement approvals using https://github.com/charmbracelet/huh and  look into using it for prompt command auto suggestions
* Look at tools provided by https://github.com/deepnoodle-ai/dive and consider using those over ours
* When pressing ctlr+d or ctrl+c again to exit, a status message or modal dialog should appear asking the user if they're sure
* The --exec mode should not do response streaming, it looks like shit. It should inform that we're waiting for a response, then display it once it's there
* Apply glamour markdown rendering to user prompts
*
### Long-term

* We need to natively integrate with context-mode
* We need to integrate with rtk
* Configurable skills folder(s) location
* Built-in commands for coding loop
* Delegation deferrals (background mode, re-promptable sessions, `touched_files` result field, parallel-sub-agent capability): see `docs/DELEGATION_FUTURE.md`.
* Look into sandboxing for commands (bubblewrap, socat?) like claude and codex https://code.claude.com/docs/en/sandboxing
* I want to add a tool so that a model can request the agent to display a file to the user without the model having to read it first and spit it out
* I would like it so that the /context command behaves like the ? keybind - it's opened in an overlaid modal immediately instead of waiting for the current turn to finish. The information within should display on a table instead of lists of lists
* We need to think about session persistence and resuming sessions
* We need a "plan" and a "build" mode - investigate how to implement (system prompt maybe?). See how it ties up with sandboxing (do that first)
* Consider if we can re-implement core functionality using https://github.com/deepnoodle-ai/dive

* ASCII logo:
▄▖▗   ▘
▚ ▜▘█▌▌▛▌█▌▛▘
▄▌▐▖▙▖▌▌▌▙▖▌
▄█████ ▄▄▄▄▄▄ ▄▄▄▄▄ ▄▄ ▄▄  ▄▄ ▄▄▄▄▄ ▄▄▄▄
▀▀▀▄▄▄   ██   ██▄▄  ██ ███▄██ ██▄▄  ██▄█▄
█████▀   ██   ██▄▄▄ ██ ██ ▀██ ██▄▄▄ ██ ██


## Bugs

### Prompt
* Pressing ESC does not stop streaming and cancel the conversation
* Tool approvals doesn't work, presumably because input is inhibited on the prompt during streaming? We need to stop doing that either way, users should be able to prep their next prompt during streaming anyway
* CTRL+C doesn't work either during an active conversation, possibly same reason as tool approvals being broken

### UI
* Steiner uses substantial CPU during streaming, and in particular if responses stream for a long time (like on video transcripts with thinking blocks), the speed at which tokens appear on screen is lower than the speed we're getting them from the API, to the point that the API finishes while steiner continues adding token by token in the screen for quite a while, sometimes even a minute.
* The "Compacting summarizing context" progress bar never stops even after compaction succeeds. It should change to a full green bar and the label changed to "Compacting finished"

### Sidebar
* When switching models via "/model blah", the host for the new active model doesn't change
* The modified files seems to never update after the application loads. It should load during startup, and after every tool call and turn end

### Compaction
* It seems that the system message that contains the full compacted content is lost on the second API request after compaction. The first request has 4 messages - the proper system prompt, then the AGENTS.md one, then a thurd one named "retained context state" that contains a json payload with a truncated version of the compacted conversation, then a fourth message with the full compacted conversation. But the model responds like the full compacted conversation wasn't there though. After that, the model comes back with some tool calls and at the end of them we send another query to the api containing 3 messages: the first three like on the previous request, and the last one with the full compaction result is missing. Questions:
  * Why is the full compaction result getting lost from the conversation?
  * What's the point of that truncated version of the compaction as a system message named "retained context state"?
* Compaction can break if a tool call results in a lot of data being dumped into the context. We need to check that a tool call response being added to the context does not go over the safety limit, and trigger compaction otherwise

### Messy, do with plenty of Opus juice available
* Markdown view's format seems garbled, things like code blocks with YAML look fine during rendering then just collapse to the left once finished. Also a lot of newlines seem lost - TODO: workaround in place (parse markdown after streaming is finished), not fully fixed

## Implementing

* When modifying a file, steiner should show a colourised diff of the change, this should not involve the model but be entirely agent-side. Code should have syntax-highlighting appropriate to the file type
* When writing a whole new file, steiner should display the file, syntax-highlighted appropriate to the file type
* Is it possible that "glamour", the markdown renderer, or any of the other tui libraries we have already supports this? Or do we need to introduce a new dependency for this? If so, investigate what's the best possible go library we can use for this
