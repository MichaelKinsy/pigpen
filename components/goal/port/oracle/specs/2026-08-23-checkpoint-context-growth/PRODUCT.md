# PRODUCT — Bounded continuation checkpoints and existing-session recovery

## Problem (issue #30)

Every auto-continue turn persisted a full continuation prompt (~6.4K chars:
objective, task list, verification contract, lifecycle policy) as a custom
session message. A reported session accumulated 851 checkpoint messages
(850 byte-identical), ~5.4 MB, growing the session file every turn and
inflating provider context and compaction input.

## User-visible behavior after this change

- Auto-continue still works exactly as before: each finished turn of an active,
  auto-continue goal triggers a hidden follow-up that starts the next turn.
- The persisted follow-up is now a tiny structured checkpoint marker
  (≤160 chars) plus minimal metadata. The objective, task tree, contracts,
  budget, and policy text are never written into checkpoint messages.
- The model still receives the complete authoritative goal state once per turn
  through the system prompt (`before_agent_start`). Nothing about what the
  model may do changes.
- Normal provider requests contain at most one historical checkpoint marker;
  all older markers are filtered out by the extension's `context` hook.
- Sessions created by older versions remain fully parseable; legacy full
  checkpoints are treated as historical and filtered from provider context.

## Recovery for already-affected sessions

`/goal-recovery` and `/goal-status health` report checkpoint health for the
current session file: total checkpoints, legacy full checkpoints, bytes spent
on checkpoint content, and projected size after recovery.

A shipped offline CLI repairs an existing oversized session file:

    pi-goal-x-recover --session <session.jsonl>            # dry-run report
    pi-goal-x-recover --session <session.jsonl> --apply --confirm-pi-closed

Safety rules:

- Dry-run is the default; nothing is written without `--apply`.
- `--apply` is refused unless `--confirm-pi-closed` is passed. Close Pi first:
  rewriting the session behind Pi's in-memory SessionManager is unsafe.
- A timestamped backup copy is created before any replacement.
- Entry ids, parent links, line count, and the session header are unchanged.
- Non-goal lines (and even malformed lines) are preserved byte-identically.
- Only legacy checkpoint content/details are rewritten to the v2 marker form.
- Recovery is idempotent: running it twice reports zero further changes.

## Out of scope

- Changing when turns trigger or how auto-continue is scheduled.
- Any change to goal lifecycle semantics (stale checkpoints still cannot call
  work tools; completion/blocking policy unchanged).
