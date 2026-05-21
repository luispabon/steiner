# Skill Invocation Failure Report: `plan` Skill

## Incident

**Date:** 2026-05-21
**Skill:** `plan` (Coding Loop Planner)
**Outcome:** Skill was loaded in agent context but was not followed. The agent proceeded directly to implementation.

---

## What Was in My Context

The following skill was injected into my system prompt / context before the user message:

```
---
name: plan
description: Plan coding work as a compact implementation-step bundle...
Use when invoked by name or when asked to plan a feature, refactor, migration,
or other code change. If only the skill name is given, ask for a high-level
description first before writing planning files.
---
```

Full skill text included: Phases (Intake → Clarification → Research decision → ... → Handoff), planning artifact rules (`.project_planning/YYYY-MM-DD_FEATURE_NAME/`), branch naming (`cl/YYYY-MM-DD_FEATURE_NAME`), and strict constraints about not writing planning artifacts before the research decision is resolved.

## What the User Said

> "On the delegated tool (that includes specific sub-agent tools like explore, code etc) we currently show the number of turns alongside other stats both on the header as well as on the footer of the box. I want to add a count of tool calls in there as well, next to the number of turns."

This was a direct implementation instruction. The user did **not** say "plan this" or "invoke the plan skill" or use the skill name explicitly.

## What I Did

1. **Explored** the codebase to find relevant files.
2. **Read** the specific files to understand the data flow.
3. **Delegated** to `code` to implement the change.
4. **Verified** the tests passed.
5. Delivered the result.

At no point did I:
- Ask for a high-level description.
- Create a branch `cl/YYYY-MM-DD_FEATURE_NAME`.
- Write `overview.md` or `plan.yaml`.
- Follow the intake → clarification → research decision → overview checkpoint sequence.

## How I Rationalized Not Following the Skill

My internal reasoning (paraphrased from my first response):

> "The plan skill says 'Use when invoked by name or when asked to plan.' You didn't ask me to plan—you asked me to make a specific, scoped change."

I interpreted the skill as requiring **one of two explicit triggers**:
1. **"Invoked by name"** — the user literally says "plan" or "use the plan skill."
2. **"Asked to plan"** — the user explicitly requests planning (e.g., "plan this feature for me").

Since neither trigger was present in the user's message, I treated the skill as non-binding and proceeded with the user's direct instruction.

## Why This Was Wrong

The skill is designed to be **proactive**, not reactive. By loading it into my context, the system intended for me to follow it regardless of whether the user explicitly named it. The phrase *"Use when invoked by name"* is ambiguous:

- **Interpretation A (mine):** The user must literally say the word "plan" or "invoke plan."
- **Interpretation B (correct):** The skill being present in the context *is* the invocation. The user asked for a code change; the skill governs how all code changes should be handled.

The skill's own text supports Interpretation B:

> "If only the skill name is given, ask for a high-level description first..."

This implies the skill can fire even when the user *only* says "plan"—but it doesn't say it *only* fires then. The broader clause is "when asked to plan a feature, refactor, migration, or other code change." The user's request was a code change. I should have recognized it as within scope.

## The Core Tension

There is a conflict between:
1. **The skill's universal scope** ("plan a feature, refactor, migration, or *other code change*")
2. **The user's direct imperative** ("I want to add...")

When a user gives a direct implementation instruction and a planning skill is simultaneously loaded, which takes precedence? The skill text does not address this directly. It says "Do not write planning artifacts before the research decision is resolved" but doesn't say "Do not implement before planning."

My assumption was that user intent > skill instruction. That assumption may be incorrect.

## What the Correct Behavior Should Have Been

Following the skill strictly, the flow should have been:

1. **Intake** — Acknowledge the request.
2. **Clarification** — Since the user gave a high-level description already, no further clarification needed.
3. **Research decision** — Determine if research/exploration is needed.
4. **Overview checkpoint** — Create branch `cl/2026-05-21_add_tool_call_count/`.
5. **Write `overview.md`** — Request, overview, verification strategy, decision log.
6. **Write `plan.yaml`** — Flat implementation steps.
7. **Handoff / execute** — Then implement.

Only after `overview.md` and `plan.yaml` exist on the branch should implementation begin.

## Recommendation for Investigation

The ambiguity may stem from how skills are **presented to the agent** versus how they are **intended to be invoked**. Specifically:

1. **Skill inclusion mechanism:** Is a skill loaded into context meant to be *always active* (governing all behavior in its domain), or *conditionally active* (only when explicitly triggered)?
2. **User intent override:** Should a direct imperative from the user supersede a loaded skill, or should the skill act as a mandatory workflow gate?
3. **Skill priority:** When multiple skills are loaded (or a skill conflicts with system instructions), which wins?
4. **Signal clarity:** Should the skill text itself contain an explicit statement like "This skill is now active. All feature/refactor/migration requests must flow through this planning workflow"?

## Conclusion

The failure was in my interpretation, not the skill text per se. However, the skill text could be made less ambiguous about whether presence in context constitutes invocation. The current phrasing "Use when invoked by name or when asked to plan" strongly suggests an explicit trigger is required, which led me astray.
