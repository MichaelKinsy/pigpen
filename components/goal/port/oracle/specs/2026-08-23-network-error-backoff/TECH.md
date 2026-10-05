# Technical — network-error recovery backoff

Date: 2026-08-23

`GoalRuntime` owns an in-memory, one-timer network-recovery state keyed to the
focused goal. It derives delay from an exported pure policy helper
(`networkErrorBackoffPlan(attempt, policy)`) and, when the timer fires, uses
the existing checkpoint continuation mechanism.

Recovery policy defaults to unbounded: delays escalate along
`NETWORK_ERROR_BACKOFF_DELAYS_MS`, plateau at `maxDelayMs` (default 80000),
and retry forever. `networkRecovery.maxAttempts` (layered settings in
`goal-settings.ts`, resolved via global/project files and
`PI_GOAL_NETWORK_RECOVERY_MAX_ATTEMPTS`; 0/unset = unbounded) restores a
bounded cap that yields no plan after exhaustion. `goal-events.ts` loads the
resolved settings per settle and passes the policy into the runtime.

`goal-events.ts` classifies assistant errors using `stopReason`, `rawStopReason`,
and `errorMessage`. Transient-failure classification lives in `goal-format.ts`
(`TRANSIENT_PROVIDER_ERROR_RE`): Pi-declared network errors plus HTTP
5xx-style provider outages (`server_error`, 502/503/504/529, "Endpoint is
unavailable", overloaded, gateway failures). On such an error it records a
pending recovery during
`agent_end`, but schedules only from `agent_settled`. Any successful
`agent_end` clears that state, thereby allowing Pi's own retry loop to win
without extension interference.

The retry state is deliberately ephemeral. `clearContinuationState()` accepts
an internal opt-out used while the hidden retry checkpoint starts, preserving
the consecutive-error counter across recovery attempts. All user-visible
cancellation paths use the default reset behaviour.

Coverage includes error classification, deterministic delay/cap policy, and
lifecycle-level deferral/cancellation behaviour.
