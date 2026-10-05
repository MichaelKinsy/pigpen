# Independent unfocused prompt control (#72)

Add `hideUnfocusedPrompt` (default false) to the existing layered settings and Goal behavior menu. When true, ordinary unfocused sessions receive no model-facing `[PI GOAL UNFOCUSED]` reminder. `hideUnfocusedBanner` remains UI-only and independent.

Preserve focused-goal context, stale-continuation rejection, focus ownership, scheduling, and saved conversation history. Correct singular/plural grammar in the reminder when it is enabled.

Validate settings parsing, precedence, persistence, live changes, repeated requests, independent UI controls, focused states, stale checkpoints, and unchanged goal/focus state through actual registered hooks.

Integrate the independent setting in the maintainer stabilization PR; preserve the established UI-only banner contract.
