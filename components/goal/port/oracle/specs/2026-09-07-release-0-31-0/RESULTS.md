# Direct comparison with v0.30.5

Baseline: `59826ec`, the previous npm and GitHub release. Candidate production source: `18323b8`; the release changes version metadata, documentation, benchmark evidence and the test discovery manifest only.

`CONTEXT-COMPARISON.json` joins the corrected previous-release captures to the final captures by fixture name. All 14 active-workflow fixtures sum to 145,134 → 89,993 extension-attributable characters, a 37.993% reduction. Characters and characters/4 estimates are not actual provider token counts. The original source captures and provider-payload cross-checks remain linked in the two optimization campaigns.

`RUNTIME-1-BEFORE.json` through `RUNTIME-4-BEFORE.json` retain the raw alternating baseline/candidate/candidate/baseline trials. `RUNTIME-COMPARISON.json` averages the two medians per side. Same machine, Node v26.7.0, real SDK 0.84.1, ten warm-ups and 21 samples per process. Each sample repeats dashboard/prompt operations 100 times and history activity ten times. Benchmark processes ran sequentially, without model calls or concurrent test suites.

| Operation | Before ms | After ms | Interpretation |
|---|---:|---:|---|
| Expanded dashboard, width 120 | 1.706056 | 0.284465 | 5.997× faster |
| Latest activity, 100,000 events | 11.883998 | 0.002892 | 4,110× faster for this indexed warm path |
| Prompt, identical unchanged input | 0.000295 | 0.008281 | About 8 microseconds extra content validation/current-usage assembly |

The previous prompt cache trusts revision/time fields; the current cache also checks actual task content and assembles fresh usage. The unchanged-input microbenchmark is slower by about 8 microseconds, within the predeclared 0.015 ms allowance for tiny paths. No release-level prompt-preparation speedup is claimed. This also illustrates why the intermediate campaign's 38.5× prompt improvement must not be presented as a comparison with v0.30.5.

Activity timing includes warm ledger/index lookup and formatting the latest five items. It excludes initial full-ledger parsing and index construction. Dashboard timing uses a 37,000-character objective and 50 long tasks at width 120, with SDK text metrics/wrapping. These are operation-specific measurements, not general application or cold-start speedups.

Reproduce after extracting each source commit into an isolated checkout with the same dependencies:

```sh
node --expose-gc --experimental-strip-types experiments/bench/release-comparison.mjs SOURCE_ROOT LABEL OUTPUT_JSON
```

`PACKAGE.json` identifies the checked publication tarball. All 51 shipped production modules byte-match the source; the packed extension imports against the real SDK and its recovery CLI accepts `--help`.
