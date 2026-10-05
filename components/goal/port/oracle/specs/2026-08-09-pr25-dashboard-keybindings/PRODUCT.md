# PR #25 review — task dashboard shortcuts

Date: 2026-08-09

Scope: analyse PR #25 (fork author johnrichardrinehart, single commit
`32d4550`, base 0.26.0-era main), fix what's necessary, squash-merge if
valuable. Goal id `msm8ppl6-m0g9zh`.

## The PR

`goal: configure task dashboard shortcuts` — adds configurable keybindings
for the unified task dashboard via the settings file:

- `keybindings.dashboard.toggleExpand` (default `ctrl+shift+t`)
- `keybindings.dashboard.scrollUp` (default `ctrl+shift+up`)
- `keybindings.dashboard.scrollDown` (default `ctrl+shift+down`)

Files: `extensions/goal-settings.ts` (schema + parse + load + save +
effective report), `extensions/goal-widget.ts` (input handling),
`extensions/widgets/goal-dashboard-renderer.ts` (footer hints),
`extensions/widgets/goal-commands.ts` (settings value render), README +
`docs/unified-dashboard.md`, 3 new settings tests + 1 updated overlay test.
161 insertions / 22 deletions across 9 files.

## Analysis against current main

The branch is 13 commits behind main (base `d822fd1`, 0.26.0; main is now
0.27.0 with the reliability contract). Cherry-pick of the single commit onto
current main applied **with zero conflicts** (`merge/pr-25` worktree).

### Interplay with post-0.26.0 changes — all green

| Area | Finding |
|---|---|
| `loadGoalSettings` caching (t4 refresh work) | PR reads settings once at subscription time; cache invalidation unaffected. New keybindings take effect on next re-sync (same staleness model as other settings — documented). |
| Strict settings schema | PR adds `keybindings` to `ALLOWED_SETTINGS_KEYS` + nested strict validation (`ALLOWED_KEYBINDING_KEYS`, `ALLOWED_DASHBOARD_KEYBINDING_KEYS`), consistent with the `additionalProperties: false` contract. |
| `noUncheckedIndexedAccess` (t2) | No new indexed accesses; tsc clean under the strict tsconfig. |
| ESLint gate (t2) | Clean, no new violations. |
| Golden tests (`goal-dashboard-golden`) | Pass. Footer output for default keys is **byte-identical** to current main: the new `expandHint`/`scrollHint` produce exactly the old `specFor().footerHint` strings for wide/medium (`Ctrl+Shift+T: expand tasks`), narrow (`Ctrl+Shift+T: expand`), minimal (`Ctrl+Shift+T: expand`), and the old overflow strings (`Ctrl+Shift+T: expand · Ctrl+Shift+↑↓: scroll` etc.). Verified by direct render probes for default and custom keys, wide/narrow/minimal, overflow + non-overflow. |
| `/goal-status verbose` report | New `dashboard keybindings` row wired via `effectiveSettingsReport` with safe optional chaining. |
| `/goal-settings` menu | `SETTING_ROWS` unchanged (menu edits the 10 persisted fields; keybindings are hand-edited in JSON as documented) — no drift. |

### Value criteria

1. Coherent, well-scoped feature — yes: one settings namespace, three keys,
   strict validation, rendering + input both consume it.
2. Backward compatible — yes: defaults identical to today's hardcoded keys;
   settings files without `keybindings` load defaults; unknown nested keys
   rejected.
3. Tests + docs — yes: 3 parse/save tests + overlay test update, README and
   `docs/unified-dashboard.md` sections.
4. No regression to existing keys/settings — verified above; full suite
   788/788, tsc + lint clean, selfcheck OK, pack dry-run clean, audit 0
   vulns, bench gate PASS.

## Issues to fix (t2)

1. **Non-null assertion** `settings.keybindings!.dashboard` in
   `extensions/goal-widget.ts` — safe through `loadGoalSettings` (always
   merges `DEFAULT_GOAL_KEYBINDINGS`) but unidiomatic and crash-prone for
   hand-built settings objects. Fix: `settings.keybindings?.dashboard ??
   DEFAULT_GOAL_KEYBINDINGS.dashboard`.
2. **`formatGoalKeybinding` cosmetics** — unknown key parts render
   upper-cased (`PAGEUP`). Add `pageUp`/`pageDown`/`home`/`end` → readable
   labels (`PageUp`/`PageDown`/`Home`/`End`).
3. **Missing renderer tests for custom keys** — the PR tests the settings
   layer only. Add unit tests for the footer with custom keybindings
   (toggle hint, scroll hint, overflow branch) to lock the behavior.
4. **Key-name validation gap** — `asKeybinding` accepts any non-empty
   string cast to `KeyId`. Accepted risk (no runtime validator exported by
   pi-tui; an invalid key simply never matches and stays visible in the
   footer). Documented, no code change.
5. **No CI on the fork branch** — GitHub Actions did not report checks for
   the fork PR; covered by running the full gates locally (all green) and by
   main's workflow post-merge (t4).

## Verdict

**VALUABLE — merged.** The feature is coherent, backward compatible, tested,
documented, and applied cleanly to current main with zero regressions
(788/788, all gates green).

## Merge log

- `63324b5` PR commit + `6265ad5` hardening fixes on review branch
  `merge/pr-25` (worktree).
- `9ba9834` squash onto main — author John Rinehart preserved (committer
  Thomas Monk), Co-authored-by trailer, `Refs: PR #25`.
- `9cd7e9d` spec review record.
- PR #25 closed with a summary comment.
- CI on main for the merged tree: run `31334012871` **success** on Node 22
  and Node 24 (tsc, lint, 791/791 tests, selfcheck, pack, audit, bench gate).
- No npm/GitHub release (per user decision).
