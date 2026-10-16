# Credits

`extensions/` is the Go port of PiG's `bench/evals/fixtures/extensions/bench-ext-NN.ts` (the ten extensions of the overhead bench), written by
**Michael Kinsy** (Copyright (c) 2026 Michael Kinsy, MIT; see [LICENSE](LICENSE)), copied into Pigpen from PiG's ext-matrix branch so the bench Piglets are pinnable here.

- Original: `bench/evals/fixtures/extensions-go` at the PiG revision pinned for `bench-ext10` in `bench/sync-spec.json` (`standalone/` and `pig-ext10.yaml` are not copied).
- Copy: `node scripts/sync-bench-piglets.mjs --pig <PiG checkout>` rewrites the files from that revision; `--check` fails when one differs. `go.mod` and `go.sum` are Pigpen's own; the module keeps the fixture's path `example.com/benchext10`, which its imports use.
