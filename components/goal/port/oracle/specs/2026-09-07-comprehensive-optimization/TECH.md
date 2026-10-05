# Technical approach

Baseline: `5e16557`, after merging the first campaign. Preserve it in an isolated checkout and run the same new measurement code against baseline and candidate.

Audit groups: settings and configuration; task derivation and mutations; goal/ledger storage and locks; prompt/tool context and child sessions; event hooks, continuation and compaction; dashboard/widget/dialog rendering; diagnostics, recovery, accounting and lifecycle controls.

Measure the existing complete benchmark matrix and history-scale campaign, plus focused repeated operations that expose work hidden by filesystem-only counters: settings resolution, content caches, paginated retrieval, activity presentation, session diagnostics, and context compaction. Measure serialized child prompts as well as executor requests with the corrected SDK context harness.

Prefer bounded caches with explicit validity contracts, avoiding mutable-object identity assumptions. Keep external refresh and session boundaries authoritative. Full-history reads are acceptable for explicit history/diagnostics, but repeated pages should reuse stable source work. Do not weaken locks, revision conflicts, evidence requirements, or ordered batch validation.

Validate optimized paths against uncached behavior, including in-place edits, focus/env/settings changes, Unicode, stale cursors, out-of-order activity and malformed history. Run the full suite, TypeScript, lint, context/provider compatibility and performance comparisons after implementation.

## Final mechanisms

- Settings resolution caches up to 16 layer/env combinations and returns caller-owned nested values/provenance. File mutation and explicit/session invalidation clear this cache.
- Task indexes use field comparisons against owned snapshots, with 32-entry and two-million-character allowances. Prompt fragments use task-index identities and content fields, with usage appended separately and equivalent cache bounds.
- Lossless detail sources cache at most 16 entries / 16 million characters. The ledger supplies an opaque read generation; append and refresh replace it. Caller-owned history arrays without a generation are always recompiled. Cursor hashes still cover actual content and section/task identity.
- Ordered batches build a task-location map on the private clone, updating locations if a transformation replaces a subtree. Writes still happen only after all validation; ledger events remain individual and ordered.
- ANSI wrap/truncate caches are shared across UI surfaces, contain all layout arguments in their keys, and return independent wrapped arrays. Visible-width caching remains the SDK's responsibility.
- Auditor policy is consolidated before payload data, and streaming previews inspect only the last 5/8 nonempty lines. Complete reports, objectives and contract data remain available.
- Version 3 derived ledger checkpoints include reconstructed Oracle results and reject malformed accumulator/advice state. Goal IDs are sanitized through a bounded per-read intern map during full ledger parsing. Persisted goal records, ledger events and session formats do not change.
- Session health discards parsed objects immediately. Profiling favored the native string splitter over a manual JavaScript line scan; a line-string array is retained during the scan, but no full parsed-session graph.

The campaign gate checks targeted speedups in both stub and real-SDK runs, no filesystem/context increase, and runtime p50 averages from alternating baseline/candidate/candidate/baseline trials. It uses 20% plus small absolute timing allowances for non-target paths; timings below a millisecond are also assessed by operation counts. Preserve unsuccessful initial measurements alongside the final evidence.
