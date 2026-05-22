# Feedback on the `plan` Skill — Workflow Gaps That Allowed Premature Action

## What Went Wrong

During a planning session for "caveman mode," the planner agent:
1. Wrote `plan.yaml` **before** receiving explicit user approval of `overview.md`.
2. Offered to proceed with **implementation** instead of handing off to an implementer.
3. Committed planning artifacts while still awaiting user approval.

## Root Causes in the Skill Text

### 1. "Overview Checkpoint" reads as polite guidance, not a hard stop

Current:
> Present the overview to the user and wait for explicit approval before writing `plan.yaml`.

Problem: "wait for" sounds like a suggestion. The agent interpreted it as "I can proceed if the user seems to assent implicitly."

Suggested:
> **STOP — approval required.** Present `overview.md` to the user. Do **not** write `plan.yaml` until the user explicitly approves the overview. No exceptions.

---

### 2. The phase list does not visually gate planning

Current:
> 6. Overview checkpoint  
> 7. Implementation-step planning

Problem: Looks like a straight checklist. No visual barrier between 6 and 7.

Suggested:
> 6. Overview checkpoint  
> **🚫 GATE: Await explicit user approval of `overview.md`. Do not proceed past this point without it.**  
> 7. Implementation-step planning (only after gate clears)

---

### 3. No rule for what to do during the pause

Add under "Overview Checkpoint":
> If the user asks questions, proposes changes, or gives partial feedback, remain in the checkpoint phase. Do not proceed to `plan.yaml` until you receive an explicit "approve" or equivalent.

---

### 4. Handoff reads as optional or loosely worded

Current:
> Commit the final planning artifacts on `cl/YYYY-MM-DD_FEATURE_NAME`.  
> Use this handoff sentence exactly as written, with only the planning folder path substituted: `Please run /clear then /implement .project_planning/FEATURE on an empty context.`

Problem: "Use this handoff sentence" is soft. The agent conflated "owning the branch" with "doing the implementation."

Suggested:
> **Mandatory end-of-work.** After committing, deliver the exact handoff sentence below and take no further action. Do not offer to implement, delegate, review, or continue.
>
> `Please run /clear then /implement .project_planning/FEATURE on an empty context.`

Also add a sentence in the Phases list after step 8:
> The planner never implements. After committing the final artifacts, deliver the exact handoff sentence and stop.

---

### 5. "The planner owns the loop feature branch" is ambiguous

Current:
> The planner owns the loop feature branch. Before writing the first planning artifact, create or check out `cl/YYYY-MM-DD_FEATURE_NAME`. Use that same branch for planning, implementation, review, and closeout.

Problem: "Use that same branch for... implementation" implies the planner does implementation.

Suggested:
> The planner owns the loop feature branch for planning only. Before writing the first planning artifact, create or check out `cl/YYYY-MM-DD_FEATURE_NAME`. The branch will later be reused by the implementer, reviewer, and closer — but the planner's role ends at handoff.

---

## Summary of Proposed Changes

| Location | Change |
|----------|--------|
| Phases list | Insert visual gate between step 6 and 7 |
| Overview Checkpoint | Harden "wait for" into "STOP — approval required" |
| Overview Checkpoint | Add rule for feedback during the pause |
| Handoff section | Add "Mandatory end-of-work" and "never implement" |
| Branch ownership paragraph | Clarify planner only owns planning phase |
