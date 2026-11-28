# Upstream: `internal/advisor`

Follows omp release tag **`v19.1.1`** (`31ffee513f`).

## Upstream files followed

- `packages/coding-agent/src/slash-commands/builtin-collaboration.ts:63-179` (`/advisor [on|off|toggle|status|dump [raw]|configure]`)
- `packages/coding-agent/src/advisor/settings.ts` (`advisor.enabled`, `advisor.immuneTurns`, `advisor.reviewMode`, `advisor.reviewInterval`, `advisor.maxNotesPerUpdate`, `advisor.continueSeverity`, `advisor.syncBacklog`, `advisor.evictStaleResults`)
- `packages/coding-agent/src/advisor/config.ts` (`WATCHDOG.yml` / `WATCHDOG.yaml` discovery, per-slug merge, tool filtering, edit path resolution)
- `packages/coding-agent/src/advisor/watchdog.ts` (`collectConfigCandidates`, `discoverWatchdogFiles`)
- `packages/coding-agent/src/advisor/emission-guard.ts` (`normalizeAdvisorNote`, `SUPPRESSED_NORMALIZED_PHRASES`, rank-aware session dedupe, per-update non-blocker budget with blocker exemption)
- `packages/coding-agent/src/advisor/advise-tool.ts` (`formatAdvisorBatchContent`, `<advisory>` XML framing with `guidance="weigh, don't blindly obey"`, `isAdvisorInterruptImmuneTurnActive`, `AdviseTool`)
- `packages/coding-agent/src/advisor/runtime.ts` (`quarantineAdvisorUnsafeOutput`, background review execution, quota error handling)
- `packages/coding-agent/src/advisor/transcript-recorder.ts` (`__advisor.jsonl` / `__advisor.<slug>.jsonl` persistence and cost loading)
- `packages/coding-agent/src/session/session-advisors.ts:3149-3231` (`formatAdvisorStatus`, `formatAdvisorHistoryAsText`)
- `packages/coding-agent/src/prompts/advisor/system.md` and `advise-tool.md`

## Stdlib-only & PiG SDK adaptations

- **YAML parser**: `ParseWatchdogYAML` parses omp's `WATCHDOG.yml` subset with Go stdlib only (no third-party YAML library) to honour zero external dependencies.
- **Child-only `advise` tool**: `advise` is never registered on primary sessions; it is passed solely via `sdk.CreateAgentSessionOptions.CustomTools` to advisor child sessions (or via the `SpawnAdvisorSubagent` hook).
- **Advisory delivery**: Pending `<advisory>` notes are injected on the next primary turn via `EventBeforeAgentStart` (hidden custom message `advisor-notes`) instead of omp's live mid-stream steer channel (PiG SDK does not expose mid-turn advisor steering).
- **Cost & history persistence**: Recorded via stdlib JSONL scanning/appending in `<sessionDir>/__advisor[.<slug>].jsonl` (no SQLite) and published to Tern's composer footer via `surface.SetComposerMeta` (`AdvisorCost`).
- **`/advisor configure`**: Prints resolved project/user `WATCHDOG.yml` paths, active files, warnings, and a starter schema template instead of opening omp's custom TUI overlay.
- **Default off**: `advisor.enabled` defaults to `false`; when disabled, zero background calls, zero `<advisory>` injections, and zero extra tools are created.
