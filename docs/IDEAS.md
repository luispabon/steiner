> This file is a scratchpad, not a source of truth. Product direction and accepted plans live in `docs/PRD.md`, `docs/ROADMAP.md`, `docs/INITIAL_IMPLEMENTATION_PLAN.md`, and the implemented code.

 * We need to natively integrate with context-mode
 * We need to integrate with rtk
 * System prompt:
  - Right now, embedded on internal/prompt/system.go. Move this to a config file somewhere
  - Make it configurable on the user's config file, per-model. Default to above when not there
 * Delegation deferrals (background mode, re-promptable sessions, `touched_files` result field, parallel-sub-agent capability): see `docs/DELEGATION_FUTURE.md`.
 * Look into sandboxing for commands (bubblewrap, socat?) like claude and codex https://code.claude.com/docs/en/sandboxing
 * I want to add a tool so that a model can request the agent to display a file to the user without the model having to read it first and spit it out
 * Automatically load .steiner/config.yaml if present
 * We keep an example config file at dist/config.yaml documenting every single option available
 * The conversation pane needs a scrollbar
 * Markdown view's format seems garbled, things like code blocks with YAML look fine during rendering then just collapse to the left once finished. Also a lot of newlines seem lost - TODO: workaround in place (parse markdown after streaming is finished), not fully fixed
 * I would like it so that the /context command behaves like the ? keybind - it's opened in an overlaid modal immediately instead of waiting for the current turn to finish. The information within should display on a table instead of lists of lists
 * Currently, we're counting towards context-fill things that aren't actually in the context, specifically the safety margin and the completion reserve. Why? They should not be. See:

```markdown
Last Request Context
Model: gemma-4-26b-a4it@iq4_nl
Prompt tokens: 1682

## Categories

* request framing: 8
  1. request framing overhead ( 8 )
* system preamble: 171
  1. system preamble ( 171 )
* global AGENTS.md: 0
* project AGENTS.md: 470
  1. /home/luis/Projects/AI/steiner/AGENTS.md ( 470 )
* project context files: 531
  1. /home/luis/Projects/AI/steiner/README.md ( 531 )
* enabled skills: 0
* durable context: 0
* conversation summary blocks: 0
* conversation messages: 29
  1. user #1: sup homey ( 8 )
  2. assistant #2 ( 21 )
* tool result / tool summary blocks: 92
  1. tool #1 bash: {"command":"ls -F","cwd":"/home/luis/Projects/AI... ( 92 )
* tool definitions: 381
  1. bash ( 70 )
  2. edit ( 88 )
  3. glob ( 57 )
  4. read ( 49 )
  5. search ( 47 )
  6. write ( 70 )

Completion reserve: 4096
Safety margin: 16384
Estimated total request tokens: 22162 / 65536
```
