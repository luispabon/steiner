> This file is a scratchpad, not a source of truth. Product direction and accepted plans live in `docs/PRD.md`, `docs/ROADMAP.md`, `docs/INITIAL_IMPLEMENTATION_PLAN.md`, and the implemented code.

 * We need to be able to report on how full the context is somehow
 * We need to natively integrate with context-mode
 * We need to integrate with rtk
 * Expand config to add more than one model that can be switched on the fly via /model
 * If context fill can't be inferred or queried from the model's API, we need to be able to estimate it ourselves. The model's config should be able to specify the context size. We should be able to estimate how much context we're shoving into each request to the model.
 * System prompt:
  - Right now, embedded on internal/prompt/system.go. Move this to a config file somewhere
  - Make it configurable on the user's config file, per-model. Default to above when not there
 * Delegation deferrals (background mode, re-promptable sessions, `touched_files` result field, parallel-sub-agent capability): see `docs/DELEGATION_FUTURE.md`.
 * Look into sandboxing for commands (bubblewrap, socat?) like claude and codex https://code.claude.com/docs/en/sandboxing
