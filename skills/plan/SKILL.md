---
name: plan
description: Plan coding work as a compact implementation-step bundle with a conservative research decision, high-level overview first, and planning artifacts written only under .steiner/plans/YYYY-MM-DD_FEATURE_NAME/. Use when invoked by name or when asked to plan a feature, refactor, migration, or other code change. If only the skill name is given, ask for a high-level description first before writing planning files.
---

# Coding Loop Planner

## Overview

Use this skill to turn a coding request into a traceable planning bundle. Write planning artifacts only under `.steiner/plans/YYYY-MM-DD_FEATURE_NAME/`, where `FEATURE_NAME` is a short filesystem-safe slug for the task.

Planning creates intent, constraints, verification strategy, and a flat list of implementation steps. It must not edit implementation files.

The planner never implements. Its work ends at handoff. After delivering the handoff sentence, the planner's only remaining action is to call `workflow_handoff` with `next: implement` and `target: .steiner/plans/FEATURE`, then stop.

## Phases

Follow this sequence:

1. Intake
2. Clarification
3. Understanding checkpoint
   ▸ GATE — user approval required before proceeding
4. Research decision
   ▸ GATE — user approval required before proceeding
5. Optional research
6. Verification strategy discovery
7. Overview checkpoint
   ▸ GATE — user approval required before proceeding
8. Implementation-step planning
9. Handoff
   ▸ STOP — planner ceases all activity after delivering handoff and workflow_handoff

Do not write planning artifacts before the research decision is resolved.

## Planning Artifacts

Allowed planning artifacts:

- `overview.md`: contains `## Request`, `## Overview`, `## Key Decisions`, `## Tradeoffs`, `## Scope Boundaries`, `## Verification Strategy`, and `## Decision Log`
- `plan.yaml`: a flat implementation-step plan
- `research.md`, `research_001.md`, etc.: research artifacts, only when research runs

Do not write artifacts outside `.steiner/plans/YYYY-MM-DD_FEATURE_NAME/`.

The planner owns the loop feature branch for planning only. Before writing the first planning artifact, create or check out `cl/YYYY-MM-DD_FEATURE_NAME`. The branch will later be reused by the implementer, reviewer, and closer — but the planner's role ends at handoff.

Before writing the first planning artifact, check whether `.steiner/plans/` is gitignored by running `git check-ignore -q .steiner/plans/`. If exit code is 0, planning artifacts are local-only — do not stage or commit them at any point during this workflow. If exit code is non-zero, planning artifacts are version-controlled — commit them as described below.

Planning is execution-ready only when `overview.md` and `plan.yaml` exist under `.steiner/plans/YYYY-MM-DD_FEATURE_NAME/` on `cl/YYYY-MM-DD_FEATURE_NAME`, and the planner has delivered the handoff sentence. When planning artifacts are version-controlled, the latest planning artifacts must also be committed. The user — not the planner — decides when to proceed to implementation.

## Intake And Clarification

If the user only invokes the skill name or gives no actionable task, ask for the minimum high-level description needed before doing anything else.

Once there is an actionable task, explore nearby code and repo instructions for facts that can answer obvious questions. Do not ask the user for information that local inspection can discover.

Do not ask implementation-detail questions before the overview checkpoint unless they materially affect scope, architecture, or research.

### Clarification style

For straightforward, repo-local work, ask targeted clarification questions when genuine uncertainty exists. Do not fabricate questions to satisfy a quota.

For ambiguous, domain-heavy, architectural, hard-to-reverse, terminology-sensitive, high-risk, or tradeoff-heavy work, switch to grill mode:

- ask one high-impact question at a time
- provide the recommended answer or default with each question
- wait for the user's answer before asking the next question
- challenge vague or overloaded terms by proposing a precise meaning
- test important assumptions with concrete scenarios
- call out contradictions between the user's description and discovered code
- stop grilling once the planner can state the goal, success criteria, scope boundaries, constraints, risks, and key tradeoffs clearly

### Understanding checkpoint

▸ GATE — user approval required before proceeding to the research decision.

After clarification, present a brief understanding summary to the user covering:

- **Goal**: what the task achieves
- **Assumptions**: anything taken as given that the user did not explicitly state
- **Scope**: what is in and what is out
- **Unknowns**: open questions or areas of uncertainty

If any assumptions or unknowns warrant clarification, ask targeted questions about them. Do not ask generic questions like "does this look right?" — ask about the substance. Do not fabricate questions when none are warranted; the summary itself gives the user enough to correct misalignment.

Do **not** proceed to the research decision until the user explicitly confirms the understanding is correct. No implicit assent.

## Research Decision

Make the research decision before `overview.md`.

Research is required by default when the task depends on information that may be current, external, or fast-moving, including:

- external APIs, SDKs, providers, model behavior, or product behavior
- third-party dependencies, framework behavior, or CLI/tool behavior
- security-sensitive behavior, compliance-sensitive behavior, or published best practices
- unfamiliar, domain-specific, risky, or low-confidence areas
- uncertainty that could materially change scope, risks, acceptance criteria, or implementation steps

Research may be skipped when the task is repo-local, stable, and sufficiently understood from nearby code and repository instructions.

When making the research decision, tell the user:

- current understanding
- whether research is required, recommended, or not needed
- brief reasons
- the exact next choice

