# Steiner Context

This file records repo-specific terms for `steiner` so architecture work can name seams consistently.

## Language

**Interactive Session**:
The long-lived interactive mode that owns conversation state, approvals, model switches, compaction, and run lifecycle.
_Avoid_: interactive mode, UI controller, chat loop

**Context Report**:
An interactive-mode view of the most recent assembled model request and its token budget.
_Avoid_: debug dump, prompt preview

**Manual Compaction**:
A user-triggered rewrite of the current conversation into a smaller retained form during an **Interactive Session**.
_Avoid_: reset, clear, summarize

**Delegation Bootstrap**:
The setup flow that turns a delegation request into a child agent run request, including child prompt, child tool visibility, limits, and execution policy.
_Avoid_: delegate glue, child setup, sub-agent scaffolding

**Turn Progression**:
The single-turn state transition in the agent loop that assembles context, fits budget, handles compaction, executes the model call, applies tool calls, and advances conversation state.
_Avoid_: runner internals, turn helper, loop step

**Prompt Source Planning**:
The prompt-assembly step that decides which context sources are included, in what order, and how budgets apply before provider messages are rendered.
_Avoid_: prompt glue, assembler internals, message ordering logic

**Tool Execution Pipeline**:
The tool-execution flow that resolves a tool, validates input, applies approval policy, executes the tool, decodes output, and shapes the final result or error.
_Avoid_: executor internals, tool glue, subprocess path

**Provider Request Execution**:
The provider flow that shapes a model request, performs HTTP execution, decodes provider wire responses, and returns stream or non-stream results with consistent error handling.
_Avoid_: provider internals, transport glue, stream path

## Relationships

- An **Interactive Session** can emit a **Context Report**
- An **Interactive Session** can trigger **Manual Compaction**
- **Manual Compaction** updates the conversation owned by an **Interactive Session**
- A **Delegation Bootstrap** produces the child run request used to execute a delegated task
- **Turn Progression** advances the conversation owned by the agent runner by one turn
- **Prompt Source Planning** decides the ordered context inputs consumed during **Turn Progression**
- The **Tool Execution Pipeline** executes tool calls consumed during **Turn Progression**
- **Provider Request Execution** performs the model calls consumed during **Turn Progression**

## Example dialogue

> **Dev:** "Should the TUI build the **Context Report** itself?"
> **Domain expert:** "No, the **Interactive Session** should own it because it already owns the latest assembled request."

> **Dev:** "Where should child tool filtering and the child prompt live?"
> **Domain expert:** "Inside the **Delegation Bootstrap**, because callers should not rebuild child agent rules by hand."

> **Dev:** "Should compaction be separate from the turn logic?"
> **Domain expert:** "No, keep it in **Turn Progression** until it has a real life outside the single-turn transition."

> **Dev:** "Where should prompt ordering and budget decisions live?"
> **Domain expert:** "Inside **Prompt Source Planning**, so the assembler is not just a pile of append calls and positional assumptions."

> **Dev:** "Should approval and subprocess handling be separate modules already?"
> **Domain expert:** "No, keep them inside the **Tool Execution Pipeline** until there is a real need to split execution paths."

> **Dev:** "Should streaming and non-streaming provider calls live in separate architectures?"
> **Domain expert:** "No, keep them inside **Provider Request Execution** and share one request path unless the wire behavior truly forces divergence."

## Flagged ambiguities

- "interactive mode" and "session" were both used for the same concept; resolved: use **Interactive Session** for the owning module and runtime concept.
- "scaffold", "child setup", and "delegate glue" were used for one concept; resolved: use **Delegation Bootstrap** for the module that assembles child agent execution.
- "turn helper", "runner step", and "one turn" were used loosely; resolved: use **Turn Progression** for the owning module of single-turn advancement.
- "assembler", "source ordering", and "prompt planning" were used interchangeably; resolved: use **Prompt Source Planning** for the module that decides ordered prompt inputs before rendering.
- "executor", "approval flow", and "tool execution path" were used loosely; resolved: use **Tool Execution Pipeline** for the module that owns end-to-end tool invocation.
- "provider transport", "stream path", and "request execution" were used loosely; resolved: use **Provider Request Execution** for the module that owns end-to-end provider request handling.
