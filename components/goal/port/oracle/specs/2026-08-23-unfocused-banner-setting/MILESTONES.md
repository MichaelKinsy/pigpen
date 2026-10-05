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
