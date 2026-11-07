# Mutation check: RolloverLane (retention.go, reminders.go, rollover.go)

`port/mutations/RolloverLane.json` holds deliberate defects of the three files (pigeq format); `RolloverLane-equivalent.json` holds the
mutants that are equivalent to the original and therefore cannot be killed (reasons below).

Command (on xd6702, against a scratch copy of the package that holds this lane's tests, the fixtures and the helpers they need,
because the whole shared package was not compiling at the time of the run):

```sh
ph run "cd components/posthorse && pigeq mutate --go <scratch copy of extensions/posthorse> \
  --mutations port/mutations/RolloverLane.json --unit-only --sdk-dir \$PIG_SDK_DIR --jobs 6"
```

Each mutant runs the copy's whole test suite (about 20 s) under pigeq's 180 s bound; 227 mutants took about 7.5 minutes with 6 jobs.

## Families (one or more mutants per function or contract row)

| Area | Functions / rows | Mutants |
|---|---|---|
| retention.go | `isCutRole`, `safeStart`, `messageCost`, `projectedCut` (materialization, owner index, keep), `pairedTail`, `boundedTail` (costs, budget, pairing, ids, first kept), `retainedTailOf` (default keep, boundary, prepared id), `sourceIDMatches`, `automaticRecovery` (limit, minimum page, payload cap, tail share, retained ids, notes, owner flag, handoff bound, first kept, error propagation) | 47 |
| reminders.go | `reminderMatches`, `strictIntMember`, `hasReminder`, `reminderBudget` (band, buffer, supported/enabled), `endsRun`, `reminderDecision`, `sameEntry`, the walk (`visit`, `result`, `scanAncestry`, hybrid), `reminderLookup` (reset, generation, key, certification, errors), `retainedProjection`, `countRetainedFromProjected`, `countRetainedMessages` | 56 |
| rollover.go | `runAborted`, `explicitReset`, `scanReminderMessages`, `staleReminderPredicate`, `chronologicalRetainedTail`, `dropStale`, the context hook, `automaticDraft`, `reportAutomaticFailure`, `freshWindowWithoutInput`, `reachedThreshold`, `fallbackTurnEligible`, `askQuestionRegistered`, `thresholdFallback`, `compactionCancelled`, `policyFromSettings`, `prepareAutomatic`, `automaticCompactionHook`, `registerRollover` (every registration, the turn_end branches, the reminder message) | 124 |

## Result

227 mutants: 227 killed, 0 survived, 0 invalid (final run, `real 5m47s`). Every mutant of `RolloverLane.json` is killed by a test that follows the TS code it pins (the tests are in
`rollover_mutation_test.go`, `rollover_hooks_test.go`, `reminders_test.go`, `retention_unit_test.go`, `retention_pi_test.go`, `retention_recovery_test.go`
and `twin_posthorse_rollover_test.go`). Survivors of the first run that became tests: the retention rules on hand-built projections (system and compaction
entries, the zero-token tail, the prepared id, the boundary), the reminder predicates (`===` strictness, queued reminders, run end), the walk
(context-window boundary, kept range before a window, order, certification), the cache lifecycle (reset, session and file key, read errors), the abort and
cancellation checks (run signal, request cancellation before the hook, after the last host read, errors after a cancellation), the exact shapes of the
explicit-reset and threshold drafts (limit, queued messages, deferral, `continue`, earlier drafts), the sentinel and the details, the notes recovery through
a fork binding, and every host-read error propagated.

One production change came with the tests: `registerAutomaticRollover`'s handler is now the named function `automaticCompactionHook`, so that a test can run it
on a request that is already cancelled (audit finding C-04). No behaviour changed.

## Equivalent mutants (`RolloverLane-equivalent.json`)

| Mutant | Why it cannot change behaviour |
|---|---|
| `budget-window-from-usage-lost` (`budgetFor(host, &window)` -> `budgetFor(host, nil)`) | `budgetFor(host, nil)` defaults the window to `host.getContextUsage().contextWindow`, and `window` was just read from that same (memoized) usage: the same value. |
| `filter-always-replaces` (`if changed \|\| reordered` -> `if true`) | The SDK compares the returned list with the one it was given, element by element and by map identity (`contextEventResult`): an unchanged list is reported unchanged whatever the handler returns, so the reply on the wire is the same. |
| `fresh-boundary-included` (`projected[boundary+1:]` -> `projected[boundary:]`) | The boundary entry is a compaction, and the loop skips every compaction entry, so including it adds nothing. |
| `recovery-handoff-limit-exclusive` (`> limit` -> `>= limit`) | The record builder never returns a record of exactly its limit: searched over 6,000 owner-input lengths with the limit set to the record's own natural length (1,000 to 4,462 characters), the builder shrinks the record (to 501 characters at limit 1,000..1,020) rather than filling it, so equality is not reachable. Not a proof over all inputs; the boundary of the original (`handoff.length > limit`) is kept as is. |

Not in either list and not mutated: the dead guard `if start < 0` after the prepared-id lookup in `retainedTailOf` (`projectedCut` always returns an index) and the `maxTokens <= 0`
guard (a zero budget retains nothing either way).
