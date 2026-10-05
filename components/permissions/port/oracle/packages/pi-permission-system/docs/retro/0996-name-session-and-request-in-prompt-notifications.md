---
issue: 996
issue_title: "pi-permission-system: name the session and the request in prompt notifications"
---

# Retro: #996 — pi-permission-system: name the session and the request in prompt notifications

## Stage: Planning (2026-09-30T05:08:16Z)

### Session summary

Planned a five-step change that makes `promptNotifications` name the session (`osc777` title) and the requested tool and agent (body), never the request's `value`.
A pure `describePromptNotice` in `presentation/prompt-notification.ts` decides the text, `selectAuthorizer` supplies the session facts from `ctx`, and `LocalUserAuthorizer.present` threads a `notice` on the dialog view.
Classified as a non-breaking `feat:`, shipped independently.

### Observations

- Operator decisions at the gate: the title falls back to `pi — <cwd basename>` for an unnamed session (following Pi's own `updateTerminalTitle`), and `osc9` carries `<title>: <body>` because its single field would otherwise lose the session name.
- The `osc9` question bounced twice for context: first for what OSC 9 is, then for which terminals read it and how they present it.
  Verified from ghostty.org and wezterm.org: Ghostty shows OSC 9's one field as the notification title; WezTerm supports both OSC forms; kitty's own protocol is OSC 99 and plain OSC 9 support was not found.
  A gate offering a protocol-level choice needs the protocol briefing up front.
- The session name comes from `ctx.sessionManager.getSessionName()` rather than the issue's `pi.getSessionName()`: `selectAuthorizer` already holds `ctx`, so no `index.ts` wiring is needed; both assert the runner active.
- The issue said 0.79.1 is the peer floor; `package.json` says `>=0.79.0`, and `getSessionName` long predates both.
- The Tidy-First assessor corrected the design's fake-context scope: the notice is built before the mode dispatch, so `makeBaseCtx` (not only `makeTuiCtx`) and `handler-fixtures` `makeCtx` need `getSessionName`; that became step 1.
- Em-dash risk: tests spell the title's `—` as `\u2014` so a mis-emitted glyph in `src/` fails.
- Declined: length-capping the text (pi-ask caps at 120), and naming the shell alias's invoked tool.

## Stage: Implementation — TDD (2026-09-30T05:28:58Z)

### Session summary

All five TDD Order steps landed as five commits: the fake-session tidying, the dialog-title hoist, the two-field renderer, the feature (describer, view `notice`, `describeSession` wiring), and the docs with the regenerated schema.
The `pi-permission-system` suite went from 5064 to 5088 tests (+24), and check, lint, and `fallow dead-code` stayed green throughout.

### Observations

- Deviation: `test/authority/authorizer-selection.test.ts`, which the plan (and the Tidy-First assessor) predicted unchanged, presents a prompt through a real `LocalUserAuthorizer` in four chain-resolution cases; its fake `sessionManager` gained `getSessionName` in the `feat:` commit, noted in the commit body.
  The assessor's "the thunk is lazy, so they never call it" held for `authorizer.test.ts` but not here.
- Every named killing mutation went red where predicted.
  The "append `value` to the body" mutation reddened through the body `toBe` first, so a second mutation leaking the value into the *title* was run: the per-kind sweep's `not.toContain("SECRET")` caught it independently.
- Mutations ran through a literal-replacement script that refused to apply unless its pattern matched exactly once, and each restore was checked with `cmp` against a backup, since the step's own edits were uncommitted.
- The em-dash landed as a literal U+2014 in `src/presentation/prompt-notification.ts`; tests spell it `\u2014`, and they pass, so the glyph is correct.
- Unpinned by design: `selectAuthorizer` reading the session name lazily rather than at activation; the `LocalUserAuthorizer` per-prompt read is pinned, and activation re-runs each turn anyway.
- Pre-completion reviewer: PASS.
  It re-derived the value boundary across every `toolName`/`surface` producer and found none carrying request data; residual note: a forwarded child supplies its own `toolName`/`surface` strings, the same trust domain that already supplies `value`.

## Stage: Final Retrospective (2026-09-30T05:39:55Z)

### Session summary

One session on `main` ran planning, five TDD steps, the ship, and this retro.
`pi-permission-system` 36.2.0 released with prompt notifications that name the session (`pi — <name>`, else the cwd basename) and the requested tool and agent, never the request's value; `osc9` carries the title ahead of the body.
Issue #996 closed against the `feat:` commit; no co-shipped issues or PRs.

### Observations

#### What went well

- Mutation harness: each killing mutation ran through a literal-replacement script that refused unless its pattern matched exactly once, and each restore was checked with `cmp` against a backup (the step's edits were uncommitted, so `git checkout` would have lost them).
  Eleven mutations, none mis-applied.
- The planned "append `value` to the body" mutation reddened through the body `toBe` before the sweep's `not.toContain`, so a second mutation leaking the value into the title proved the sweep discriminates alone; the step's red-count check surfaced this rather than letting a double-covered pin pass as proof.
- The value-absence sweep is typed `Record<PromptPayloadKind, …>`, so a new ask kind is a compile error until it gets a case; the pre-completion reviewer independently re-derived every `toolName`/`surface` producer.

#### What caused friction (agent side)

- `instruction-violation` (user-caught) — The `osc9` gate offered "body only vs. prefix" without saying what OSC 9 is, how it relates to OSC 777, or how terminals render it.
  The operator asked twice for context before answering; `clarification-gates` already requires defining terms of art, but a protocol name read as a config value.
  The same gate shape bounced in #906's planning.
  Impact: two extra gate rounds and two web searches; no rework.
- `instruction-violation` (self-identified, via the suite) — The plan copied the Tidy-First assessor's claim that `authorizer-selection.test.ts` would stay unchanged ("the thunk is lazy") without verifying it; four of its cases present a prompt and went red in step 4.
  The `delegation` skill already says to verify a subagent's universal claim.
  Impact: a one-line fixture fix in the `feat:` commit, noted in its body.

#### What caused friction (user side)

- None; the operator's two redirecting questions were cheaper than a wrong pick and exactly where the gate was thin.

### Diagnostic details

- **Model-performance correlation** — The main session ran on `anthropic/claude-opus-5-5`; both subagents (`tidy-first-assessor`, `pre-completion-reviewer`) ran on `claude-sonnet-5-5` per their transcripts.
  The assessor's one wrong prediction was a universal claim the planner should have verified, not a model mismatch; the reviewer's invariant re-derivation was thorough.
- **Feedback-loop gap analysis** — Each TDD step ran its test files and `pnpm run check` before committing; the full suite, lint, and `fallow dead-code` ran at the end and again before the push.
  No gap.

### Changes made

1. `.pi/skills/clarification-gates/SKILL.md` — the terms-of-art sentence names a protocol or format in an option (`osc9`, SSE) as a term of art to define: what it is and who renders it.
