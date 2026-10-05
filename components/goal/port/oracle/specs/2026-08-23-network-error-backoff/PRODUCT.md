# Product — network-error recovery backoff

Date: 2026-08-23

## Problem

Pi retries a failed provider request three times. A provider response such as
`Provider finish_reason: network_error` can still exhaust that retry budget,
leaving an active auto-continue goal stranded even though the outage may be
short-lived.

## Behaviour

- Transient provider failures are recoverable: Pi-declared network errors
  plus HTTP 5xx-style outages (503 `server_error`, "Endpoint is unavailable",
  overloaded, gateway failures). 4xx client errors such as auth failures stay
  non-retryable.
- After Pi has fully settled with a transient provider failure, an active,
  auto-continue goal retries its checkpoint on an escalating backoff ladder
  (5, 10, 20, 40, then 80 seconds) that plateaus at the maximum delay.
- The extension waits for `agent_settled`; a transient Pi retry that succeeds
  must clear the pending goal-level recovery instead of creating a duplicate
  turn.
- A recovery retry is cancelled by user-driven input, pausing/stopping or
  changing the focused goal, and session shutdown.
- By default recovery is unbounded: it keeps retrying at the plateau delay
  until the provider recovers. `networkRecovery.maxAttempts` (layered
  settings or `PI_GOAL_NETWORK_RECOVERY_MAX_ATTEMPTS`; 0/unset = unbounded)
  restores a bounded cap; when a configured cap is exhausted the goal remains
  active and the user receives an actionable warning.
- `networkRecovery.maxDelayMs` / `PI_GOAL_NETWORK_RECOVERY_MAX_DELAY_MS`
  caps every single delay (default 80000).
- Non-transient provider errors retain the existing non-retry behaviour.

## Non-goals

- Do not create a rapid retry loop: delays never fall below the ladder
  plateau and every retry is cancellable by user-owned paths.
- Do not persist transient retry counters or change a goal's lifecycle status.
- Do not replace Pi's built-in provider retry policy.
