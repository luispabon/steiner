# Overview: README restructure + configuration reference

## Request

Restructure README.md as the primary human entry point for steiner. Add a new `docs/CONFIGURATION.md` full reference. Add documentation-maintenance instructions to CLAUDE.md so coding agents keep docs in sync when features change.

Goals:
- Short intro + differentiation vs other coding agents
- Fast path to running (quickstart leads)
- Minimal config examples in README, complete reference in docs/
- Clear "how it works" section covering agent loop, tools, context management, delegation
- Structure README sections to map clearly to code areas

## Overview

### README.md restructure

New structure:

1. **Title + intro** — 2-line description
2. **Why steiner** — bullet differentiation: local-first, context management as core design, delegation-first, transparent approval gates, provider-agnostic
3. **Quickstart** — ≤5 steps, fast path to running
4. **Usage** — interactive mode, `--exec`, command reference table
5. **Configuration** — 2 minimal examples (local LLM, OpenRouter cloud), pointer to `docs/CONFIGURATION.md`
6. **Built-in tools** — brief table mapping tool name → purpose
7. **Context management** — 3-paragraph overview + link to `docs/CONTEXT_MANAGEMENT.md`
8. **Sub-agent delegation** — overview + link to `docs/SUBAGENT_DELEGATION.md`
9. **Optional features** — caveman mode (condensed), web search (condensed)
10. **Development** — build, test, check

### docs/CONFIGURATION.md (new file)

Full configuration reference:

- Every provider field documented (`type`, `base_url`, `api_key`, `api_key_env`, `headers`, `timeout`)
- Every model field documented (`provider`, `id`, `params`, `extra_params`, `prompt_suffix`, `retry`, `prompts`, `advanced.limits`)
- Every top-level config block: `default_model`, `limits`, `approval`, `tools`, `sub_agent`, `search`, `logging`, `paths`, `project_context`
- Minimal examples: local LLM, cloud (OpenRouter), multi-provider
- Full examples: tuned model with params, advanced limits, full kitchen-sink with every block populated

### CLAUDE.md additions

New `## Documentation maintenance` section with rules:
- Which README section to update when built-in tools change (`internal/tool`)
- Which doc to update when config fields change (`docs/CONFIGURATION.md` + README config section)
- Which doc to update when sub-agent types change (`docs/SUBAGENT_DELEGATION.md` + README)
- Which doc to update when context management changes (`docs/CONTEXT_MANAGEMENT.md` + README)

## Verification Strategy

No build or test commands needed — pure documentation change.

Post-edit verification:
- Read README.md to confirm structure and no broken links
- Read docs/CONFIGURATION.md to confirm every config block/field is present
- Read CLAUDE.md to confirm maintenance rules are coherent with existing agent instructions

## Decision Log

- Keeping abundant config examples in `docs/CONFIGURATION.md`, not README — README gets minimal/representative examples only
- README sections named to match code package areas to help agents find the right section to update
- Not moving CONTEXT_MANAGEMENT.md or SUBAGENT_DELEGATION.md — they are already well-structured; README links to them
