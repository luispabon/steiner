---
name: plan
description: Plan coding work as a compact implementation-step bundle with a conservative research decision, high-level overview first, and planning artifacts written only under .project_planning/YYYY-MM-DD_FEATURE_NAME/. Use when invoked by name or when asked to plan a feature, refactor, migration, or other code change. If only the skill name is given, ask for a high-level description first before writing planning files.
---

# Coding Loop Planner

## Overview

Use this skill to turn a coding request into a traceable planning bundle. Write planning artifacts only under `.project_planning/YYYY-MM-DD_FEATURE_NAME/`, where `FEATURE_NAME` is a short filesystem-safe slug for the task.

Planning creates intent, constraints, verification strategy, and a flat list of implementation steps. It must not edit implementation files.

The planner never implements. Its work ends at handoff. After delivering the handoff sentence, take no further action.

## Phases

Follow this sequence:

1. Intake
2. Clarification
3. Research decision
   ▸ GATE — user approval required before proceeding
4. Optional research
5. Verification strategy discovery
6. Overview checkpoint
   ▸ GATE — user approval required before proceeding
7. Implementation-step planning
8. Handoff
   ▸ STOP — planner ceases all activity after delivering handoff

Do not write planning artifacts before the research decision is resolved.

## Planning Artifacts

Allowed planning artifacts:

- `overview.md`: contains `## Request`, `## Overview`, `## Verification Strategy`, and `## Decision Log`
- `plan.yaml`: a flat implementation-step plan
- `research.md`, `research_001.md`, etc.: research artifacts, only when research runs

Do not write artifacts outside `.project_planning/YYYY-MM-DD_FEATURE_NAME/`.

The planner owns the loop feature branch for planning only. Before writing the first planning artifact, create or check out `cl/YYYY-MM-DD_FEATURE_NAME`. The branch will later be reused by the implementer, reviewer, and closer — but the planner's role ends at handoff.

Planning is execution-ready only when `overview.md` and `plan.yaml` exist on `cl/YYYY-MM-DD_FEATURE_NAME`, the latest planning artifacts have been committed, and the planner has delivered the handoff sentence. The user — not the planner — decides when to proceed to implementation.

## Intake And Clarification

If the user only invokes the skill name or gives no actionable task, ask for the minimum high-level description needed before doing anything else.

Once there is an actionable task, explore nearby code and repo instructions for facts that can answer obvious questions. Do not ask the user for information that local inspection can discover.

Clarify until you can reliably determine:

- goals, constraints, assumptions, and likely code areas
- external dependencies or current information needs
- risks and open questions
- whether research is required
- whether the request is understood well enough to produce a reliable overview

Do not ask implementation-detail questions before the overview checkpoint unless they materially affect scope, architecture, or research.

Use the lightest clarification style that is safe for the task.

For straightforward, repo-local work, ask only the questions needed to avoid a misleading overview.

For ambiguous, domain-heavy, architectural, hard-to-reverse, terminology-sensitive, high-risk, or tradeoff-heavy work, switch to grill mode:

- ask one high-impact question at a time
- provide the recommended answer or default with each question
- wait for the user's answer before asking the next question
- challenge vague or overloaded terms by proposing a precise meaning
- test important assumptions with concrete scenarios
- call out contradictions between the user's description and discovered code
- stop grilling once the planner can state the goal, success criteria, scope boundaries, constraints, risks, and key tradeoffs clearly

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

**STOP — approval required.** Write `overview.md` only after clarification, research decision, any approved research, and verification discovery are complete. Present `overview.md` to the user. Do **not** write `plan.yaml` until the user explicitly approves the overview. No implicit assent, no exceptions.

If the user asks questions, proposes changes, or gives partial feedback, remain in the checkpoint phase. Do not proceed to `plan.yaml` until you receive an explicit "approve," "looks good," "go ahead," or equivalent.

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

## Handoff

**Mandatory end-of-work.** Commit the final planning artifacts on `cl/YYYY-MM-DD_FEATURE_NAME`. Deliver the exact handoff sentence below, then take no further action. Do not offer to implement, delegate, review, or continue.

`Please run /clear then /implement .project_planning/FEATURE on an empty context.`
