# Product — GoalService ledger path simplification

Date: 2026-08-09

This is a behavior-preserving maintenance improvement. Goal mutations keep the
same durability order and best-effort ledger semantics, while multi-event
mutations use the existing batched ledger writer consistently.

## Invariants

- The goal file remains authoritative; a ledger failure never rolls back a
  successful goal-file write.
- Every failed individual ledger event remains observable through the existing
  diagnostic hook.
- A mutation with multiple ledger events writes one contiguous ledger block on
  the normal path.
- The public tools, commands, file format, and event format do not change.

## Success criteria

- Repeated ledger append/failure handling has one implementation in
  `GoalService`.
- Apply, task update, create, turn flush, and out-of-turn event append all use
  the shared path.
- Type checking and the complete test suite pass.
