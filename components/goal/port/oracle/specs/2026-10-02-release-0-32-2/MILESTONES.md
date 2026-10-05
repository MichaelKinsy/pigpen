# Milestones — release 0.32.2

## Contents

Patch release with two contributor fixes, both merged with the contributor's commits unchanged:

- PR #85 by `x12315` (montana): dashboard keyboard navigation and single-press handling. Spec: `specs/2026-10-02-dashboard-keyboard-navigation/`.
- PR #84 by `rogeecn` (Rogee): HTTP/2 `PROTOCOL_ERROR` classified as transient. Spec: `specs/2026-10-02-network-protocol-error-retry/`.

## Changelog

The `## [Unreleased]` section from the two merges is cut as `## [0.32.2] — 2026-10-02`. The ranking badge, `assets/ranking.json`, and the README badge line are left to the publish workflow, which regenerates them on the release tag.

## Validation on the release tip (macOS, pi 1.0.0 dev dependencies)

- `npm run check` — exit 0.
- `npx eslint .` — exit 0.
- `node scripts/run-unit-tests.mjs` — 1051 passed, 0 failed, 0 skipped, 44 suites.
- `node scripts/run-unit-tests.mjs integration` — 31 passed, 0 failed, 0 skipped.
- `node scripts/run-unit-tests.mjs e2e` — 20 passed, 0 failed, 0 skipped.
- `npm run test:selfcheck` — exit 0, 77 test files.
- `npm run context:gate` — PASS, 24 fixtures against the unmodified `experiments/context/baseline-main.json`.
- `npm pack --dry-run` — `pi-goal-x-0.32.2.tgz`, 71 files, 269.7 kB.

## Notes

- The two fix specs were registered in `specs/SPECS.yaml` in a separate commit before the version bump, as their own notes record.
- PR #83 (background-task wait) is not in this release. It is parked pending a design decision; `specs/2026-10-01-open-pr-disposition/PLAN.md` records the assessment and the required changes.