If research is not needed, offer the user the choice to continue without research or trigger research anyway. **Do not proceed** until the user explicitly responds. No implicit assent.

If research is required or recommended, summarize the questions research will answer. **Do not proceed** until the user explicitly approves. No implicit assent.

## Research Delegation

Research happens before `overview.md`, never after it.

If research is approved, the planner must delegate it. Do not replace the research phase with the planner's own reasoning.

Call Steiner's `research` tool with exactly one self-contained `task`. If `research` is unavailable, report that and ask whether to continue without research or use the best available fallback.

The research task must be tight and include:

- the exact questions to answer
- relevant user intent, constraints, and known decisions
- relevant paths, files, packages, APIs, or docs already known
- expected output format
- non-goals and scope boundaries

The delegated researcher is read-only. Research is complete when the delegated result has been received and reviewed by the planner.

If a persisted research artifact is useful, the planner writes `research.md` or `research_001.md` from the delegated result. Do not require the researcher to write files.

After writing a research artifact, communicate findings to the user:

1. Present an inline summary (3-5 bullets) covering key findings, implications, and any surprises or risks discovered.
2. Call `display_file` on the research artifact so the user can review the full detail.

The inline summary drives the conversation forward; the displayed file is the detailed reference. Do not skip either step.

## Research Output Contract

Research artifacts should use these sections:

- `## Question`
- `## Findings`
- `## Implications`
- `## Risks and Uncertainties`
- `## Sources`
- `## Open Questions`

Keep research scoped to the questions that matter for planning.

## Verification Strategy

Before writing `overview.md`, discover the repository verification strategy once and record it in `overview.md`.

Keep discovery shallow and evidence-driven. Prefer:

1. repo instructions and agent docs
2. root task runners and manifests
3. CI configuration
4. relevant subproject manifests

Record likely formatter, lint, type-check, test, build, and repo-mandated commands. Note whether each command is cheap, medium, or expensive, and whether safe fix mode should be preferred over check-only mode.

Executor and reviewer should consume this section instead of rediscovering verification commands by default.

## Overview Checkpoint

**STOP — approval required.** Write `overview.md` only after clarification, research decision, any approved research, and verification discovery are complete.

The overview must include:

- `## Key Decisions`: decisions made or assumed during clarification and research, with brief rationale for each
- `## Tradeoffs`: alternatives considered and why they were rejected or deferred
- `## Scope Boundaries`: what is explicitly in scope and what is out, so the user can catch misalignment early

These sections give the user concrete material to react to, not just a summary of intent.

Call `display_file` with the overview path and `limit: 1000` to show the entire file to the user.

After showing the overview, drive a targeted discussion:

1. Identify any open decisions or tradeoffs that need user input and ask specific questions about them.
2. If there are no open decisions, highlight the most consequential choices made and invite feedback.
3. Do not ask generic questions like "does this look good?" — ask about the substance.

Remain in the checkpoint phase until all open items are resolved AND the user gives explicit approval ("approve," "looks good," "go ahead," or equivalent). Do **not** write `plan.yaml` until then. No implicit assent, no exceptions.

If the user asks questions, proposes changes, or gives partial feedback, continue the discussion. Do not proceed to `plan.yaml` until you receive explicit approval.

## Plan Format

`plan.yaml` must be a flat implementation-step plan. Do not use stage/step nesting.

Use this top-level YAML shape:

```yaml
steps:
  - id: step-1
    title: ...
    scope: ...
    files: []
    constraints: []
    acceptance: []
    verification: []
```

Each step should include:

- `id`
- `title`
- `scope`
- `files`
- `constraints`
- `acceptance`
- `verification`

Optional fields:

- `depends_on`: only when a real dependency exists
- `parallel_group`: only when parallel execution is safe and worth the coordination cost
- `delegate_profile`: `explore`, `research`, `code`, `plan`, `verify`, or `delegate`

Keep steps large enough to be worth delegating. Avoid atomizing work into tiny mechanical steps.

Serial execution is the default. Parallel execution is exceptional and must be explicitly justified by independence and coordination value.

## Delegation Guidance

Use Steiner's specialised model-facing tools by name:

- `explore` for read-only codebase discovery
- `research` for current, external, or codebase research
- `code` for bounded implementation edits
- `plan` for bounded sub-problem analysis
- `verify` for check-only verification
- `delegate` only when no specialised profile fits

Every Steiner delegated task must be self-contained and include relevant context already known by the main agent. Do not make delegated agents rediscover context unnecessarily.

If an advisor tool is available, use it as a brief steering check before freezing an
approach on architectural or tradeoff-heavy work.

## Handoff

**Mandatory end-of-work.** If planning artifacts are version-controlled, commit the final planning artifacts on `cl/YYYY-MM-DD_FEATURE_NAME`. Deliver the exact handoff sentence below, then call `workflow_handoff` with `next: implement` and `target: .steiner/plans/FEATURE`. Do not imply the implement workflow has already started. Do not offer to implement, delegate, review, or continue.

`Please run /clear then /implement .steiner/plans/FEATURE on an empty context.`

If the user accepts the handoff, context is cleared and the next workflow starts in the new session. If the user dismisses it, the tool returns a declined result and you must not assume continuation.
