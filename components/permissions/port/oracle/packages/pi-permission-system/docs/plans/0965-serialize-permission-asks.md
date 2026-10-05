---
issue: 965
issue_title: "pi-permission-system: a second inline permission ask replaces the first and strands its Promise — serialize asks per session"
---

# Serialize the human-facing asks one session presents

## Release Recommendation

**Release:** ship independently

Issue #965 is not a step of any improvement phase.
Phase 15 (token roles and declared effects) is open, and its spine is bash token-role loss; this is an `authority/` prompt-scheduling defect that shares no step's mechanism, the same disposition [#907] and [#914] carry.
It is also a user-visible hang with a third-party reporter carrying a private fork, so it should not wait behind a batch.

## Problem Statement

A parent session running background subagents on an ask-y policy hangs, and the hang is invisible.

What the operator sees, in order:

1. A subagent's first tool call touches a path outside its cwd, so the child forwards an `external_directory` ask to the parent's inbox.
2. The parent's poll loop drains it and mounts the forwarded permission dialog inline.
3. The parent agent, still working, hits an `ask` of its own; a second dialog mounts.
4. A prompt flashes up and is immediately replaced.
   The operator answers the visible one, and then nothing happens.
   The subagents' widgets may even show ✓, while the parent sits at "working" with no answerable prompt on screen.
   `Esc` brings nothing back, and the session has to be killed.

The reporter observed this five times across four agent roles on Windows 10 + Git Bash, with the first `bash`/`read` call of a `pi-subagents` child as the trigger.

The mechanism, verified rather than taken on report:

- Pi's `showExtensionCustom`, inline branch (`overlay: false`), mounts a component with `disposeActiveSelector(); editorContainer.clear(); addChild(component); setFocus(component)`.
  The previous component's `close` is never called, so its promise never settles and it is never disposed.
  Read in the published `@earendil-works/pi-coding-agent@0.87.0` tarball (`dist/modes/interactive/interactive-mode.js`, `showExtensionCustom` at line 2237) and byte-identical in the tracking checkout at `d1230ea20`.
- `Container.clear()` (`packages/tui/src/tui.ts`) drops its children array without calling `dispose` on anything, so an evicted component cannot even learn it was evicted.
- This extension presents human-facing asks from two independent tasks that converge on one terminal, `LocalUserAuthorizer.authorize`: the gate's local ask (`GateRunner` → `AuthorizerSelection.escalate`) and the forwarded ask (`ForwardingManager` tick → `ForwardedRequestServer.processInbox` → the same `escalate`).
  Nothing coordinates them.
- `ForwardedRequestServer.processInbox` drains its inbox serially and `ForwardingManager` holds a `processing` lock for the whole drain, so a stranded forwarded ask holds that lock and every later forwarded ask queues behind it indefinitely.
  That is the 240 s+ first-tool-call stall.

Upstream `earendil-works/pi#7007` is closed with the disposition that prompt sources sharing one inline slot must serialize their own prompts; core does not arbitrate.
So the fix belongs here.

## Goals

- Serialize every human-facing ask this session presents — local and forwarded alike — through one FIFO queue, so the second ask waits for the first to settle instead of replacing its dialog.
- Settle every pending and queued ask at session shutdown as an *unanswered denial*, never an approval and never a "user denied", so a gate and the forwarding inbox behind it are released when the session goes away.
- Keep `permissions:ui_prompt` honest: the documented contract is that it fires *immediately before* the active user-facing UI is invoked, so the emit moves inside the serialized region rather than firing at enqueue time.
- Not breaking.
  No config key changes, no default changes, no public type changes (see Design Overview).

## Non-Goals

- **Cross-extension arbitration.**
  A per-extension queue cannot see another extension's use of the same slot.
  Confirmed other writers of Pi's inline `custom` slot: `@eko24ive/pi-ask` (`src/ui/controller.ts:122`, `ctx.ui.custom<AskResult>(factory)` with no options, so `overlay ?? false`, mounted from a **tool call** rather than a keystroke) and `@gotgenes/pi-subagents`' session navigator (`src/ui/session-navigator.ts:110`).
  Only core can arbitrate globally, and `pi#7007` declined to.
  This is a bounded limitation, priced in Risks, not a defect of the design.
