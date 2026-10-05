# Milestones

- Recorded approved existing-flow design before implementation. No commands, tools or settings added. Provider normalization remains a separate upstream concern.

- Implemented nullable proposal budget, visible old/new/saved limits, revision-safe confirmation, budget-change ledger events and bounded recovery through scheduler declarations. Added coalesced warning handling and per-model Responses documentation/upstream report draft.
- Regression work caught an accidental test-file assembly error (restored the original tests before continuing) and an omitted-budget recovery edge case; an ordinary tweak now retains budget_limited unless a budget edit was explicitly proposed. Updated the creation-report golden assertion for Budget: none.
- Intentional context baseline update: 24-fixture total serialized characters 262312 -> 262786, extension-attributable 150212 -> 150686. Drift comes from the nullable draft budget schema and guidance. No gate invariants relaxed; the remeasured context gate passes.
- Final validation: 1,026 tests pass, zero failures/skips, including scripted real SDK execution and retry/compaction coverage. TypeScript, ESLint, diff whitespace check and the 24-fixture context gate pass. Provider checks pass for three Responses compatibility captures and six existing real SDK payload captures; no live provider requests sent.
- No command/tool/settings count changes. Upstream report is prepared in UPSTREAM.md, not posted; provider normalization remains explicitly unresolved by this extension patch.

- 2026-09-22: Integrated against remote 0.31.6 with shared-root, host-dialog and cache-policy changes. Nullable budget proposals use the existing drafting tool and `/goal-tweak`; no command/tool added. Cross-version provider construction, full tests and benchmark/context results are recorded in ../2026-09-22-issue-and-pr-resolution-plan/VALIDATION.md. Upstream Responses normalization remains outside this extension fix.
