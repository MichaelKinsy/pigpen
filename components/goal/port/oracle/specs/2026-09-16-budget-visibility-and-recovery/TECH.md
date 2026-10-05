# Implementation

Extend the existing proposal schema and confirmation/report text. Capture the target focus/revision before the dialog; flush buffered mutations and reject stale confirmation before writing through GoalService. Add a goal_budget_changed ledger event with nullable old/new limits and consumed usage. Persist budget and lifecycle together. Recover stopped goals by declaring ready through GoalScheduler, preserving spent allowance; foreign/interrupted/claimed scheduling remains stopped. Clear obsolete budget reminders.

Coalesce crossed thresholds per accounting update and key warning tracking by goal and budget. Add real SDK Responses captures before transmission for registered execution/drafting schemas with omitted/false/true compatibility. Document supported-version reproduction and the model override. Run full tests, typecheck, lint, context and provider checks; baseline changes require recorded rationale.
