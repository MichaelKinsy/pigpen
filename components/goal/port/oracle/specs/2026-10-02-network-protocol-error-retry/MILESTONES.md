# Milestones — network recovery for HTTP/2 PROTOCOL_ERROR

## Provenance

PR #84 by `rogeecn` (Rogee), one commit imported unchanged via a maintainer merge commit on `incoming/pr-84`:

- `5438851` fix: retry HTTP/2 protocol errors

The maintainer commit adds only the CHANGELOG entry and this spec directory. Commits cannot be added to the pull request from outside the contributor's fork, so the contributor's commit is included here unchanged instead; the branch is otherwise ready to merge.

## Defect evidence on `main` (verified 2026-10-02)

The transient classifier list in `extensions/goal-format.ts` covered network errors, 429/502/503/504/529, rate limiting, and gateway text, but not HTTP/2 `PROTOCOL_ERROR`:

```
$ rg "PROTOCOL_ERROR|protocol_error" node_modules/@earendil-works/pi-ai/dist node_modules/@earendil-works/pi-coding-agent/dist/core
(no matches)
```

## Validation on the branch tip

- `npx tsc --noEmit` — clean.
- `npx eslint .` — clean.
- `node scripts/run-unit-tests.mjs` — 1043/1043 pass.
- `node scripts/run-unit-tests.mjs integration` — 31/31 pass (same as `main`).
- `node scripts/run-unit-tests.mjs e2e` — 20/20 pass.
- `npm run context:gate` — PASS (24 fixtures, no baseline re-measurement needed).

The contributor's report of 1036 unit tests across 44 suites was measured against an older `main`; 1043 is the current count, with the new classification test included.

## Design-philosophy audit

- New user command: none.
- New setting: none.
- Retry policy, backoff parameters, or scheduler-state change: none. The fix reuses the existing recovery policy in `extensions/network-error-backoff.ts`.
- Prompt growth: none. `extensions/prompts/` is untouched and the context gate passes against the unmodified baseline.
- Filesystem complexity: none.
- Host assumptions: unchanged. No new pi API is used; the fix widens a string classifier only.
- README, `specs/SPECS.yaml`, `package.json`, and the host range: untouched.
- Smallest effective change: yes. One pattern and one regression test, both tied to an observable defect.

## Accepted consequences

- The pattern is deliberately broad: any error text containing "protocol error" becomes retryable. The exposure is an application-level "protocol error" (for example a build complaining about its own protocol) being retried once through the existing bounded backoff. Quota and billing text is still checked first and never retried.
- The regression test asserts classification only. Asserting that recovery engages would be a strictly stronger test; it is not a merge blocker because the classifier is the only thing that changed.