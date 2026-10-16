# Credits

`extensions/bunruntime` is the `bunruntime` extension of **PiG**, https://github.com/MichaelKinsy/PiG, written by **Michael Kinsy**
(Copyright (c) 2026 Michael Kinsy, MIT; see [LICENSE](LICENSE)), copied into Pigpen as a Package so the bench Piglets are pinnable here.

- Original: `piglets/standard/extensions/bunruntime` at the PiG revision pinned in `bench/sync-spec.json`.
- Copy: `node scripts/sync-bench-piglets.mjs --pig <PiG checkout>` rewrites the Go files from that revision; `--check` fails when one differs. `go.mod` and `go.sum` are Pigpen's own.
