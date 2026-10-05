# Implementation log

- Recorded approved behavior and implementation before code changes. Literal pre-PR rollback rejected because it would restore the no-tool continuation gate.

- Implemented implicit settlement, opt-in layered setting/UI, runtime wait validation, and queued-repair conversion at schedule and claim. Existing wait state retains strict semantics.
- Added declaration-free real SDK coverage, task/final-wrap-up tests, policy-change and inheritance tests, and menu persistence coverage.
- Type checking caught a missing proposedAt in a new test fixture; corrected it.
- Context gate initially detected expected prompt/tool-description drift. Regenerated the 24-fixture baseline without relaxing gate invariants. Total serialized characters changed from 261018 to 262312; extension-attributable characters from 148918 to 150212. This measures prompt size, not runtime token savings. Updated baseline gate passes.
- First full regression run passed 999/1001 tests. Updated the obsolete default-repair expectation and settings row count; the latter also exposed the boolean display formatter missing the new key, which is now fixed and asserted.
- Final full regression run: 1002 tests pass, zero failures/skips, including real SDK execution, retry/compaction and integration coverage. TypeScript and ESLint pass. Diff whitespace check passes; context gate passes with the reviewed baseline.
