# TECH — Blocker Oracle

- **Settings**: sparse nested `GoalOracleSettingsLayer` resolved per leaf via
  the standard layered resolver (`project > global > defaults`); provenance
  keys `oracle.*`. maxFailedAttemptsPerBlocker clamped to [1,3].
- **Fingerprint**: sha256 over {goalId, objective hash, contract hash,
  task-tree shape+contracts hash, currentTaskId, NFKC-normalized reason},
  truncated to 24 hex chars. Usage/revision/timestamps excluded.
- **Ledger**: four new event types with validators/sanitizers/reconstruct
  cases; `ReconstructedGoalState.latestOracleResult` carries the bounded last
  disposition.
- **Session**: injectable `createSession` seam (mirrors the auditor);
  tools locked to ["read","grep","find","ls","submit_goal_oracle_advice"];
  structured advice validated at runtime (diagnosis, 1–4 alternatives with
  steps/evidence bounds, recommendedIndex range check, disposition enum).
- **State machine** lives in update_goal(blocked): disabled → legacy path;
  actionable-without-work → re-arm reminder + refuse block;
  needs_human/insufficient_context or failure-limit-reached → block;
  otherwise consult once. Follow-up attempts recorded from the tool_call
  handler when meaningful progress follows armed advice
  (get_goal/echo-reads excluded).
- **Tests**: tests/goal-oracle.test.ts (fingerprint stability, config refusal,
  read-only profile enforcement, invalid-output rejection, per-fingerprint
  reconstruction, actionable + needs-human journey demonstrations).
