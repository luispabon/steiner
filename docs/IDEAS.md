> This file is a scratchpad, not a source of truth. Product direction and accepted plans live in `docs/PRD.md`, `docs/ROADMAP.md`, `docs/INITIAL_IMPLEMENTATION_PLAN.md`, and the implemented code.

 * We need to natively integrate with context-mode
 * We need to integrate with rtk
 * System prompt:
  - Right now, embedded on internal/prompt/system.go. Move this to a config file somewhere
  - Make it configurable on the user's config file, per-model. Default to above when not there
 * Delegation deferrals (background mode, re-promptable sessions, `touched_files` result field, parallel-sub-agent capability): see `docs/DELEGATION_FUTURE.md`.
 * Look into sandboxing for commands (bubblewrap, socat?) like claude and codex https://code.claude.com/docs/en/sandboxing
 * I want to add a tool so that a model can request the agent to display a file to the user without the model having to read it first and spit it out
  * We keep an example config file at dist/config.yaml documenting every single option available
 * The conversation pane needs a scrollbar
 * Markdown view's format seems garbled, things like code blocks with YAML look fine during rendering then just collapse to the left once finished. Also a lot of newlines seem lost - TODO: workaround in place (parse markdown after streaming is finished), not fully fixed
 * I would like it so that the /context command behaves like the ? keybind - it's opened in an overlaid modal immediately instead of waiting for the current turn to finish. The information within should display on a table instead of lists of lists
  * We need to think about session persistence and resuming sessions
  * The user prompt area should look like opencode's - a box, with a left coloured border (same orange as the headers on the sidebar), a padding of 1 character then the various status messages on the bottom line with their labels on the same orange as the left border
 * prompt history should be available from session to session. Two sessions in parallel should write to the same history file at ~/.config/steiner/history.log without clobbering each other
 * Looks like AGENTS.md is truncated when sent via the API. Also, investigate how that should work - should we wrap the agents.md file into something like "These are the repo's instructions for coding agents at AGENTS.md: ...."?
