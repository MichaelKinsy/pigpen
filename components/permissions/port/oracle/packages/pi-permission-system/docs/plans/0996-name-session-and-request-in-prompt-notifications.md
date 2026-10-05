---
issue: 996
issue_title: "pi-permission-system: name the session and the request in prompt notifications"
---

# Name the session and the request in prompt notifications

## Release Recommendation

**Release:** ship independently

The architecture roadmap records this issue as out of scope for the open phase (its sweep dispositions list), so it carries no `Release:` batch tag.
The change is a non-breaking `feat:`, so the release is a minor bump of `pi-permission-system`.

## Problem Statement

The `promptNotifications` terminal notifications added for [#906] carry fixed text.
An `osc777` notification is titled `pi` with the body `Permission Required` (or `Permission Required (Subagent)`), and `osc9` carries that body alone.
With several Pi sessions open, a notification says that a prompt is waiting, but not in which session or for what.

The operator sees, in order:

1. A desktop notification titled `pi` reading `Permission Required`.
2. Nothing in it telling which of the open panes raised it.
3. A hunt through each pane to find the open dialog.

The point of the notification is to send the operator to the right pane.

## Goals

- The `osc777` title names the session: `pi — <session name>`, falling back to `pi — <cwd basename>` for an unnamed session, and to `pi` when both are empty (operator decision at the gate).
- The notification body names what is asked and by whom: `<dialog title>: <tool>` or `<dialog title>: <tool> (<agent>)`, for example `Permission Required: bash` or `Permission Required (Subagent): read (scout)`.
- `osc9`, which has a single field, carries the title ahead of the body: `pi — refactor-auth: Permission Required: bash` (operator decision at the gate).
- The request's `value` (command, path, MCP target, skill name) never reaches a notification, per the boundary `docs/decisions/0010-permission-log-secret-exposure.md` draws for the logs.
- Classification: **non-breaking `feat:`**.
  It changes the text of an opt-in notification (default `[]`) and changes no default.
  One observable edge: an `osc777` title filter matching exactly `pi` stops matching once the title names a session or directory.

## Non-Goals

- The `bell` channel — unaffected.
- The `select`/`input` fallback (non-TUI modes) — it emits no notification today and still does not.
- Length-bounding the title or body.
  Notification centers truncate natively; `@eko24ive/pi-ask` caps its message at 120 characters, but a session name is operator-authored text and the body is short labels.
  Revisit only if a real notification proves unreadable.
- The shell-alias invoked name (`exec_command` gated as `bash`): the body names the gated tool (`toolName`), not `invokedToolName`.
- `permissions:ui_prompt` payload shape — unchanged.
- `README.md` line 70 — predicted unchanged: it says the dialog "rings the terminal", making no claim about notification text.
- `docs/cross-extension-api.md` line 521 — predicted unchanged for the same reason.

## Background

- `src/presentation/prompt-notification.ts` — `renderPromptNotification(channels, message)` renders BEL, OSC 9, and OSC 777 per configured channel.
  The OSC 777 title is the fixed constant `OSC_777_TITLE = "pi"`; the message is control-stripped, and in OSC 777 `;` becomes `:`.
- `src/authority/permission-prompt-component.ts` — `presentInlinePermissionPrompt` writes the notification inside the `ui.custom` factory (once, at mount), passing the dialog `title` as the message.
  `PermissionPromptView extends PromptPreferences` with `mode` and `ui`.
- `src/authority/local-user-authorizer.ts` — `LocalUserAuthorizer.present` chooses the dialog title (`"Permission Required"` / `"Permission Required (Subagent)"` on `details.forwarding`), builds the view as `{ mode, ui, ...getPromptPreferences() }`, and calls `requestPermissionDecision(view, title, details.payload, options)`.
- `src/authority/authorizer.ts` — `selectAuthorizer`'s `ctx.hasUI` arm constructs `LocalUserAuthorizer` from `ctx.ui` and `ctx.mode`.
  Rebuilt on every `AuthorizerSelection.activate`.
- `PromptRequestFacts` (`src/presentation/prompt-payload.ts`) — `toolName` is `null` for skill asks and the degraded forwarded payload; `surface` is always present (`""` only on a degraded forwarded payload); `requester.agentName` is `null` for an unnamed local requester.
  A forwarded ask's payload keeps the child's `toolName` and `surface`, with `requester` replaced by the serving node (`buildForwardedAskPayload`).
- Session name: `ReadonlySessionManager` includes `getSessionName(): string | undefined` in the pinned `@earendil-works/pi-coding-agent` 0.79.1 (`dist/core/session-manager.d.ts`), long predating the `>=0.79.0` peer floor (CHANGELOG: added with `/name`).
  `ctx.sessionManager` asserts the runner active (`runner.js`), the same as `ctx.ui`, which the dialog already reads at prompt time.
- Pi's own terminal title (`interactive-mode.ts` `updateTerminalTitle`) is `π - <name> - <cwd basename>`, or `π - <cwd basename>` when unnamed; the cwd-basename fallback here follows that precedent.
- OSC 9 has one text field, which Ghostty's docs describe as the notification's title; OSC 777 carries `notify;<title>;<body>`.
  Both verified from ghostty.org and wezterm.org docs at planning time.
- AGENTS/skill constraints: no `process.platform` read outside `index.ts` (lint-guarded) — `node:path` `basename` does not read it; `#src/` for cross-directory imports; `authority/` already imports `presentation/`.

## Design Overview

Three roles, one per module:

- **What to say** — a pure function in `presentation/prompt-notification.ts`.
- **When to say it** — the dialog's `ui.custom` factory (unchanged position).
- **Where the session facts come from** — `selectAuthorizer`, which holds `ctx`.

```typescript
// src/presentation/prompt-notification.ts
export interface PromptNotice {
  readonly title: string;
  readonly body: string;
}

/** The session facts a notice names, read when the prompt opens. */
export interface NotificationSession {
  readonly name: string | undefined;
  readonly cwd: string;
}

export function describePromptNotice(
  dialogTitle: string,
  request: Pick<PromptRequestFacts, "toolName" | "surface" | "requester">,
  session: NotificationSession,
): PromptNotice;

export function renderPromptNotification(
  channels: readonly PromptNotificationChannel[],
  notice: PromptNotice,
): string;
```

`describePromptNotice` rules:

- `label` = `session.name` when non-empty, else `basename(session.cwd)` when non-empty, else none.
- `title` = `pi — <label>`, or `pi` with no label.
- `what` = `request.toolName ?? request.surface` (empty string counts as absent).
- `who` = `request.requester.agentName` when non-empty (local or forwarded).
- `subject` = `what (who)`, `what`, or `who` alone; `body` = `<dialogTitle>: <subject>`, or `<dialogTitle>` with no subject.
- It reads no `value`, `matchedPattern`, `executedUnit`, or evidence: the `Pick` makes that structural.

`renderPromptNotification` rules:

- `bell` → BEL.
- `osc9` → `ESC ] 9 ; <title>: <body> BEL`, control-stripped.
- `osc777` → `ESC ] 777 ; notify ; <title> ; <body> BEL`, each field control-stripped and `;` → `:`.

Examples (session `refactor-auth`):

| Ask                                              | `osc777` title       | `osc777` body                                  | `osc9`                                                             |
| ------------------------------------------------ | -------------------- | ---------------------------------------------- | ------------------------------------------------------------------ |
| local bash                                       | `pi — refactor-auth` | `Permission Required: bash`                    | `pi — refactor-auth: Permission Required: bash`                    |
| forwarded read from `scout`                      | `pi — refactor-auth` | `Permission Required (Subagent): read (scout)` | `pi — refactor-auth: Permission Required (Subagent): read (scout)` |
| local skill, unnamed session in `/w/pi-packages` | `pi — pi-packages`   | `Permission Required: skill`                   | `pi — pi-packages: Permission Required: skill`                     |

Wiring (call sites):

```typescript
// authorizer.ts, hasUI arm
new LocalUserAuthorizer({ ui: ctx.ui, mode: ctx.mode, /* … */,
  describeSession: () => ({ name: ctx.sessionManager.getSessionName(), cwd: ctx.cwd }) });

// LocalUserAuthorizer.present
const title = details.forwarding ? "Permission Required (Subagent)" : "Permission Required";
const view = { mode, ui, ...getPromptPreferences(),
  notice: describePromptNotice(title, details.payload.request, this.deps.describeSession()) };

// presentInlinePermissionPrompt factory
const notification = renderPromptNotification(view.promptNotifications, view.notice);
```

The thunk is read per prompt, so a name set mid-session is picked up.
For a forwarded ask, the `LocalUserAuthorizer` is the serving node's, so the session name and cwd are the parent's (where the human is), and `who` is the requesting subagent's name.
`PermissionPromptView` gains `notice: PromptNotice`; the `select` fallback ignores it.
The notice is computed before the mode dispatch (cheap, pure), so every `hasUI` context that reaches a prompt must supply `sessionManager.getSessionName` — real contexts always do; test fakes are fixed in step 1.

## Module-Level Changes

- `src/presentation/prompt-notification.ts` — add `PromptNotice`, `NotificationSession`, `describePromptNotice`; change `renderPromptNotification` to take a `PromptNotice`; remove `OSC_777_TITLE`; `import { basename } from "node:path"`.
- `src/authority/permission-prompt-component.ts` — `PermissionPromptView` gains `notice`; the factory renders `view.notice`.
- `src/authority/local-user-authorizer.ts` — `LocalUserAuthorizerDeps` gains `describeSession`; `present` hoists the dialog title and adds `notice` to the view.
- `src/authority/authorizer.ts` — `selectAuthorizer`'s `hasUI` arm supplies `describeSession`.
- `src/config/config-schema.ts` — `promptNotifications` `markdownDescription` ("the notification text is the dialog title, never the request itself") reworded; regenerate `schemas/permissions.schema.json` (`pnpm run gen:schema`).
- `test/presentation/prompt-notification.test.ts` — migrate to the `PromptNotice` signature; add `describePromptNotice` cases.
- `test/authority/permission-prompt-component.test.ts` — `makeView`/`makeFakeView` supply a `notice`; the "terminal notification on open" cases assert the notice's bytes.
- `test/authority/local-user-authorizer.test.ts` — `makeDeps` gains `describeSession`; the exact-view assertion (line ~158) gains `notice`; new notice cases.
- `test/composition-root.test.ts` — `makeBaseCtx`'s `sessionManager` gains `getSessionName` (optional name option); new `osc777` end-to-end case.
- `test/helpers/handler-fixtures.ts` — `makeCtx`'s `sessionManager` gains `getSessionName`.
- `test/helpers/prompt-view-fixtures.ts` — add `makePromptNotice(overrides)`.
- `docs/configuration.md` — `#### Terminal notifications`: replace the "notification text is the dialog title" paragraph with the title/body/osc9 description and the value boundary.
- `docs/architecture/architecture.md` — module-tree entries for `permission-prompt-component.ts` (line ~985) and `prompt-notification.ts` (line ~1026): new signature and the "labels only, never the value" constraint.
- Predicted unchanged: `test/authority/authorizer.test.ts`, `test/authority/authorizer-selection.test.ts`, and `test/helpers/authorizer-fixtures.ts` — they build authorizers but never present a prompt, and the thunk is lazy (assessor-verified; the full suite confirms).
  `test/service/permission-events.test.ts` and `test/session-start.test.ts` use `hasUI: false`, so no `LocalUserAuthorizer` is built.

## Test Impact Analysis

1. New: `describePromptNotice` is pure, so every title/body rule gets a direct case — named session, cwd fallback, empty cwd basename (`/`), local with and without agent, forwarded, skill (`toolName` null), degraded forwarded (`surface` `""`).
2. The renderer tests move from a string message to a `PromptNotice`; the sanitizing cases extend to the title field.
3. The component's "terminal notification on open" tests stay: they pin *when* (once, at mount, not on re-render), which the pure function cannot.
   The composition-root test stays as the only end-to-end pin of `ctx.sessionManager.getSessionName()` → bytes.
4. Value absence: a `describePromptNotice` sweep builds one payload per `PromptPayloadKind` whose `value` holds a sentinel (`SECRET-VALUE`), plus a `matchedPattern`/`executedUnit` sentinel, and asserts no rendered channel output (`bell`, `osc9`, `osc777`) contains it.

## Invariants at risk

- [#906]: the notification fires once per presented ask, at mount, never on re-render or in the fallback — pinned by `permission-prompt-component.test.ts` "does not signal again when the dialog re-renders" and the composition-root "rings the configured channels when a prompt opens, and only then"; both unchanged in intent.
- [#906]: no request **value** reaches notification history (narrowed from "no request fact") — pinned by the new value-absence sweep (step 3) and the component test's existing "the path being decided never reaches the terminal" assertion, updated to the new bytes.
- Control characters in any field cannot end the sequence — pinned by the renderer's sanitizing tests, extended to the title (a session name is free text).

## TDD Order

1. **`test:` give the fake contexts a `sessionManager.getSessionName`.**
   Add `getSessionName` to `makeBaseCtx` in `test/composition-root.test.ts` (with an optional `sessionName` option, default `undefined`) and to `makeCtx` in `test/helpers/handler-fixtures.ts`.
   Prepares the step-4 friction: the notice is built before the mode dispatch, so any `hasUI` fake reaching a prompt would throw.
   Behavior-neutral; no killing mutation (nothing reads the field yet).
   Commit: `test: add getSessionName to the fake session managers`.
2. **`refactor:` hoist the dialog title in `LocalUserAuthorizer.present`.**
   The ternary becomes a local passed to `requestPermissionDecision`, so step 4 can hand the same title to the notice.
   Existing `local-user-authorizer.test.ts` stays green unchanged.
   Commit: `refactor: name the dialog title in LocalUserAuthorizer.present`.
3. **`refactor:` render notifications from a `PromptNotice`.**
   `renderPromptNotification(channels, notice)`; `osc777` writes `notice.title` and `notice.body` (both sanitized, `;` → `:`); `osc9` writes `notice.body` alone for now (bytes unchanged); `OSC_777_TITLE` removed; the component passes `{ title: "pi", body: title }`.
   Tests: migrate `prompt-notification.test.ts`; add a title-sanitizing case (control characters dropped, `;` → `:` in the title field).
   Killing mutation: make `renderChannel` write `notice.title` without `.replaceAll(";", ":")` — the title-sanitizing case goes red.
   Commit: `refactor: render prompt notifications from a title and body`.
4. **`feat:` name the session and the request.**
   Add `NotificationSession` and `describePromptNotice`; `osc9` writes `<title>: <body>`; `PermissionPromptView.notice`; `LocalUserAuthorizerDeps.describeSession`; `present` builds the notice; the component renders `view.notice`; `selectAuthorizer` supplies `describeSession` from `ctx.sessionManager.getSessionName()` and `ctx.cwd`.
   Add `makePromptNotice` to `prompt-view-fixtures.ts`.
   Tests (spell the em-dash as `\u2014` in test literals, so a mis-emitted glyph in `src/` fails):
   - `describePromptNotice` — each rule in the Design Overview, and the value-absence sweep across every `PromptPayloadKind` and all three channels.
   - Renderer — `osc9` carries `<title>: <body>`.
   - `LocalUserAuthorizer` — the view carries `notice` built from `describeSession()` and the dialog title; a forwarded ask's notice body starts `Permission Required (Subagent):`; `describeSession` is called per prompt (a name changed between two asks shows in the second).
   - Component — the terminal write renders `view.notice`, not the dialog title.
   - Composition root — `osc777` configured, `makeTuiCtx` with a session name: the write is `ESC]777;notify;pi — <name>;Permission Required: demo BEL`; unnamed: the title is `pi — <basename(cwd)>` (compute the basename in the test from the `mkdtemp` path).
   Killing mutations, one per class:
   - Make `describePromptNotice` ignore `session.name` → named-session unit case and composition-root named case go red.
   - Make the cwd fallback return no label → cwd-fallback unit case and composition-root unnamed case go red.
   - Use `request.surface` instead of `request.toolName ?? request.surface` → the path-ask case (`toolName` `read`, `surface` `path_read`) goes red.
   - Drop the `(<agent>)` suffix → local-agent and forwarded cases go red.
   - Append `request.value` to the body → the value-absence sweep goes red.
   - Render `osc9` from `notice.body` alone → the `osc9` prefix case goes red.
   - In `present`, pass `"Permission Required"` unconditionally to `describePromptNotice` → the forwarded-notice case goes red.
   - In the component, render `{ title: "pi", body: title }` instead of `view.notice` → the component case goes red.
   - In `selectAuthorizer`, supply `name: undefined` → the composition-root named case goes red.
   Commit: `feat: name the session and the requested tool in prompt notifications`.
5. **`docs:` describe the notification text.**
   `docs/configuration.md` `#### Terminal notifications` paragraph; `config-schema.ts` `markdownDescription` plus `pnpm run gen:schema` (the parity test in `test/config-schema.test.ts` requires both in one commit); `docs/architecture/architecture.md` entries for `permission-prompt-component.ts` and `prompt-notification.ts`.
   Commit: `docs: describe the session and tool in prompt notifications`.

No `Co-authored-by:` trailer: the issue and its design are the operator's own.

## Risks and Mitigations

- **Em-dash emission** — the title's `—` (U+2014) is a known unreliable glyph for the authoring model.
  Test literals spell it `\u2014`, so a mis-emitted character in `src/` turns the tests red instead of shipping.
- **A fake context without `getSessionName`** — would throw at prompt time and fail the ask; step 1 fixes the shared fakes, and the full suite runs after step 4.
- **Stale context** — `ctx.sessionManager` asserts the runner active; `ctx.ui` already does at the same moment, so the notice adds no new failure mode.
- **cwd basename in notification history** — a directory name, not request data; accepted at the gate as the unnamed-session fallback.
- **Non-ASCII in OSC** — a session name could already be any Unicode; OSC payloads are UTF-8 in the terminals verified (Ghostty, WezTerm).

## Open Questions

- Whether to cap title/body length — deferred until a real notification proves unreadable (see Non-Goals).

[#906]: https://github.com/gotgenes/pi-packages/issues/906
