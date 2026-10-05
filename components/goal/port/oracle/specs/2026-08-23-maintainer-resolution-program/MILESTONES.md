# MILESTONES — program execution log

## 2026-08-23

- 15:0x preflight complete (tag `pre-maintenance-2026-08-23`, protection on,
  baseline 818/818 recorded, fork heads verified, notices posted).
- PR #32 merged (issue #30) → 0.27.5 released; #30 CLOSED COMPLETED.
- PR #27 merged after lease force-replacement of CarmeloCampos fork branch.
- PR #29 merged after lease force-replacement of eettoreee fork branch;
  0.28.0 released via PR #33.
- PRs #34/#35/#37 merged (measurement, dedup, separation) behind
  context:gate + deterministic protocol gate.
- PR #38 merged (issue #26 Oracle) → 0.29.0 released via PR #39; #26 CLOSED
  COMPLETED.
- PR #40 final report; PR #41 backfilled MILESTONES.md for the two behavior
  specs flagged by the independent completion auditor.

Setbacks recorded: settings lock starvation (fixed: wider window + backoff);
fork workflow runs requiring admin approval each time; one flaky serial-run
failure cleared on two consecutive clean reruns; local-main mishap during
PR G caught by branch protection before any push.
