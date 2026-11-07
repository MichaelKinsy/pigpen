# Mutation check: recovery*.go (RecoveryLane)

`port/mutations/RecoveryLane.json`: 299 deliberate defects in `recovery.go`, `recovery_notes.go`, `recovery_note_touches.go`,
`recovery_records.go`, `recovery_results.go` (inverted conditions and boundaries, one-character wording/label/separator changes,
arithmetic constants, dropped branches, swapped orderings, UTF-16 helpers replaced by `len`, dropped error propagation).

Run (the package copy must be self-contained: pigeq copies only the package directory, so a test that reads
`../../port/oracle` cannot run there; `TestNotesToolRegistrationMatchesTheOriginal` is removed from the scratch copy):

    ph run "cd components/posthorse && pigeq mutate --go <scratch copy>/extensions/posthorse --mutations port/mutations/RecoveryLane.json --unit-only --sdk-dir \$PIG_SDK_DIR --jobs 6"

What kills them: the golden replay `TestRecoveryGoldenMatchesOracle` (1,200 generated scenarios whose output was recorded from
the unmodified original, `port/tools/recovery-golden.mjs`), the twins, `recovery_units_test.go` (exact arithmetic and wording at
the budget boundaries) and other lanes' tests.

## Mutants removed as equivalent

| Mutation | Why it cannot change behaviour |
|---|---|
| results/call id fallback (`"Call ID: unknown"`) | `recoverableToolResults` only keeps results whose `toolCallId` is defined, so the fallback is unreachable. |
| results/trailing boundary (`index >= start`) | `entries[start]` is the assistant entry itself, never a tool result. |
| notes/clear keeps note | the kept zero-valued note has neither kind `read` nor `written`: no render loop lists it and a later touch treats it like an absent one. |
| touches/marker relative dot slash | `./.pi/notes/x` already matches the earlier `/.pi/notes/` marker (`lastIndexOf`), so the `./.pi/notes/` prefix branch never returns a different value. |
| touches/unscoped base keeps slash | `normalizeRelPath` strips leading slashes, so `/x` and `x` give the same base. |
| handoff/render batch empty guard (`&&`) | `plan.bare` is empty exactly when `plan.header` is, so `||` and `&&` agree. |
| handoff/render batch default budget | `perBlock` is only read inside a loop over `plan.blocks`; with no blocks it is unused. |
| touches/legacy notes agent segment required | `/.pi/notes/p/q/r/x` is caught by the earlier `/.pi/notes/` marker; only the `agent/` form reaches the regex, which this keeps matching. |
