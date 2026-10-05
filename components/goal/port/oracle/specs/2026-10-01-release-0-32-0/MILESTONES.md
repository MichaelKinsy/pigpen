# Release log — 0.32.0

## 2026-10-01

Released v0.32.0 from PRs #87 and #88.

### Contents

- **pi 1.0.0 support** (#87). Host range `>=0.83.0 <2.0.0`, developed and verified against
  1.0.0. Includes the `session_compact_failed` handler and the drafting-tool guideline that
  stops the model proposing a token budget the user never asked for.
- **Scheduler summary simplification** (#88). The autonomous-runs line is omitted when the
  allowance is unlimited, with `showAutonomousRuns` to hide it otherwise; next actions removed.

### Release shape

The two changes were split into separate pull requests so each could be reviewed on its own:

| PR | Subject | Gates at merge |
| --- | --- | --- |
| #87 | pi 1.0.0 support, budget-draft guard, compaction-failure handling | validate + compatibility 0.83.0, 0.84.1, 0.85.1, 0.86.0 |
| #88 | unlimited-allowance reporting, next-action removal | same |

### Baseline correction before release

Both changes were first written against a local `main` that was 80 commits behind, at 0.31.5.
Releases 0.31.6 through 0.31.9 had shipped in the meantime. Nothing was merged from that work:
PR #86 was closed as conflicting, and both changes were redone against the released line.

Two facts from the released history changed the work:

- **Issue #59 was already released in 0.31.7.** The token-budget feature looked like uncommitted
  in-flight work on the stale checkout. It was not, so there was nothing to hold back from the
  release.
- **The host range is `>=0.83.0 <0.88.0`, not `>=0.83.0 <0.85.0`.** 0.31.7 added tested support
  for hosts 0.83 through 0.87, so setting the range to `^1.0.0` would have dropped hosts that
  shipped support days earlier. The range is widened instead.

### Compatibility matrix

The first push of #87 failed the 0.83.0 and 0.84.1 jobs. `session_compact_failed` does not exist
before 1.0.0, so registering it through `pi.on()` failed `npm run check` on those hosts. The
handler is registered through a local type alias; on hosts that never emit the event it is never
called. All four matrix jobs then passed.

`experiments/context/baseline-main.json` was regenerated in #87. The committed baseline was
measured against pi 0.87.0, and development moves to 1.0.0, whose system prompt differs, so the
measured byte counts move. It was regenerated again in #88 for the prompt text changes. Both
diffs are numeric and `context:gate` passes.

### Ranking

`scripts/update-ranking.py --release v0.32.0 --record-release` fetched a live reading:
rank 6 of 3,258 extensions, 145,600 downloads. Badges regenerated.

### Validation before tagging

`check`, `lint`, unit 1042, integration 31, e2e 20 with no skips, `test:selfcheck`,
`context:gate`, `npm pack --dry-run`. All green on 1.0.0 with the 0.83-0.87 compatibility jobs
green in CI.