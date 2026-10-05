---
issue: 933
issue_title: "pi-permission-system: a config warning present at session start is never shown"
---

# A config warning present at session start reaches the operator

## Release Recommendation

**Release:** ship independently

`docs/architecture/architecture.md`'s open-issue sweep records [#933] as "filed by [#927]'s implementation; out of scope for the roadmap" — it belongs to no numbered step and no `Release: batch`.
It is a `config/` notification-lifecycle defect, so it cuts its own `fix:` release.

## Problem Statement

Every config issue `loadAndMergeConfigs` produces — the permissive-bash-fallback warning, the deprecated-preview-cap notice, and a refused `permissionDialogKeys` binding — reaches the debug log and nothing else.

The cause is a dedupe against a notification nobody received.
`src/index.ts:241` primes the store with `configStore.refresh(undefined, false)` so it holds a config before the first handler runs.
`ConfigStore.refresh` joins `mergeResult.issues` into one `warning` string, records `this.lastConfigWarning = warning`, and then calls `ctx?.ui.notify(warning, "warning")` — a no-op, because the priming call has no ctx.
`SessionLifecycleHandler.handleSessionStart` then calls `refreshConfig(ctx, projectTrusted)` with a real ctx, produces the identical string, and the dedupe swallows it.

**What the operator sees** (reproduced through the real composition root; the spike is not retained): a global config of `{"permission": {"*": "allow"}}` — the exact shape `detectPermissiveBashFallback` exists to flag — then `session_start` and `before_agent_start` with a `hasUI: true` ctx whose `ui.notify` is captured.
Measured: **`ui.notify` called 0 times.**
The warning appears only in `logs/pi-permission-system-debug.jsonl` under `config.loaded`, and only when `debugLog` is enabled.

The swallowed class is precisely the issues that are identical under global-only and full merge: every global-scope issue, plus the three cross-cutting detectors whenever no project config contributes.
A warning caused by project config *does* differ from the priming string and is shown today — which is why the defect reads as intermittent.

## Goals

- A config issue present before the session starts is warned once, to the session that has a UI to show it in.
- A config issue that first appears mid-session is warned on the next turn, because the config is re-read on every `before_agent_start`.
- The dedupe's real job survives: an unchanged issue is not re-announced on every turn.
- `ConfigStore` stops owning a notification lifecycle and a UI side-effect.
  It loads and answers what is wrong with the config; something else decides whether the operator has been told, and something else syncs the status bar.
- No behavior change rides the fix: one notification per report, as today; the two preparatory tidyings are behavior-preserving and land as their own `refactor:` commits ahead of it.

Not breaking.
No config key, schema field, default, or output shape changes; the only observable delta is that a warning the operator was supposed to see now arrives.

## Non-Goals

- **Headless delivery.**
  When `ExtensionRunner.hasUI()` is false, `ctx.ui` is pi's `noOpUIContext` (`../../pi/packages/coding-agent/src/core/extensions/runner.ts:530`), so a non-interactive session has no notify channel at all and the debug log stays its only record.
  The issue's Expected scopes this out: "shown once, to the session that has a UI to show it in."
