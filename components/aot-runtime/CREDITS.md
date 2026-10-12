# Credits

`extensions/aot-runtime` is the `aot-runtime` extension of **PiG**, https://github.com/MichaelKinsy/PiG, written by **Michael Kinsy**
(Copyright (c) 2026 Michael Kinsy, MIT; see [LICENSE](LICENSE)), copied into Pigpen as a Package so the bench Piglets are pinnable here.
It downloads and runs Pi-Bolt's compiler (pinned in `lock.go`); its notices are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

- Original: `piglets/aot/extensions/aot-runtime` at the PiG revision pinned in `bench/sync-spec.json`.
- Copy: `node scripts/sync-bench-piglets.mjs --pig <PiG checkout>` rewrites the Go files from that revision; `--check` fails when one differs. `go.mod` and `go.sum` are Pigpen's own.
