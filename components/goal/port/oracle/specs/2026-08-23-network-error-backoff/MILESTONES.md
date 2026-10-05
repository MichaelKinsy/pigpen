# Milestones — network-error recovery backoff

## 2026-08-23 — Design

- Identified that `agent_end` precedes Pi's built-in retry settlement, so the
  goal extension must defer its fallback to `agent_settled`.
- Chose a bounded 5-step exponential ladder (5–80 seconds), preserving the
  existing protection from runaway provider-error loops.

## 2026-08-23 — Implementation and validation

- Added network-error classification from Pi assistant error metadata and a
  pure bounded-delay policy.
- `agent_end` records a network recovery candidate; `agent_settled` schedules
  it, so a later success in Pi's own retry loop clears the candidate instead.
- Recovery timers are unref'd and cancelled by every ordinary continuation
  reset, including user-driven turns, focus/lifecycle changes, and shutdown.
- Validation passed: `npm run check`, `npm run lint`, `npm test` (837 tests),
  `npm run test:integration` (31 tests), `npm pack --dry-run`, and
  `git diff --check`.

## 2026-08-25 — Regression fix: 503 outages never engaged recovery; unbounded default

- User report: repeated `Error: 503: {"type":"server_error","message":"Error
  from provider (Console): Upstream request failed: Endpoint is unavailable."}`
  followed by "Retry failed after 3 attempts" left the goal stranded with no
  goal-level backoff.
- Root cause: `isNetworkErrorAssistantMessage` matched only the literal text
  `network error` in `rawStopReason`/`errorMessage`. The 503 `server_error`
  payload contains neither string, so `goal-events.ts` routed it down the
  generic-error path (persist only, no recovery scheduled).
- Classification broadened: `TRANSIENT_PROVIDER_ERROR_RE` now matches HTTP
  5xx-style transient failures (502/503/504/529, `server_error`,
  service unavailable, bad gateway, gateway timeout, upstream request
  failed, endpoint is unavailable, overloaded). Auth/malformed-request 4xx
  errors remain non-retryable.
- Behavior change (user-directed): recovery is UNBOUNDED by default — it
  keeps retrying on the ladder, plateauing at `maxDelayMs` (default 80s),
  instead of giving up after five attempts.
- Settings: new layered `networkRecovery.{maxAttempts,maxDelayMs}` leaves
  (global/project `pi-goal-x-settings.json` plus
  `PI_GOAL_NETWORK_RECOVERY_MAX_ATTEMPTS` / `PI_GOAL_NETWORK_RECOVERY_MAX_DELAY_MS`
  env overrides), following the existing oracle-settings conventions.
  `maxAttempts: 0`/unset = unbounded; bounded caps still exhaust with the
  actionable resume-hint warning.
- Notification wording distinguishes bounded (`recovery 1/5`) from
  unbounded (`recovery 1, unbounded`) progress.
- New suite `tests/goal-network-recovery.test.ts`: exact-payload regression
  through the real `agent_end` → `agent_settled` lifecycle, escalation past
  the old 5-attempt cap, configured-cap exhaustion hint, success resets the
  counter, plus classification unit coverage. Fast lifecycle tests shrink
  delays via `PI_GOAL_NETWORK_RECOVERY_MAX_DELAY_MS`.
- Validation passed: `npm run check`, full `npm run test:serial`.

## 2026-08-25 (later) — "unlimited retry still not working" report: verified fixed in real runtime; added live e2e guard

- Follow-up user report: same `503 server_error / Endpoint is unavailable` payload,
  goal stayed active with no retry notification after Pi's retries exhausted.
- Built a REAL-runtime reproduction: local HTTP mock always returning the exact
  reported payload, isolated `PI_CODING_AGENT_DIR`, custom provider via
  `models.json`, seeded active autoContinue goal (`autoSelectSingleGoal`),
  driving a genuine `pi --mode rpc` subprocess with this repo's extension.
