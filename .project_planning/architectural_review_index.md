# Architectural Review Index

This file tracks the architectural review subjects for `steiner` and the recommended execution order.

## Subjects

1. [Subject 1](/home/luis/Projects/AI/steiner/.project_planning/architectural_review_subject_1.md:1) — **Interactive Session**
2. [Subject 2](/home/luis/Projects/AI/steiner/.project_planning/architectural_review_subject_2.md:1) — **Delegation Bootstrap**
3. [Subject 3](/home/luis/Projects/AI/steiner/.project_planning/architectural_review_subject_3.md:1) — **Turn Progression**
4. [Subject 4](/home/luis/Projects/AI/steiner/.project_planning/architectural_review_subject_4.md:1) — **Prompt Source Planning**
5. [Subject 5](/home/luis/Projects/AI/steiner/.project_planning/architectural_review_subject_5.md:1) — **Tool Execution Pipeline**
6. [Subject 6](/home/luis/Projects/AI/steiner/.project_planning/architectural_review_subject_6.md:1) — **Provider Request Execution**

## Recommended execution order

### Main-path architecture order

1. [Subject 4](/home/luis/Projects/AI/steiner/.project_planning/architectural_review_subject_4.md:1) — clean the prompt seam first
2. [Subject 3](/home/luis/Projects/AI/steiner/.project_planning/architectural_review_subject_3.md:1) — then deepen the core loop around the cleaner prompt seam
3. [Subject 5](/home/luis/Projects/AI/steiner/.project_planning/architectural_review_subject_5.md:1) — then deepen tool execution behind a stable executor surface
4. [Subject 6](/home/luis/Projects/AI/steiner/.project_planning/architectural_review_subject_6.md:1) — then unify provider request behavior behind the stable provider surface
5. [Subject 2](/home/luis/Projects/AI/steiner/.project_planning/architectural_review_subject_2.md:1) — then deepen delegation bootstrap, which is relatively independent
6. [Subject 1](/home/luis/Projects/AI/steiner/.project_planning/architectural_review_subject_1.md:1) — leave interactive-session refactor until the underlying seams are cleaner

### Urgency-biased order

Use this if delegated execution becomes important sooner than interactive cleanup:

1. [Subject 2](/home/luis/Projects/AI/steiner/.project_planning/architectural_review_subject_2.md:1)
2. [Subject 4](/home/luis/Projects/AI/steiner/.project_planning/architectural_review_subject_4.md:1)
3. [Subject 3](/home/luis/Projects/AI/steiner/.project_planning/architectural_review_subject_3.md:1)
4. [Subject 5](/home/luis/Projects/AI/steiner/.project_planning/architectural_review_subject_5.md:1)
5. [Subject 6](/home/luis/Projects/AI/steiner/.project_planning/architectural_review_subject_6.md:1)
6. [Subject 1](/home/luis/Projects/AI/steiner/.project_planning/architectural_review_subject_1.md:1)

## Recommendation

Preferred order:

- `4 -> 3 -> 5 -> 6 -> 2 -> 1`

Alternative when delegation urgency is higher:

- `2 -> 4 -> 3 -> 5 -> 6 -> 1`