- **Our own settings modal is not a colliding writer.**
  `src/config/config-modal.ts:188` mounts with `{ overlay: true }`, which is a different host slot (`ui.showOverlay`), so it neither clobbers nor is clobbered by the ask dialog and stays out of the queue.
- **A bounded wait or prompt timeout.**
  The queue is unbounded by operator decision.
  Auto-denying a queued ask because the human at the head is slow is [#931]'s feature, deliberately not arriving through the back door here.
- **Fixing the forwarding timeout itself.**
  [#735] (dead or blocked parents burning the full timeout) is adjacent; a released ask now returns an answer at shutdown rather than waiting it out, but the timeout's length and its dead-target fast-fail are untouched.
- **Coalescing two asks raised by one tool call.**
  [#915] is the separate question of whether two gates should raise two prompts at all; this change only stops the two prompts from destroying each other.
- **Moving the dialog to the overlay path.**
  PR [#638] proposes that for a different reason (tmux flicker), and [#874] / PR [#757] push the package's dialogs in the opposite direction.
  Two overlays would contend on their own slot, so overlay is not a fix for this.

## Background

- `LocalUserAuthorizer` (`src/authority/local-user-authorizer.ts`) is the single `permissions:ui_prompt` emit site and the sole terminal that presents to a human.
  Its `authorize` is a straight-line body: `buildUiPrompt` → `emitUiPromptEvent` → `return this.deps.requestPermissionDecision(...)`.
- It is **constructed per activation**, inside `selectAuthorizer`'s `ctx.hasUI` local arm (`src/authority/authorizer.ts`, the `new LocalUserAuthorizer({...})` literal).
  `AuthorizerSelection.activate` runs on every turn event, so a queue cannot be a field the authorizer owns; it has to be a collaborator threaded through `AuthorizerSelectionDeps`.
- `requestPermissionDecision` (`src/authority/permission-prompt-component.ts`) is the mode dispatcher: TUI gets the inline `ctx.ui.custom` dialog, everything else gets the `select`/`input` fallback.
  Wrapping `authorize` therefore serializes both surfaces.
- `PermissionPromptDecision` already carries `confirmationUnavailable?: true`, and `DecisionSource` already has `{ kind: "unavailable"; reason }` — both added by [#719] precisely so an ask nobody answered is not reported as a user denial.
  `ParentAuthorizer`'s `abandon(denialReason)` (`src/authority/approval-escalator.ts`) is the shape to mirror, including its rule from [#726] that `denialReason` and `decidedBy.reason` are the *same string* so what the model is told and what the log attributes cannot drift.
- `SessionLifecycleHandler.handleSessionShutdown` (`src/handlers/lifecycle.ts`) already owns the shutdown sequence: clear the status bar, `audit.writeSummary`, `session.shutdown()`, `serviceLifecycle.teardown()`.
- `docs/cross-extension-api.md` documents the `permissions:ui_prompt` timing contract explicitly: emitted "immediately before it invokes the active user-facing permission UI… for integrations such as notification extensions that should alert only when the user needs to respond to a permission prompt."
- AGENTS.md constraint that applies: the extension module is cached per `(extensionPath, cwd)` but the factory is re-invoked per session switch, so a queue constructed in the factory body is rebuilt per session generation and carries no state across `/new`.
  It must not be parked at module scope.
- Directory vocabulary: the new module belongs in `authority/`, beside the terminal that uses it.
  `handlers/` is already allow-listed to import `authority/` in `.fallowrc.json`, so the shutdown consumer needs no zone change (verified with `pnpm --silent fallow guard`).

## Design Overview

### The queue

One `AskDialogQueue` per extension-factory invocation, constructed in `src/index.ts` and injected.
It is a FIFO over *presentations*, not a lock over a resource it can inspect: it cannot see the host's slot, so its whole contract is that it never lets two of its own presentations overlap.

```typescript
/** Settle every ask this session still owns; the shutdown-facing slice (ISP). */
export interface AskDialogRelease {
  releaseAll(reason: string): void;
}

export class AskDialogQueue implements AskDialogRelease {
  run<T>(present: () => Promise<T>, released: (reason: string) => T): Promise<T>;
  releaseAll(reason: string): void;
}
```

`run` admits the caller, chains it behind every ask admitted earlier, and invokes `present` only when its turn comes.
`released` is the caller's own fallback value, supplied at admission — the queue never constructs a decision, because a decision is stamped at the site that decides and the queue does not know what kind of thing it is presenting.

Required behavior, stated so the implementation is not re-derived:

- The returned promise settles exactly once, whether by `present`, by `present` rejecting, or by a release.
- A release of a **queued** ask must never call its `present` — the whole point is that nothing renders invisibly.
- A release of an **in-flight** ask settles the caller's promise while the host's own promise stays pending; a late resolution from `present` is discarded rather than overwriting the released value.
- `releaseAll` resets the queue's tail, so a session that keeps running after a release is not blocked behind an ask nobody can answer.
- A rejecting `present` propagates to its own caller and does not wedge the queue.
  This preserves today's behavior: a throwing dialog reaches `createFailClosedToolCall`, which blocks.

The queue is deliberately **logger-free**, following the `transient-fs-retry.ts` precedent in this package.
Nothing is lost: `PermissionPrompter` already brackets each ask with `permission_request.waiting` and a terminal entry, so the queue wait shows up as the gap between them, and a released ask writes a terminal `permission_request.denied` carrying `resolution: "confirmation_unavailable"` and the release reason on `decidedBy`.
The one thing not separable from the log alone is "queued" versus "presented and the human was slow"; `permissions:ui_prompt` now marks the presentation moment on the bus, which is where a consumer that cares already listens.

### Where the emit goes, and why

Decorating the injected `requestPermissionDecision` at the composition root would be the smallest diff and is **rejected**: it fires `permissions:ui_prompt` at enqueue time, potentially minutes before the dialog appears, which breaks the documented contract for exactly its named consumer (a notification extension, and [#906]'s proposed BEL).

So the queue is injected into `LocalUserAuthorizer` and `authorize` runs the emit *and* the presentation inside the serialized region:

```typescript
authorize(details: PromptPermissionDetails): Promise<PermissionPromptDecision> {
  return this.deps.dialogs.run(
    () => {
      emitUiPromptEvent(this.deps.events, buildUiPrompt(details));
      return this.deps.requestPermissionDecision(view, title, details.payload, options);
    },
    (reason) => releasedDecision(reason),
  );
}
```

This keeps the "single `permissions:ui_prompt` emit site" invariant (`architecture.md`'s `local-user-authorizer.ts` entry) and keeps the emit-before-present order the existing test asserts.

### The released decision

Mirrors `ParentAuthorizer`'s `abandon`, one string for both fields:

```typescript
const SESSION_ENDED_REASON =
  "The session ended before this permission request was answered";

function releasedDecision(reason: string): PermissionPromptDecision {
  return {
    approved: false,
    state: "denied",
    confirmationUnavailable: true,
    denialReason: reason,
    decidedBy: { kind: "unavailable", reason },
  };
}
```

`denialReason` is present on purpose: the issue's proposal names only `approved`, `confirmationUnavailable`, and `decidedBy`, which would leave the model with a generic block reason.
[#726]'s rule is that the agent-facing text and the provenance record reuse one string.

### Consumer call sites

The shutdown consumer, `SessionLifecycleHandler.handleSessionShutdown`:

```typescript
this.audit.writeSummary(this.logger);
this.dialogs.releaseAll(SESSION_ENDED_REASON);
this.session.shutdown();
this.serviceLifecycle.teardown();
```

Release runs **before** `session.shutdown()`.
`shutdown()` reaches `deactivate()` → `forwarding.stop()`, and an in-flight `processInbox` that is awaiting a forwarded ask can only finish writing its response file if the ask has already been settled.
Releasing first is what lets that drain complete instead of being abandoned mid-request.

Placement on `SessionLifecycleHandler` (7th dep) rather than `PermissionSession` (which would go from 7 constructor params to 8) follows that class's own documented pattern — `logger`, `audit`, and `configIssues` are all single-purpose collaborators injected there rather than reached through the session — and keeps `PermissionSession`'s parameter list describing session state only.

### What the new source sees, and what it misses

The queue's evidence is *this extension's own admissions*, which is narrower than "the inline slot is free".

- It sees: every ask this node presents, on both the TUI and the `select`/`input` surface, from either task.
  That is the whole reported failure.
- It does not see: a foreign extension mounting into the same slot.
  Today that strands one ask and the next one still renders; behind a FIFO the head never settles and everything after it waits.
  This is not detectable from inside an extension — `Container.clear()` calls no `dispose` — so no design here closes it.
  Priced in Risks and accepted by operator decision.

## Module-Level Changes

| File                                           | Change                                                                                                                                                                                               |
| ---------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `src/authority/ask-dialog-queue.ts`            | **New.** `AskDialogQueue` class + `AskDialogRelease` interface                                                                                                                                       |
| `src/authority/local-user-authorizer.ts`       | `LocalUserAuthorizerDeps` gains `dialogs: AskDialogQueue` (5 → 6 fields); `authorize` body wrapped in `dialogs.run(...)`; `SESSION_ENDED_REASON` + `releasedDecision` helper                         |
| `src/authority/authorizer.ts`                  | `AuthorizerSelectionDeps` gains `dialogs` (9 → 10 fields); `selectAuthorizer`'s local arm passes it into the `new LocalUserAuthorizer({...})` literal                                                |
| `src/handlers/lifecycle.ts`                    | `SessionLifecycleHandler` gains a 7th constructor dep `dialogs: AskDialogRelease`; `handleSessionShutdown` calls `releaseAll`                                                                        |
| `src/index.ts`                                 | Constructs the one `AskDialogQueue`; threads it into the `AuthorizerSelection` deps literal and the `SessionLifecycleHandler` argument list                                                          |
| `test/authority/ask-dialog-queue.test.ts`      | **New.** Queue unit tests                                                                                                                                                                            |
| `test/authority/local-user-authorizer.test.ts` | `makeDeps` widened to accept `events`; the one hand-rolled deps literal routed through it; new serialization tests                                                                                   |
| `test/helpers/authorizer-fixtures.ts`          | `makeAuthorizerSelectionDeps` supplies a default `dialogs`                                                                                                                                           |
| `test/handlers/lifecycle.test.ts`              | Constructs `SessionLifecycleHandler` with the new dep; asserts the release                                                                                                                           |
| `test/composition-root.test.ts`                | Wiring assertion that the real factory's `session_shutdown` releases pending asks                                                                                                                    |
| `docs/architecture/architecture.md`            | Module-tree entry for `ask-dialog-queue.ts`; updated entries for `local-user-authorizer.ts`, `authorizer.ts`, `lifecycle.ts`; a short subsection under `## Prompt presentation` on ask serialization |
| `docs/cross-extension-api.md`                  | `## UI Prompt Broadcasts`: state that concurrent asks are serialized and the event marks the presentation moment, not the enqueue                                                                    |
| `README.md`                                    | Line 24's UI-prompt bullet: same clarification, one clause                                                                                                                                           |

Greps run to build this list:

- New required interface fields (per the shared-fixture rule): `makeAuthorizerSelectionDeps` in `test/helpers/authorizer-fixtures.ts` is the **only** site constructing an `AuthorizerSelectionDeps` bag — `authorizer.test.ts`, `authorizer-selection.test.ts`, and `forwarded-request-server.test.ts` all route through it.
  `test/authority/local-user-authorizer.test.ts` has 13 `makeDeps(` calls plus exactly one hand-rolled `new LocalUserAuthorizer({...})` literal (line 169), which the preparatory step folds in.
- Mechanism prose, not symbols: `ui_prompt` appears in `README.md` (line 24), `.pi/skills/package-pi-permission-system/SKILL.md` (line 170, a bare channel-name list needing no edit), and `docs/cross-extension-api.md`.
- No export is removed or renamed, so no cross-package or public-surface grep is owed.
  `src/service.ts` is the public surface and re-exports `Authorizer` / `AuthorizerVerdict` only; `AuthorizerSelectionDeps` and `LocalUserAuthorizerDeps` are not in `dist/public.d.ts`, so adding a required field to either is not a breaking type change.
- Mermaid: the architecture doc's four diagrams cover the rule model, composition, session approvals, and the roadmap graph; none models prompt scheduling, so none needs a node.

### Predicted unchanged, with the claim each rests on

| File                                           | Claim                                                                                                                                                                                                                          |
| ---------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `src/authority/forwarding-manager.ts`          | Serialization lives below `escalate`; the processing lock and the per-tick re-announcement are untouched, and its "re-announces while a drain is still in flight" test is exactly the invariant that keeps a longer drain safe |
| `src/authority/forwarded-request-server.ts`    | It already drains serially and reaches the queue through the injected `escalator`                                                                                                                                              |
| `src/authority/permission-prompt-component.ts` | The queue wraps `authorize`, above the mode dispatch                                                                                                                                                                           |
| `src/authority/permission-prompter.ts`         | Its `waiting` entry now brackets the queue wait as well as the deliberation; no code change is needed for that to be true                                                                                                      |
| `src/session/permission-session.ts`            | Release lives on the lifecycle handler, so the constructor stays at 7 params                                                                                                                                                   |
| `src/config/config-modal.ts`                   | Mounts with `{ overlay: true }`, a different host slot                                                                                                                                                                         |
| `.fallowrc.json`                               | `handlers/` already allows `authority/`; verified with `pnpm --silent fallow guard` from the repo root                                                                                                                         |

## Test Impact Analysis

The extraction of a queue makes a previously untestable property testable: today nothing can assert that two asks do not overlap, because the overlap is a property of the host's slot and every test double for `ui.custom` happily mounts twice.
`AskDialogQueue` moves that property into a pure object with no UI, so "the second `present` is not called until the first settles" becomes a plain unit assertion.

- **New and previously impractical:** FIFO ordering, non-overlap, release-while-queued, release-while-in-flight, late-resolution discard, rejection pass-through.
  All of these are queue-level and need no TUI.
- **Existing tests that stay as-is:** every assertion in `local-user-authorizer.test.ts` about payload projection, forwarded provenance ([#292]), option composition, and the emit-before-present order.
  They exercise the layer the queue wraps, not the queue, and the emit-order test is the one that pins the contract move staying honest.
- **Nothing becomes redundant.**
  No existing test covers scheduling, so none is superseded.
- **The queue's input domain** is the interleave, not a value space: admitted-while-idle, admitted-while-busy, released-while-queued, released-while-in-flight, and the rejecting presentation.
  Each gets a case; two concurrent `authorize` calls on a real `LocalUserAuthorizer` over a real queue covers the composition.

## Invariants at risk

| Invariant                                                             | Owner                         | Pinned by                                                                                                                                                                     |
| --------------------------------------------------------------------- | ----------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `LocalUserAuthorizer` is the single `permissions:ui_prompt` emit site | [#292], Phase 13              | `test/authority/local-user-authorizer.test.ts` — "emits a UI prompt event with normalized surface and value"                                                                  |
| The emit precedes the presentation                                    | `docs/cross-extension-api.md` | Same file, the `expect(calls).toEqual(["emit", "dialog"])` case (line ~169) — the one hand-rolled deps literal, which is why the preparatory step touches it                  |
| A forwarded ask's broadcast is non-degraded                           | [#292]                        | Same file, `describe("forwarded provenance")`                                                                                                                                 |
| A human decision is attributed at the mode dispatcher                 | [#726]                        | `test/authority/permission-prompt-component.test.ts`                                                                                                                          |
| An ask nobody answered is not reported as "User denied"               | [#719]                        | `test/authority/approval-escalator.test.ts` — the released decision must satisfy the same shape                                                                               |
| A serving node re-announces while a drain is still in flight          | [#907]                        | `test/authority/forwarding-manager.test.ts` — "re-announces while a drain is still in flight"; the queue makes drains longer, so this matters more after the change, not less |

Constituencies, since an invariant can be dead for one reader and load-bearing for another: the emit-timing invariant serves a **notification** consumer (alert when the human must respond) and an **audit** consumer (join a prompt to its decision by `requestId`).
Moving the emit inside the queued region is correct for the first and neutral for the second — `requestId` is unchanged and every prompt still gets exactly one terminal `permissions:decision`.

Quantitative claim, stated structurally rather than estimated: an uncontended ask is delayed by exactly one microtask turn, because the queue's tail starts already-resolved and `run` chains through a single `.then`.
That is asserted directly (the first `present` has not run synchronously but has run after one awaited tick) rather than argued.

## TDD Order

1. **`test:` route the last hand-rolled `LocalUserAuthorizer` deps literal through `makeDeps`.**
   Widen `makeDeps`'s override parameter in `test/authority/local-user-authorizer.test.ts` to accept `events`, and rewrite the emit-order test (line ~169) to use it.
   Friction it prepares: that literal is the file's only deps-construction site bypassing `makeDeps`, so the new required `dialogs` field would otherwise need inserting in two places in one file.
   This is the Tidy-First assessor's one accepted recommendation; it adds no tests and so names no killing mutation — verification is that the suite stays green and `new LocalUserAuthorizer({` appears nowhere in the file outside `makeDeps`.
   Commit: `test(pi-permission-system): build every LocalUserAuthorizer deps bag through makeDeps`.

2. **`refactor:` add `AskDialogQueue`.**
   New `src/authority/ask-dialog-queue.ts` and `test/authority/ask-dialog-queue.test.ts`.
   Covers: idle admission presents after one tick; a second admission does not call its `present` until the first settles; three admissions present in FIFO order; a rejecting `present` rejects its own caller and leaves the queue usable; `releaseAll` settles a queued ask **without calling its `present`**; `releaseAll` settles an in-flight ask and a later resolution from its `present` is discarded; the queue accepts new work after a release.
   Typed `refactor:` because no consumer references it yet, so `cliff.toml` leaves the changelog to step 3.
   Killing mutations, one per equivalence class:
   - Ordering: "make `run` call `present()` immediately instead of chaining on the stored tail" — must turn the non-overlap and FIFO tests red.
   - Release dispatch: "make `releaseAll` clear its set of live entries without invoking each `released` callback" — must turn both release tests red.
   - Release reset: "make `releaseAll` leave the tail chained to the released ask" — must turn "accepts new work after a release" red.
   - Settle-once: "drop the settled guard so a late `present` resolution overwrites the released value" — must turn the late-resolution test red.

   Commit: `refactor(pi-permission-system): add a FIFO queue for human-facing asks`.

3. **`fix:` present one ask at a time.**
   `LocalUserAuthorizerDeps` gains `dialogs`; `authorize` wraps emit + presentation in `dialogs.run`; `AuthorizerSelectionDeps` gains `dialogs` and `selectAuthorizer` passes it; `test/helpers/authorizer-fixtures.ts` supplies a default; `src/index.ts` constructs the queue and threads it in.
   One step because adding a required field to both dep bags breaks every consumer at the type level in the same commit.
   Covers: two concurrent `authorize` calls on one authorizer never overlap their presentations; the second ask's `permissions:ui_prompt` is emitted only after the first settles; the existing emit-before-present order still holds for a single ask.
   Killing mutations:
   - "make `authorize` call the emit and `requestPermissionDecision` directly, bypassing `this.deps.dialogs.run`" — must turn the non-overlap test and the deferred-emit test red.
   - "hoist `emitUiPromptEvent` back above the `dialogs.run(...)` call" — must turn the deferred-emit test red and leave the non-overlap test green; a mutation that reddens both means the deferred-emit test is not isolating the timing.

   Commit: `fix(pi-permission-system): stop a second permission ask from replacing the first`.

4. **`fix:` release pending asks when the session ends.**
   `SessionLifecycleHandler` gains the `dialogs: AskDialogRelease` dep and calls `releaseAll(SESSION_ENDED_REASON)` ahead of `session.shutdown()`; `src/index.ts` passes it; `test/handlers/lifecycle.test.ts` and `test/composition-root.test.ts` cover the wiring.
   Covers: `handleSessionShutdown` releases; the release precedes `session.shutdown()`; a released ask's decision is `{ approved: false, state: "denied", confirmationUnavailable: true }` with `denialReason` and `decidedBy.reason` the same string and `decidedBy.kind === "unavailable"`; the real factory's `session_shutdown` releases an ask admitted through the real chain.
   Killing mutations:
   - "delete the `this.dialogs.releaseAll(...)` line from `handleSessionShutdown`" — must turn the lifecycle and composition-root tests red.
   - "move the `releaseAll` call after `this.session.shutdown()`" — must turn the ordering test red.
   - "return `createDeniedPermissionDecision()` from the released callback instead of the unavailable-marked shape" — must turn the decision-shape test red; this is the [#719] attribution guard, so a green here would mean the shape is unasserted.

   Commit: `fix(pi-permission-system): answer pending permission asks when the session ends`.

5. **`docs:` record the serialization.**
   `docs/architecture/architecture.md` (module-tree entry for the new module, updated entries for the three touched modules, a subsection under `## Prompt presentation`), `docs/cross-extension-api.md`'s `## UI Prompt Broadcasts`, and `README.md` line 24.
   Verification: `pnpm exec rumdl check` on each edited file, and a grep that `ask-dialog-queue.ts` appears in the module tree.
   Commit: `docs(pi-permission-system): document per-session ask serialization`.

## Risks and Mitigations

| Risk                                                                                                               | Mitigation                                                                                                                                                                                                                                                                                                                                              |
| ------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| A foreign extension clobbers the queue's head, which then never settles and jams every later ask                   | Accepted by operator decision; unbounded FIFO was chosen over a bounded wait because auto-denying behind a slow human is [#931]'s feature. The jam is a strictly narrower case of a hang that already exists today, it needs a non-`@gotgenes` extension mounting inline from a non-keystroke path mid-dialog, and no in-extension design can detect it |
| A released in-flight ask leaves a stale dialog painted, since the queue cannot unmount a component it does not own | Bounded: release happens only at session shutdown, where the host tears the session's UI down itself. Answering the stale dialog resolves a promise nobody awaits, which is inert                                                                                                                                                                       |
| The queue serializes the `select`/`input` fallback too, where the collision may not exist                          | Deliberate and free: `requestPermissionDecision` dispatches on mode below the queue, and serializing a surface that did not need it costs one extra tick. No behavior test distinguishes the two modes here                                                                                                                                             |
| A forwarded ask presented first delays the operator's own turn behind it                                           | This is FIFO working as intended, and it is strictly better than today, where one of the two is destroyed. The forwarding inbox was already serial                                                                                                                                                                                                      |
| The release path fails open (settling as an approval) under some interleave                                        | The released value is a constant produced by `releasedDecision`; the queue never constructs a decision and has no branch that could yield an approval. Step 4's decision-shape assertion is the guard, and its killing mutation names the failure explicitly                                                                                            |
| A queued ask still times out on an out-of-process child that is polling                                            | Unchanged by this plan and out of scope; [#735] owns the timeout's length and its dead-target fast-fail                                                                                                                                                                                                                                                 |

## Open Questions

- Should a release also fire on something short of shutdown — an idle threshold, or a `before_agent_start` that finds the head older than the forwarding timeout?
  Deferred: it is the same lever as [#931], and the operator declined a bound in this change.
  No follow-up issue is filed, because [#931] already tracks the lever and nothing here is concrete enough to add to it.
- Whether the package should eventually ask Pi for a session-scoped coordination primitive, which is the only thing that closes the cross-extension case.
  `pi#7007` is closed and its reporter already put the argument on record there; nothing is filed from this session.

[#292]: https://github.com/gotgenes/pi-packages/issues/292
[#638]: https://github.com/gotgenes/pi-packages/issues/638
[#719]: https://github.com/gotgenes/pi-packages/issues/719
[#726]: https://github.com/gotgenes/pi-packages/issues/726
[#735]: https://github.com/gotgenes/pi-packages/issues/735
[#757]: https://github.com/gotgenes/pi-packages/issues/757
[#874]: https://github.com/gotgenes/pi-packages/issues/874
[#906]: https://github.com/gotgenes/pi-packages/issues/906
[#907]: https://github.com/gotgenes/pi-packages/issues/907
[#914]: https://github.com/gotgenes/pi-packages/issues/914
[#915]: https://github.com/gotgenes/pi-packages/issues/915
[#931]: https://github.com/gotgenes/pi-packages/issues/931