- Captured real pi 0.84.3 event shapes during outage: intermediate failures emit
  `agent_end` (willRetry=true) without surfacing messages; final exhaustion emits
  an assistant message with `stopReason:"error"` and
  `errorMessage = '503: {"type":"server_error","message":"Error from provider
  (Console): Upstream request failed: Endpoint is unavailable."}'`, then exactly
  one `agent_settled`. Classification matched every cycle.
- Observed end-to-end success with current code: "Retrying the goal in 5s
  (recovery 1, unbounded)" → checkpoint continuation delivered → turn restarted
  → escalation through recovery 2/10s, 3/20s, 4/40s across consecutive cycles.
- Root cause of the field report: the failing session predated the 0.30.3 fix
  (extensions load once per session; the old bounded/classifier code was still
  in memory). No product defect found in the current tree.
- Hardening: new `tests/e2e/network-recovery-rpc.test.ts` automates this whole
  scenario against a real `pi` subprocess (~20s; skips when `pi` is absent):
  asserts ≥2 escalating unbounded recovery notifications, checkpoint delivery
  after settle, warning level, and counter start. Manifest regenerated.

## 2026-08-25 (evening) — second field report: provider-initiated aborts paused goals; 429s never classified

- New field evidence showed two distinct failure shapes the previous fix missed:
  1. `Goal paused.` after repeated 503s: an assistant message arrives with
     `stopReason:"aborted"` (TUI renders "Aborted after N retry attempts") and
     the agent_end handler paused the goal on ANY aborted message, even when
     no user abort signal fired — misattributing provider-side termination to
     user intent.
  2. A second outage shaped as OpenRouter-style
     `429: {"message":"Provider returned error",... "temporarily rate-limited
     upstream..."}`: TRANSIENT_PROVIDER_ERROR_RE had no 429/rate-limit
     patterns, so those errors silently never scheduled recovery.
- Captured real shapes via the rpc harness with a 429 mock: final failure is
  `stopReason:"error"` + `errorMessage:"429 status code ..."`; recovery was
  never scheduled (0 notify events) before the fix.
- Fixes:
  - Classification adds `\b429\b`, `rate-limited`/`rate limit`,
    `too many requests`; new NON_TRANSIENT exclusion list (quota/billing/
    GoUsageLimitError etc.) wins over transient matches so deterministic
    billing failures still fail fast.
  - agent_end now pauses ONLY when `ctx.signal.aborted` is true (genuine user
    abort). Aborted assistant messages without a user signal route into the
    same bounded recovery as classified transient errors.
- E2E harness generalized into parameterized outage scenarios; new live-pi
  scenario proves sustained 429 outages now produce escalating unbounded
  recovery notifications and checkpoint continuations.
- Unit coverage: exact field-reported 429 payload classifies transient;
  quota-flavored 429 stays non-transient; provider-initiated abort engages
  recovery; user-signal abort still pauses.

### Audit correction (same day)

- Independent audit correctly flagged two gaps in the first attempt:
  1. `message_end` and `turn_end` still paused on ANY aborted message and fire
     BEFORE `agent_end`, so the agent_end-only fix was ineffective in the real
     event flow. All three handlers are now signal-aware: only
     `ctx.signal.aborted === true` (genuine user Esc) pauses; provider-side
     aborts route into recovery. A dedicated regression test drives the FULL
     real ordering (`message_end → turn_end → agent_end → agent_settled`)
     with an aborted message and no user signal.
  2. Attribution correction per pi 0.84.3 source: `retryAssistantCall` returns
     `stopReason:"error"` on retry EXHAUSTION; `stopReason:"aborted"` with
     retryAttempt > 0 ("Aborted after N retry attempts") marks a user abort
     during the backoff sleep. The field report's "Goal paused." was therefore
     most plausibly a genuine abort during the outage — but since transport-
     level terminations can also surface as aborts without a user signal, the
     signal-aware rule keeps goals alive through outages while preserving
     explicit-Esc semantics.
- The 429 classification fix and its live-pi e2e scenario were verified sound
  by the audit and are unchanged.
