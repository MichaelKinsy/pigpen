# First-run cost of the aot bench Piglets

`node scripts/bench-aot-cost.mjs --binary <bench-ext10-aot|bench-ext10-split> --json <file>` measures what the aot runtime spends before a TypeScript set runs compiled. The compiled host is a slot image built on the user's machine from the pinned Pi-Bolt compiler (0.7.4, signature-checked); it is not part of the Binary and a later start pays none of this.

Measured 2026-10-12, linux/amd64, pig built from PiG commit `d92513e779` (single-file lock; not in the 0.5.0 candidate `8ba802d544`) (integrate-042 `a220c94a53` plus the single-file lock fix), a host at load average about 220 (so the seconds are a ceiling, not a benchmark), one run each, network downloads included:

| | bench-ext10-aot (10 TypeScript extensions) | bench-ext10-split (5 TypeScript, 5 Go-fused) |
|---|---:|---:|
| Piglet Binary size | 70,402,311 bytes | 70,410,503 bytes |
| start 1: `/bun-runtime install` (the first builds run on Bun) | 3.5 s | 2.8 s |
| start 1: `/aot-runtime install` (compiler download and verify) | 7.3 s | 5.3 s |
| start 2: build request recorded to compiled host on disk | 29.3 s | 25.2 s |
| compiled host (`pig-ts-host`) | 232,933,264 bytes | 232,839,056 bytes |
| image directory | 236,216,721 bytes | 236,122,513 bytes |
| start 3 runs the set on the compiled host | yes | yes |

The image is about 233 MB regardless of the set's size (it contains Bun, JavaScriptCore and ICU), so the arena's cost line is: one-off about 40 s and 236 MB of disk per machine, in the user's slot and outside the Binary. Equivalence of the two Piglets with the TypeScript originals: [equivalence.md](equivalence.md).
