# PRODUCT — Opt-in read-only Blocker Oracle (issue #26)

A cheap executor model can benefit from ONE stronger consultation before
giving up and asking the user. The Oracle is that consultation — heavily
fenced.

## User-visible behavior

Off by default. Enable under `/goal-settings → Blocker Oracle`:

- `oracle enabled` (default false)
- `oracle provider/model` (BOTH must be set explicitly; provider-only is
  refused and the executor model is never used silently)
- `oracle thinking_level`, `oracle project resources` (default off = isolated)
- `max failed attempts per blocker` (1–3, default 2)

When an active goal reports `update_goal({status:"blocked"})` WITH a reason
(now required — it feeds the fingerprint, ledger, and user report):

- If this exact blocker (same normalized reason + same task state) never got
  advice: the Oracle consults once in an isolated read-only session.
- Actionable advice: the goal STAYS ACTIVE and the executor receives concrete
  steps; blocking is refused until a meaningful work attempt against the
  advice is recorded.
- needs_human / insufficient_context: the goal blocks immediately.
- Provider/config failures keep the goal active up to the failure limit;
  beyond it, blocking proceeds with a durable failure annotation.
- Abort or focus change during consultation discards the result safely.

Every phase lands in the ledger (`oracle_started/result/failed/
followup_attempted`) with bounded payloads. Fingerprints ignore usage,
elapsed time, and revision counters.

## Safety

The Oracle session has ONLY read/grep/find/ls plus one structured submit tool.
It cannot mutate files, approve completion, choose focus, or loop: one consult
per fingerprint, hard attempt caps, no unrestricted subagent recursion.
