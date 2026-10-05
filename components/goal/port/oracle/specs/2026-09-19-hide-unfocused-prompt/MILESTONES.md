# Milestones

1. Layered `hideUnfocusedPrompt` setting (parse, resolve, persist, report, menu) — done.
2. Gate the ordinary unfocused reminder in `currentGoalContext`; stale-checkpoint and focused-goal paths untouched — done.
3. Hook-level regressions: hidden on repeated requests, live toggle, banner independence, stale rejection preserved, no goal/focus mutation — done.
4. Singular/plural grammar fix and README entry — done.
5. Draft PR with the open design question (separate setting vs. widening `hideUnfocusedBanner`) — pending maintainer input.

- 2026-09-22: Integrated in the issue/PR stabilization branch with default false, layered settings and existing settings UI. Full integration validation is recorded in ../2026-09-22-issue-and-pr-resolution-plan/VALIDATION.md.
