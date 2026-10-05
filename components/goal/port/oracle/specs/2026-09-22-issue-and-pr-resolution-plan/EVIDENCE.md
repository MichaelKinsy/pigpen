# Review snapshot and reproducible observations

Date: 2026-09-22. Repository: `tmonk/pi-goal-x`.

## Pinned revisions

| Item | Revision | Observed status |
| --- | --- | --- |
| Remote main | `8b077aa6a1e9c83709bf639af79528501f8273d0` | Package 0.31.6 |
| Local HEAD | `5a7c4cd` | Package 0.31.5, existing dirty budget-recovery work |
| [PR #66](https://github.com/tmonk/pi-goal-x/pull/66) | `5ea6792e86e8e086ed697b3d8e6e997161562e3d` | Open, mergeable; CI action required |
| [PR #71](https://github.com/tmonk/pi-goal-x/pull/71) | `f0027df641a2ddab389316cd9c41538a239aa281` | Open, mergeable; CI action required |
| [PR #74](https://github.com/tmonk/pi-goal-x/pull/74) | `3c04190aeec4e33abd4f44eb80ce93995be746f6` | Open, mergeable; CI action required |
| [PR #75](https://github.com/tmonk/pi-goal-x/pull/75) | `440a94ee9bf114d0a5cd612f0869e48023d730e9` | Draft, mergeable; CI action required |
| [PR #79](https://github.com/tmonk/pi-goal-x/pull/79) | `3be6f0ad2e0d79a8d7adfc9c2585cbbb6638a142` | Open, mergeable; CI action required |

Useful read-only inventory commands:

```sh
gh repo view --json nameWithOwner,description,defaultBranchRef,url
gh issue list --state all --limit 500 --json number,title,body,state,comments,url
gh pr list --state all --limit 500 --json number,title,body,state,headRefName,url
gh pr view 79 --json headRefOid,baseRefOid,mergeable,mergeStateStatus,files,comments,reviews,commits
gh pr diff 79
gh api repos/tmonk/pi-goal-x/pulls/79/comments
gh run list --limit 15 --json databaseId,headSha,event,status,conclusion,workflowName
```

All open issue bodies/comments and all open PR bodies/diffs/reviews were read. The all-state inventory established the historical set; detailed historical reading concentrated on related caching, host UI, scheduling, provider, persistence and maintenance-scope changes.

## #79 targeted tests and missing combined cases

On a disposable main archive with the inspected #79 diff applied, using Node v26.7.0:

```sh
node --test tests/goal-live-retention.test.ts tests/goal-prompt-cache.test.ts
```

Result: 23 tests passed, zero failed, zero skipped. No full build, provider network request or complete PR test suite was run in this assessment.

The following additional probe imports the actual PR implementation:

```js
import { cacheGoalHistory } from './extensions/goal-prompt-cache.ts';
import { LiveTailRetention } from './extensions/goal-live-retention.ts';

const payload = { messages: [
  { role: 'user', content: [{ type: 'text', text: 'history' }] },
  { role: 'user', content: [
    { type: 'text', text: 'live', cache_control: { type: 'ephemeral' } },
  ] },
  { role: 'system', content: [], output_config: { effort: 'high' } },
] };
const result = cacheGoalHistory(payload, ['live']);
console.log({
  relocated: result !== undefined,
  markerStillOnLive: !!payload.messages[1].content[0].cache_control,
});

const retention = new LiveTailRetention();
const base = [{ role: 'user', content: 'start' }];
retention.apply('s', base, {
  state: 'policy', counters: 'Usage: 10 tokens\nBudget: 100 tokens',
});
const next = retention.apply('s', [
  ...base, { role: 'assistant', content: 'done' },
], { state: 'policy', counters: 'Usage: 20 tokens' });
console.log(next.transientContents);
```

Observed output:

```text
{ relocated: false, markerStillOnLive: true }
[
  'policy',
  'Usage: 10 tokens\nBudget: 100 tokens',
  'Usage: 20 tokens'
]
```

Interpretation: #79 does not incorporate #71's effort-marker fix. Its retention primitive also requires explicit policy invalidation/supersession for removed limits. The latter probe isolates the same omission behavior exposed by putting `budgetLine()` in append-only counters; it does not prove an end-to-end recovery flow always preserves that exact old state, since other lifecycle changes may reset it.

## Main-source findings

- `extensions/goal-state.ts`: both widget factories defer `loadGoalSettings(ctx.cwd)` and `goalActivityEvents(ctx, ...)` through captured `ctx`; `updateUI` also queues work through `lastUiCtx`.
- `extensions/prompts/goal-prompts.ts`: `goalPrompt()` adds lifetime `formatUsage(goal)` under an unqualified `Usage:` label.
- `extensions/goal-prompt-cache.ts`: current main uses `messages.at(-1)` to find the live block; #79 retains this lookup while #71 replaces it.
- `package.json`: all three host peers are `>=0.83.0 <0.85.0`; Node is `>=22.15.0`.
- `.github/workflows/ci.yml`: primary Linux/Node 24 checks include full tests, typecheck/lint, selfcheck, pack, audit and NAF; context/provider gates are not included.
- Storage paths exist in `storage/goal-files.ts`, `storage/goal-lock.ts`, `goal-ledger.ts`, `goal-recovery.ts` and debug operations in `goal-service.ts`; recovery also reads `.pi/.goals-pool-snapshot.json` outside the goals directory.

## Historical scope evidence

- [PR #64](https://github.com/tmonk/pi-goal-x/pull/64) deliberately makes execution contracts opt-in and restores implicit continuation. Loop-prevention work must respect that decision.
- [PR #61 maintainer comment](https://github.com/tmonk/pi-goal-x/pull/61#issuecomment-5670440918) and [PR #60 maintainer comment](https://github.com/tmonk/pi-goal-x/pull/60#issuecomment-5669007109) decline maintaining full test-suite portability.
- [Issue #30](https://github.com/tmonk/pi-goal-x/issues/30), [PR #32](https://github.com/tmonk/pi-goal-x/pull/32), [issue #67](https://github.com/tmonk/pi-goal-x/issues/67) and [PR #68](https://github.com/tmonk/pi-goal-x/pull/68) show why persisted history growth and provider-prefix stability require distinct validation.
- [PR #70](https://github.com/tmonk/pi-goal-x/pull/70) is closed, unmerged, and had no comments/reviews explaining closure in the retrieved record. Treat its claimed incidents as leads to verify, not a mandate to adopt its behavior.

## Limits of this assessment

No paid/live model tests, npm compatibility installs, standalone reload-crash reproduction, full PR CI reruns or implementation merges were performed. Reporter observations and contributor test claims are identified as such. Peer-range mismatch and relevant source paths were checked against remote main, not inferred from the older local checkout. GitHub and published host versions may change; refresh the inventory and support targets when executing the plan.
