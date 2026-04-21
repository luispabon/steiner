> This file is a scratchpad, not a source of truth. Product direction and accepted plans live in `docs/PRD.md`, `docs/ROADMAP.md`, `docs/INITIAL_IMPLEMENTATION_PLAN.md`, and the implemented code.

 * Console must support response streaming, currently waits until model response is done to display.
 * Console must understand markdown and display appropriately.
 * Console must have a colour theme, always dark-mode, and hopefully swappable coloud themes. We should get the most popular from the internet (eg darkula, catpuccin, etc)
 * Console must behave like an actual console: history prompt when clicking up and down, be able to delete words or letters, be able to use left or right to move around the current prompt
 * We need to be able to report on how full the context is somehow
 * We need to natively integrate with context-mode
 * We need to integrate with rtk
