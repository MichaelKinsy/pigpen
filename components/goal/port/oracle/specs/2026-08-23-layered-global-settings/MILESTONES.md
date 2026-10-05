# MILESTONES — layered global settings (PR #27 clean rewrite)

## 2026-08-23 — implementation on maint/pr-b-global-settings

Commit sequence (§32):

1. `docs(spec): define layered global goal settings`
2. `feat(settings): resolve global project and environment layers`
3. `feat(settings-ui): edit scopes and clear inherited overrides`
4. `test(settings): cover layering concurrency diagnostics and paths`

Failed attempt / setback recorded:

- First race-test run exposed lock starvation: `acquirePathLock`'s bounded
  window (20 × 5 ms) timed out under genuine two-thread contention, and one
  retry path busy-spun without backoff when a lock vanished between EEXIST
  and stat. Fixed by widening the default window to 200 × 5 ms (~1 s — user-
  paced UI edits tolerate this) and adding backoff to the vanish path.
  Five consecutive clean race runs after the fix.

Validation evidence:

- Full §81 gate green on the branch: check/lint clean; test:all 857/857;
  self-check manifest OK; test:serial 825/825; pack dry-run ok; audit
  0 vulnerabilities; bench:gate:naf PASS; settings-race PASS;
  verify-pr-head PASS vs forbidden head ea2251b4 / authors CarmeloCampos,
  eettoreee.
- Fork history replaced under lease:
  `git push --force-with-lease=refs/heads/feat/config-global:ea2251b4…`
  succeeded (lease held); maintainer notice posted on #27 before push.
- Fork workflow run required admin approval (`action_required` → approved);
  validate CI then passed at head 6ceba52.
- Merged with expected_head_sha match (local == GitHub head); merge commit
  03ff28f; main CI green; post-merge smoke 857/857.

Behavior notes:

- Legacy strict parse (`parseGoalSettings`) kept for fail-closed callers but
  now aggregates ALL unknown keys into one error; layered loading uses
  diagnostics instead of wholesale rejection.
- `saveGoalSettingsFileConfig` re-implemented over the locked atomic writer
  (fresh re-read under lock; never persists a cached whole object).
EOF
cat > specs/2026-08-23-unfocused-banner-setting/MILESTONES.md <<'EOF'
# MILESTONES — unfocused banner setting (PR #29 clean rewrite)

## 2026-08-23 — implementation on maint/pr-c-banner-setting

Commit sequence (§41):

1. `docs(spec): define optional unfocused UI suppression`
2. `feat(ui): hide and restore unfocused goal chrome live`

Setback / iteration recorded:

- The first live-refresh test failed because the harness's ui.setStatus/
  setWidget were no-ops; extended the integration harness to capture status
  and widget calls so hide/restore can be asserted on captured UI state
  BEFORE the menu handler resolves (per plan §37). A second iteration fixed
  the select-queue matching (match any queued option against the rendered
  option list rather than positionally).

Validation evidence:

- Live hide/restore asserted pre-handler-resolve via captured UI state
  (status cleared + widget unregistered after selecting "Set project override
  to true"; restore re-renders the unfocused hint via "Use inherited value").
- `[PI GOAL UNFOCUSED]` safety invariant pinned by test (prompt builder takes
  only the open-goal count; "Do not choose or switch focus autonomously"
  text unchanged).
- Layering matrix tested: absent/absent visible; global true hidden; project
  false overrides global true; project true over global false hidden.
- Full gate green: check/lint clean; test:all 862/862; selfcheck OK;
  serial 829/829; pack ok; audit 0 vulns; bench PASS; verify-pr-head PASS vs
  forbidden head d2166d4a / author eettoreee.
- Fork replaced under lease (`d2166d4a…` held); fork workflow approved;
  validate CI passed at 4bf271f; merged with expected_head_sha (b18f071);
  main CI green; release 0.28.0 shipped via PR #33 with post-merge smoke
  862/862 ×2 (one unrelated flake cleared on rerun).
EOF
cd /Users/tom/projects/pi-goal-x && git add specs && git commit -q -m "docs(spec): record milestones for layered settings and banner setting" && git push origin HEAD:refs/heads/maint/spec-milestones-backfill 2>&1 | tail -1