- **The sibling accumulation.**
  `SessionLifecycleHandler` warns `resolver.getConfigIssues(agentName)` — `FilePolicyLoader`'s per-file schema issues — once at `session_start`, unlatched, while policy is re-read from file mtimes on any turn.
  That is the same defect class, and the operator's principle for this fix ("we refresh mid-session, so we must keep the user informed") applies to it verbatim.
  It is kept out of this fix only to keep the fix tight, and is filed as [#953] — which the reporter's `ConfigIssueSource` seam is shaped to make cheap.
- **A review-log record for config issues.**
  `AuthorizerChainAudit` writes both a durable review entry and a latched warning; this change adds only the warning, leaving `config.loaded`'s debug entry as the record it is today.
  See Open Questions.
- **No change to detector logic** or to which issues `loadAndMergeConfigs` produces.
- **No change to `ConfigStore.save`'s modal path**, including its own `ctx.ui.notify` error report on a failed write and its status sync on a command ctx.
  The command handed it that ctx; it is correctly scoped.
- **The priming call itself stays.**
  `index.ts:236-240` documents why it must run after `session` is assigned (a debug-write IO failure routes through `session.notify`) and why it withholds the project scope (no trust decision exists yet).
  Both reasons survive; the fix removes the notification decision from the load, not the load.
- **A `warnOnce` primitive on `SessionLogger`.**
  This will be the package's fourth hand-rolled tell-once latch (`PermissionSessionLogger.reported`, `AuthorizerChainAudit`, `ChildNodeAudit`).
  The logger is not touched by this change, so tidying it does not make this change easier; the fifth instance pays for the primitive.

## Background

### The modules

| Module                              | Role in this change                                                                                               |
| ----------------------------------- | ----------------------------------------------------------------------------------------------------------------- |
| `src/config/config-loader.ts`       | `loadAndMergeConfigs` returns `{ merged, issues }`; three pure detectors append to `issues`                       |
| `src/config/config-store.ts`        | `ConfigStore` owns `config` + `lastConfigWarning`, notifies, syncs status, writes the `config.loaded` log         |
| `src/config/status.ts`              | `syncPermissionSystemStatus(ctx, config)` — pure over `ctx.ui.setStatus`                                          |
| `src/index.ts`                      | Composition root; primes the store at line 241 and builds both driving handlers at 308/315                        |
| `src/handlers/lifecycle.ts`         | `SessionLifecycleHandler.handleSessionStart` — refresh, reset, log paths, warn policy issues, activate            |
| `src/handlers/session-turn-prep.ts` | `SessionTurnPrep.prepare` — warm parser, activate, refresh config, announce ready; runs **every turn**            |
| `src/logging/session-logger.ts`     | `SessionLogger.warn(message)` → injected notify sink → `PermissionSession.notify`                                 |
| `src/session/permission-session.ts` | Owns `context` (set by `activate`); `notify(message)` writes to `this.context?.ui.notify`; relays `refreshConfig` |

### Who owns an operator-facing warning today

`grep -rn "ui\.notify" packages/pi-permission-system/src` returns 10 hits (measured).
Eight are a *command* ctx (`config-modal.ts`, `ConfigStore.save`) or the gate ctx (`permission-gate-handler.ts:94`) — correctly scoped to a call the operator just made.
One is `PermissionSession.notify` (`permission-session.ts:98`), the package's session-lifecycle notify sink: `SessionLogger.warn(msg)` → the injected sink → `this.context?.ui.notify`.
Every other session-lifecycle warning routes through it — `UNTRUSTED_PROJECT_MESSAGE`, the `resolver.getConfigIssues()` loop, `AuthorizerChainAudit`, `ChildNodeAudit`, and log-IO failures.

`ConfigStore.refresh:122` is the **only** session-lifecycle warning that reaches `ctx.ui.notify` directly.
That reach is the defect's enabling condition: it made a ctx *parameter* the delivery channel, so a call without one silently consumed the message.
The same parameter also gates `syncPermissionSystemStatus` (lines 113-115) — a second UI side-effect coupled to the load through an optional ctx.
That one has no latch, so it never bit, but it is the same shape.

In both existing audits the latch sits with the thing that knows what "the same alarm" means (per configured name; per absent child) and delivery goes through `logger.warn`.
A latch in `ConfigStore` is therefore not per se misplaced — bypassing `logger.warn` is.

### The ordering the fix would otherwise inherit

`handleSessionStart` (`lifecycle.ts:52-56`) calls `refreshConfig(ctx, …)` **before** `resetForNewSession(ctx, …)`, and `resetForNewSession` (`permission-session.ts:111-118`) is what calls `activate(ctx)`, which sets `this.context`.
So at the `refreshConfig` call, `session.notify` is a no-op on a fresh session — a debug-write IO failure during that refresh is swallowed today for the same reason the warning is.

That order is pinned by `lifecycle.test.ts` "calls refreshConfig before resetForNewSession".
Traced: the pin arrived with the handler decomposition as a pure-refactor characterization test, was carried through #331, #341, and #644 (whose plan says only "preserved"), and no plan or ADR records a reason for it.
Every side effect of `resetForNewSession` was read: `configureForCwd` builds a `FilePolicyLoader` and reads no extension config; `activate` builds the path normalizer, starts forwarding (reads `hasUI`), and selects the authorizer — whose chain is read per ask in `linksFor`, not at activation.
**Spike, measured:** swapping the two lines fails exactly that one characterization test and leaves 4522 tests green, including every composition-root end-to-end test.
Tidying 1 below inverts the pin.

### `before_agent_start` fires on submit, not at startup

`AgentSession` emits `before_agent_start` after pushing the user message (`../../pi/packages/coding-agent/src/core/agent-session.ts:1390-1406`), so it runs once per agent run — not when the session opens.
Driving the report from turn prep alone would delay a startup warning until the operator's first prompt.
Both moments are needed.

### AGENTS.md constraints that apply

- **Keep scope tight** — this is a bug fix; the sibling accumulation is filed, not folded.
- **Prefer config patterns over new runtime mechanisms** — no new config key; the new collaborator replaces a field, it does not add a knob.
- Directory vocabulary (`.pi/skills/package-pi-permission-system/SKILL.md`): the new module's subject is config issues, so it goes in `config/`, and its name must be added to the architecture doc's `config/` tree listing when it is written.
- Within the package, `./` names a same-directory module and `#src/` a cross-directory one; both are lint-enforced with auto-fix.

## Design Overview

Two behavior-preserving tidyings first, then the change they make easy.

### Tidying 1: activate before refresh

`handleSessionStart` reorders to `resetForNewSession` → `refreshConfig`, matching `SessionTurnPrep.prepare`'s existing `activate` → `refreshConfig`.
After this, every `refreshConfig` call in the package runs with `session.context` set, so anything delivered through `logger.warn` from the config path has somewhere to go — and the two drivers have the same shape.
The characterization pin is inverted to `calls resetForNewSession before refreshConfig`, with the reason in the test name's neighbor comment: delivery through `session.notify` needs the context `activate` sets.

### Tidying 2: status sync leaves the load

`PermissionSession.refreshConfig(ctx, projectTrusted)` — the point both drivers already call — takes over the `ctx.hasUI`-gated `syncPermissionSystemStatus(ctx, this.configStore.current())`.
`ConfigStore.refresh` loses the status-sync block (113-115).
It keeps its `ctx` parameter for now, because after this tidying the notify at line 122 is the *only* thing that reads it — the smell is fully exposed, and step 6 deletes the parameter once the notify is gone.
The session owns the context lifecycle, so it is the right owner of a UI side-effect keyed on that context; putting the two lines in each handler would duplicate them (the #746 rule).

### The change: the store answers, a reporter tells

```ts
// src/config/config-issue-reporter.ts

/** The config seam the reporter reads (ISP): current issues, nothing else. */
export interface ConfigIssueSource {
  getConfigIssues(): readonly string[];
}

/** The log seam the reporter writes (ISP): one operator-facing warning. */
export interface ConfigIssueWarner {
  warn(message: string): void;
}

/** The seam the two handlers drive. */
export interface ConfigIssueReporting {
  report(): void;
}

export class ConfigIssueReporter implements ConfigIssueReporting {
  private reported: ReadonlySet<string> = new Set();

  constructor(
    private readonly source: ConfigIssueSource,
    private readonly log: ConfigIssueWarner,
  ) {}

  report(): void {
    const current = this.source.getConfigIssues();
    const fresh = current.filter((issue) => !this.reported.has(issue));
    if (fresh.length > 0) this.log.warn(fresh.join("\n"));
    this.reported = new Set(current);
  }
}
```

### Latch semantics: latch per issue, deliver per report

| Sequence of reports | Warned                                                |
| ------------------- | ----------------------------------------------------- |
| `[A]`               | `A`                                                   |
| `[A]`, `[A]`        | `A` once                                              |
| `[A, B]`            | one message, `A\nB`                                   |
| `[A]`, `[A, B]`     | `A`, then `B` only                                    |
| `[A]`, `[]`, `[A]`  | `A`, then `A` again (the set dropped it)              |
| `[]`                | nothing                                               |

The latch is per issue, so a later report announces only what is new; delivery is one joined message per report, so a maximally misconfigured global config still produces one notification, as it does today.
Replacing the set rather than accumulating into it is what makes the fifth row differ from the second.
It preserves today's clearing behavior (`lastConfigWarning = null` when the issues vanish) at per-issue granularity instead of per-blob.

Measured ceiling from the three cross-cutting detectors on a single global config (`{"permission": {"*": "allow"}, "toolInputPreviewMaxLength": 500, "toolTextSummaryMaxLength": 500, "permissionDialogKeys": {"deny": "j"}}`): **3 issues**, one per detector.
Legacy-file notices and zod field violations add to that only when those files or fields exist.

### Call sites

```ts
// src/index.ts — after configStore (126) and logger (120) exist
const configIssueReporter = new ConfigIssueReporter(configStore, logger);
// …passed to new SessionLifecycleHandler(…) at 308 and new SessionTurnPrep(…) at 315
```

```ts
// src/handlers/lifecycle.ts — handleSessionStart, after Tidying 1
this.session.resetForNewSession(ctx, projectTrusted);
this.session.refreshConfig(ctx, projectTrusted);
this.configIssues.report();   // the config was just re-read; say what is new
```

```ts
// src/handlers/session-turn-prep.ts — prepare
this.session.refreshConfig(ctx, ctx.isProjectTrusted());
this.configIssues.report();   // same shape as session_start
this.readyAnnouncer.announceReady(ctx);
```

Both consumers use 100% of the seam they receive, and neither reaches through the other (`report()` takes no arguments — the reporter asks its own source).

### Why the reporter is not wired through `PermissionSession`

`PermissionSession` already relays `refreshConfig`, `logResolvedConfigPaths`, and `config` to the store, and Tidying 2 gives `refreshConfig` a real job of its own.
Adding a fourth pure relay to a seven-dependency aggregate, to spare a thin handler a sixth collaborator, trades a real ownership boundary for an arity count.
The reporter's only inputs are `configStore` and `logger`, both in hand at `index.ts:126`, so it is wired directly to its drivers.

### Why the store does not drive the reporter itself

An injected reporter called from the end of `refresh()` needs no handler changes at all — and reintroduces the defect at the priming call, which has no context under any ordering.
Coupling load-time to notify-time is the cause, not an implementation detail of it.
Pull from the drivers is the right model even after Tidying 1.

### Naming overlap to hold

`ConfigStore.getConfigIssues()` (no arguments; extension-config load and merge) and `PermissionResolver.getConfigIssues(agentName?)` (agent-scoped policy-file issues) will share a name in one package.
They never appear in the same expression — `handleSessionStart` calls the resolver's directly and reaches the store's only through the reporter — so the collision is conceptual.
Each method's doc comment names its accumulation; [#953] is where the resolver's rename naturally lands.

## Module-Level Changes

### Added

- **`src/config/config-issue-reporter.ts`** — `ConfigIssueReporter` plus the three seams above.
  Doc comment states the latch-per-issue / deliver-per-report rule and why delivery goes through `logger.warn` rather than a ctx parameter.
- **`test/config/config-issue-reporter.test.ts`** — latch and delivery semantics.

### Changed

- **`src/handlers/lifecycle.ts`** — Tidying 1 swaps lines 54-55.
  Then: a sixth constructor dep (`ConfigIssueReporting`); `handleSessionStart` drives `report()` after `refreshConfig`; "Constructor deps" doc comment updated.
- **`src/session/permission-session.ts`** — Tidying 2: `refreshConfig` gains the `ctx?.hasUI`-gated `syncPermissionSystemStatus(ctx, this.configStore.current())` (import from `#src/config/status`); doc comment says it owns the status-bar sync.
- **`src/config/config-store.ts`**
  - Tidying 2: delete lines 113-115 (the status-sync block) and the now-unused `syncPermissionSystemStatus` import from `refresh`'s path (`save` keeps its own call).
  - Step 4: line 83 `private lastConfigWarning: string | null = null` → `private configIssues: readonly string[] = []`; assign `this.configIssues = mergeResult.issues` in `refresh`; add `getConfigIssues(): readonly string[]`; add `ConfigIssueSource` to `implements` (type-only `./config-issue-reporter` import; no runtime cycle).
  - Step 6: delete lines 120-125 (the if/else and the `ctx?.ui.notify` call); delete `save`'s `this.lastConfigWarning = null` (line 180); `refresh(ctx: ExtensionContext | undefined, projectTrusted)` → `refresh(cwd: string | undefined, projectTrusted)`; line 76 doc comment ("privately own `config` and `lastConfigWarning`") restated.
    `git log -S'this.lastConfigWarning = null'` traces the `save` reset to one commit, `5941733a feat: add ConfigStore owning extension config state` — it arrived with the extraction and carries no separate rationale.
    `save` writes only `debugLog` / `permissionReviewLog` / `yoloMode`, none of which any detector reads, so it cannot change the issue set and has nothing to re-announce.
  - `SessionConfigStore.refresh`'s signature changes with step 6; `PermissionSession.refreshConfig` passes `ctx?.cwd`.
- **`src/handlers/session-turn-prep.ts`** — a fourth constructor dep; `prepare` drives `report()` between `refreshConfig` and `announceReady`; doc comment updated.
- **`src/index.ts`** — construct the reporter; pass it at lines 308 and 315.
  The priming call `configStore.refresh(undefined, false)` is textually unchanged by step 6; its first argument now means "no cwd", a plain data absence rather than "no one to notify".
- **`src/config/config-loader.ts`** — `detectUnusableDialogKeys`'s doc comment (lines 463-477) describes this defect in present tense and cites `(#933)`; rewrite for the fixed behavior.
  Verified as the only `#933` mention anywhere in `src/` or `test/`.
- **`test/handlers/lifecycle.test.ts`** — Tidying 1 inverts "calls refreshConfig before resetForNewSession" (line 167).
  `makeSetup()` (line 42) takes the reporter stub; new test for the drive.
- **`test/session/permission-session.test.ts`** — Tidying 2: "config delegation" gains status-sync tests (a `hasUI: true` ctx sets the status; `hasUI: false` does not; `undefined` ctx does not), asserting on `makeCtx`'s `ui.setStatus` — `syncPermissionSystemStatus` is pure over it, so no module mock is needed.
- **`test/config/config-store.test.ts`**
  - Tidying 2: delete "calls syncPermissionSystemStatus when hasUI is true" (297) and "does not call syncPermissionSystemStatus when hasUI is false" (305) — moved, not lost; the `#src/config/status` mock can go with them.
  - Step 4: `getConfigIssues()` is empty before any refresh and returns the loader's issues after one.
  - Step 6: delete the four dedupe tests — "sets warning when issues are present" (217), "notifies UI when a new warning appears and hasUI is true" (240), "does not re-notify the same warning on subsequent calls" (252), "clears warning when no issues on next refresh" (265); the remaining `refresh` calls pass a cwd string (or `undefined`) instead of a ctx; add "the `config.loaded` entry carries the joined `warning`".
- **`test/helpers/session-fixtures.ts`** — step 6: `makeConfigStore`'s `refresh` stub type follows the new signature.
- **`test/handlers/session-turn-prep.test.ts`** — `makeTurnPrep()` (line 14) takes the stub; new tests: `report()` is driven on `prepare`, and before `announceReady`.
- **`test/handlers/before-agent-start.test.ts`** — line 74 constructs `SessionTurnPrep` with three args; add the fourth.
- **`test/composition-root.test.ts`** — new end-to-end pin (below); the #927 test at line 2225 retargeted from the debug log to `ui.notify`, keeping its policy-untouched assertions.
- **`test/helpers/handler-fixtures.ts`** — add `makeConfigIssueReporter()` (`{ report: vi.fn() }`) so the three construction sites do not improvise it three times.
- **`docs/architecture/architecture.md`**
  - Line 865 — the `config-store.ts` entry says "owns `config` + `lastConfigWarning`"; restate as a ctx-free load that answers its issue list.
  - New `config/` tree entry for `config-issue-reporter.ts`.
  - Line 934 — the `lifecycle.ts` entry lists "(session: `PermissionSession` + resolver + serviceLifecycle + audit)".
    It already omits `logger`, the existing fourth dep, independent of this issue; correct that and add the reporter in the same edit.
  - Line 936 — the `session-turn-prep.ts` entry lists its three deps and its step order; add the reporter and the report step.
  - Line 887 — the `permission-session.ts` entry lists "the config gateway" among what it owns; add the status-bar sync (Tidying 2).

### Predicted unchanged, with the claim each rests on

| File                                                                                           | Claim                                                                                                                                                         |
| ---------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `src/handlers/session-turn-prep.ts`'s `TurnPrepSession` interface                              | `refreshConfig(ctx, projectTrusted)` keeps its signature on the session; only the store's changes                                                             |
| `src/config/config-schema.ts`, `schemas/permissions.schema.json`, `config/config.example.json` | No config surface changes, so no `pnpm run gen:schema` and no parity-test drift                                                                               |
| `packages/pi-permission-system/README.md`, `docs/configuration.md`                             | No command, config key, or user-facing feature is added, removed, or renamed                                                                                  |
| `.pi/skills/package-pi-permission-system/SKILL.md`                                             | Measured: 0 hits for `lastConfigWarning`, `getConfigIssues`, or `933`; its only `ConfigStore` mention is `makeConfigStore`, whose name and role are unchanged |
| `docs/architecture/architecture.md` Mermaid blocks                                             | Measured: `grep -n "ConfigStore\|config-store"` matches line 865 only — no diagram node models this flow                                                      |
| `docs/architecture/architecture.md:1201-1203`                                                  | The [#933] open-issue sweep entry is a filing-time disposition record; sibling entries for shipped issues (e.g. [#907]) keep their present-tense description  |
| `src/index.ts:236-240` (the priming comment)                                                   | The priming call does not move and both its stated reasons survive                                                                                            |
| `ConfigStore.save`                                                                             | Keeps its command-ctx status sync and error notify; only the `lastConfigWarning` reset is removed                                                             |

## Test Impact Analysis

**New tests the change enables.**
The latch was a private field inside `ConfigStore.refresh`, reachable only through a mocked `loadAndMergeConfigs` plus a hand-built ctx double — which is why the four existing tests read as they do.
As its own collaborator it is directly testable: six cases over a two-method fake, no filesystem and no ctx.
Tidying 2 likewise makes "the status bar tracks yolo on refresh" testable at the session, against a real `ui.setStatus` spy, instead of through a module mock of `#src/config/status`.

**Tests that become redundant.**
The four notify/dedupe tests in `config-store.test.ts`.
Their concern splits cleanly: latch semantics move down to `config-issue-reporter.test.ts`, and end-to-end delivery moves up to the composition-root pin.
The two status-sync tests move to `permission-session.test.ts` with Tidying 2.
What stays at the store's level is only what the store still owns — that it loads, captures the loader's issues, and answers them.

**Tests that must stay as-is.**

- `config-store.test.ts`'s project-scope gating tests (#644) — they assert the `loadAndMergeConfigs` arguments, which this change does not touch.
- `config-store.test.ts`'s "writes config.loaded debug log" — the `warning` field it covers is the fixed behavior's durable record.
- `lifecycle.test.ts`'s "withholds the project scope from refreshConfig and resetForNewSession" and `session-turn-prep.test.ts`'s "withholds the project scope when the project is untrusted" — both open and read; they assert on the real `configStore.refresh` call, not a mocked-away layer.
- The #927 test's policy assertions (`*: allow` still allows; the refused binding keeps `n`).
  Only its warning assertion is retargeted.

**The pin Tidying 1 inverts.**
`lifecycle.test.ts` "calls refreshConfig before resetForNewSession" is a characterization test with no recorded rationale (traced above).
It becomes "calls resetForNewSession before refreshConfig", and now carries a reason.

**The end-to-end pin** (`composition-root.test.ts`), written against the reproduction this plan measured: a global config of `{"permission": {"*": "allow"}}`, the real factory, then `session_start` + **two** `before_agent_start` fires with a `hasUI: true` ctx whose `ui.notify` is captured.
The permissive-bash-fallback text must arrive exactly **once**.
Baseline measured on the current code: **0**.
A three-line spike of the minimal delivery-gated variant produced exactly **1** across `session_start` + one `before_agent_start`, with the full package suite green and **zero** test edits (166 files / 4523 tests, measured) — so no existing test pins the swallow, and the count in this pin is reachable.

## Invariants at risk

| Invariant                                                                                                  | Pinned by                                                                                                                                                                                                                                                                                                           |
| ---------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **#644** — an untrusted project's scope is withheld from every config read, on both refresh paths          | `lifecycle.test.ts` "withholds the project scope from refreshConfig and resetForNewSession"; `session-turn-prep.test.ts` "withholds the project scope when the project is untrusted"; `config-store.test.ts` "withholds the project scope when the project is untrusted" — all three assert the real call arguments |
| **#927** — a refused `permissionDialogKeys` binding keeps its default letter and does not floor the policy | `composition-root.test.ts:2225`; its two policy assertions are preserved verbatim when the warning assertion is retargeted                                                                                                                                                                                          |
| **ADR 0012 decision 3** — `permissions:ready` is announced **last** in turn prep                           | `session-turn-prep.test.ts` "announces the node as ready, on the same ctx", plus a new ordering assertion that `report()` precedes `announceReady`                                                                                                                                                                  |
| The dedupe's real job — an unchanged issue is not re-announced on every turn                               | The end-to-end pin's "exactly once across two `before_agent_start` fires"; quantitative, with the baseline and target measured above                                                                                                                                                                                |
| One notification per report, however many issues                                                           | `config-issue-reporter.test.ts` "`[A, B]` warns once with `A\nB`"                                                                                                                                                                                                                                                   |
| The status bar reflects `yoloMode` after every refresh with a UI ctx                                       | Today: `config-store.test.ts` 297/305 (module-mocked). After Tidying 2: `permission-session.test.ts` against `ui.setStatus`. Also `composition-root.test.ts` — measured green across the Tidying 1 spike, which exercised the real sync at both moments                                                             |
| `config.loaded`'s `warning` field stays the durable record                                                 | `config-store.test.ts` "writes config.loaded debug log", extended to assert the joined string                                                                                                                                                                                                                       |
| The store holds a config before the first handler runs                                                     | `config-store.test.ts` "returns DEFAULT_EXTENSION_CONFIG before any refresh"; the priming call is not moved                                                                                                                                                                                                         |
| **Tidying 1 preserves policy loading** — `configureForCwd` does not depend on the extension config         | Measured: 4522 green with the lines swapped, including every composition-root policy test; `configureForCwd` (`permission-manager.ts:144-151`) reads only `agentDir` and `cwd`                                                                                                                                      |

**Constituencies.**
The warning's reader is the operator, who is reachable only through `ctx.ui` and only when `hasUI()` is true.
The debug log's reader is whoever is diagnosing after the fact, and that record is unchanged by this plan.
Both constituencies are served after the change; today only the second is.

## TDD Order

1. **`refactor:` activate before refresh** (Tidying 1).
   Red: invert `lifecycle.test.ts` "calls refreshConfig before resetForNewSession" to assert `["resetForNewSession", "refreshConfig"]`, with a comment naming why: delivery through `session.notify` needs the context `activate` sets.
   Green: swap `lifecycle.ts:54-55`.
   Friction it prepares: without it, step 5's `report()` would need to sit in a specific position relative to `resetForNewSession`, with a comment and a mutation to pin it; after it, both drivers have the same `refresh → report` shape and there is nothing to pin.
   **Killing mutation:** swap the lines back — the inverted pin goes red.
   Commit: `refactor: activate the session before the session_start config refresh`

2. **`refactor:` status sync leaves the load** (Tidying 2).
   Red: `permission-session.test.ts` "config delegation" — `refreshConfig` with a `hasUI: true` ctx calls `ctx.ui.setStatus(PERMISSION_SYSTEM_STATUS_KEY, …)` reflecting the store's current config; with `hasUI: false` it does not; with `undefined` it does not.
   `config-store.test.ts` — `refresh` never calls `ctx.ui.setStatus` (replacing 297/305, which move here in spirit).
   Green: add the gated `syncPermissionSystemStatus` call to `PermissionSession.refreshConfig`; delete `config-store.ts:113-115` and the `#src/config/status` mock's now-dead `syncPermissionSystemStatus` entry in `config-store.test.ts`.
   Friction it prepares: after this, `ctx` has exactly one reader left in `ConfigStore.refresh` — the notify — so step 6 can delete the parameter and let the compiler find every site.
   **Killing mutations:** (a) drop the `hasUI` guard in `refreshConfig` — the `hasUI: false` test goes red; (b) restore the status-sync block in `ConfigStore.refresh` — "never calls `ctx.ui.setStatus`" goes red.
   Commit: `refactor: sync the status bar from PermissionSession.refreshConfig, not the store`

3. **`refactor:` the latch, as its own collaborator.**
   Red: `test/config/config-issue-reporter.test.ts` — the six rows of the semantics table.
   Green: `src/config/config-issue-reporter.ts`.
   No consumer references it yet, so this is `refactor:` and `cliff.toml` skips it.
   **Killing mutations:** (a) delete the `!this.reported.has(issue)` filter so every current issue is fresh on every report — must redden "`[A]`, `[A]` warns once" and "`[A]`, `[A, B]` warns only `B`"; (b) replace `this.reported = new Set(current)` with `for (const i of current) this.reported.add(i)` — must redden "`[A]`, `[]`, `[A]` warns twice" and leave the rest green; (c) replace `fresh.join("\n")` with a per-issue `warn` loop — must redden "`[A, B]` warns once with `A\nB`".
   Commit: `refactor: add a config-issue reporter that warns what is new, once per report`

4. **`refactor:` the store answers its issues (additive).**
   Red: `config-store.test.ts` — `getConfigIssues()` is empty before any refresh, and returns the loader's `issues` after one.
   Green: add the `configIssues` field, assign it in `refresh`, add the getter, add `implements ConfigIssueSource`.
   The notify and the `lastConfigWarning` dedupe stay untouched in this step — lift-and-shift, so no commit exists in which a config warning has no delivery path at all.
   **Killing mutation:** make `getConfigIssues()` return `[]` unconditionally — must redden the post-refresh test and leave the pre-refresh one green.
   Commit: `refactor: let ConfigStore answer its config issues`

5. **`fix:` the warning reaches the operator.**
   This is the step a user can observe, and the one that ships to the changelog.
   Red: the end-to-end pin in `composition-root.test.ts` (exactly one notify of the permissive-bash-fallback text across `session_start` + two `before_agent_start` fires); the #927 test retargeted to `ui.notify`; `lifecycle.test.ts` "drives the config-issue report on session_start"; `session-turn-prep.test.ts` "drives the config-issue report on prepare" and "reports before announcing ready".
   Green: `makeConfigIssueReporter()` in `test/helpers/handler-fixtures.ts`; the sixth dep on `SessionLifecycleHandler` and the `report()` call after `refreshConfig`; the fourth dep on `SessionTurnPrep` and the `report()` call between `refreshConfig` and `announceReady`; the construction and two wirings in `index.ts`; the third arity fix in `before-agent-start.test.ts`.
   Both handler-class doc comments updated here, with their `architecture.md` entries (lines 934/936, including the pre-existing `logger` omission on 934).
   **Killing mutations:** (a) delete the `report()` call in `handleSessionStart` — must redden the end-to-end pin and the #927 retarget, since turn prep would still deliver but only on the first prompt; (b) delete the `report()` call in `prepare` — must redden "drives the config-issue report on prepare"; (c) move `prepare`'s `report()` after `announceReady` — must redden "reports before announcing ready".
   Commit: `fix(pi-permission-system): show a config warning present at session start`

6. **`refactor:` remove the swallowing push; `refresh` becomes a pure load.**
   Red: `config-store.test.ts` — delete the four dedupe tests; every remaining `refresh` call passes a cwd string or `undefined`; add "the `config.loaded` entry carries the joined `warning`".
   Green: delete `lastConfigWarning`, lines 120-125, and `save`'s reset; change `refresh`'s first parameter to `cwd: string | undefined` (in `ConfigStore` and `SessionConfigStore`); `PermissionSession.refreshConfig` passes `ctx?.cwd`; `makeConfigStore`'s stub type follows; update the class doc comment at line 76 and the `architecture.md` entry at line 865; add the `config-issue-reporter.ts` tree entry.
   With no ctx in scope, a `ui.notify` cannot be reintroduced here without the compiler objecting — that is the whole point of the parameter removal.
   **Killing mutations:** (a) drop the `warning` join so `config.loaded` records `null` — must redden the debug-entry test; (b) in `PermissionSession.refreshConfig`, pass `undefined` instead of `ctx?.cwd` — must redden `permission-session.test.ts` "refreshConfig delegates to configStore.refresh with the trust flag" once it asserts the cwd.
   Commit: `refactor: make ConfigStore.refresh a ctx-free load`

7. **`docs:` correct the prose the fix invalidates.**
   Rewrite `detectUnusableDialogKeys`'s doc comment (`config-loader.ts:463-477`), whose present tense and `(#933)` citation describe the defect as live.
   Mark nothing in the roadmap — [#933] is an open-issue sweep disposition, not a numbered step, so there is no `✅` or `Landed:` note to add.
   Verify: `grep -rn "933" packages/pi-permission-system/src packages/pi-permission-system/test` returns nothing.
   Commit: `docs(pi-permission-system): describe the dialog-key warning's delivery`

Steps 1-4, 6 and 7 are `refactor:`/`docs:` by the rule that a step's type follows what a user can observe once it lands; step 5 is where the behavior arrives, so the changelog reads once.
Steps 1 and 2 are the Tidy First preparations; the implementing session executes them in order and runs no second assessment.

## Risks and Mitigations

| Risk                                                                                                                                                 | Mitigation                                                                                                                                                                                                                                                                                            |
| ---------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Tidying 1 changes something the pin was protecting**                                                                                               | The pin's history was traced (no recorded rationale); every side effect of `resetForNewSession` was read and none reads the extension config; the swap was spiked at 4522 green. The one observable delta — an IO failure during the `session_start` refresh is now reported — is the fix's own class |
| **Absent latch:** a reporter with no `reported` set re-warns every issue on every turn — the annoyance `lastConfigWarning` was introduced to prevent | This is the risk asserting what happens when the mechanism is *absent*, so it is named as step 3's killing mutation (a) and step 5's end-to-end pin counts notifications across two turns rather than one. Testing the latch present would only verify the happy path                                 |
| **A delivery gap between commits**                                                                                                                   | Steps 3-4 are additive; the old push is deleted only in step 6, after step 5 delivers. Tidying 2 deliberately leaves `ctx` on `refresh` so the notify survives until then                                                                                                                             |
| **A save no longer re-announces** a persisting issue, because the store-level reset is gone                                                          | `save` writes only `debugLog` / `permissionReviewLog` / `yoloMode`; no detector reads any of them, so the issue set cannot change across a save and the reporter's latch is correct to hold                                                                                                           |
| **`SessionLifecycleHandler` reaches six constructor deps**                                                                                           | All six are distinct collaborators, each used by a distinct responsibility, and every `src/handlers/` class uses positional construction. The Tidy-First assessor declined a deps-object as the wrong-abstraction move for this directory. Track and watch                                            |
| **A mid-session warning arrives between turns**, when the operator may not be looking at the notification area                                       | Unchanged from today's intent — the dedupe existed to deliver exactly this case. `config.loaded` remains the durable record for anyone reading after the fact                                                                                                                                         |

## Open Questions

- Should the reporter write a durable review-log record alongside the warning, as `AuthorizerChainAudit` does?
  `config.loaded`'s debug entry covers it only when `debugLog` is enabled.
  Deferred — it is new behavior the issue did not ask for.
- [#953] carries the sibling accumulation, the agent-name question its fold raises, and the `PermissionResolver.getConfigIssues` → `getPolicyIssues` rename.

[#907]: https://github.com/gotgenes/pi-packages/issues/907
[#927]: https://github.com/gotgenes/pi-packages/issues/927
[#933]: https://github.com/gotgenes/pi-packages/issues/933
[#953]: https://github.com/gotgenes/pi-packages/issues/953
