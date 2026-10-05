# MILESTONES — Drafting Prompt Normalization

## 2026-05-17 — Initial implementation

### Design phase
- Analyzed root cause: "going in circles / frustrated" language is emergent LLM behavior, not explicitly coded. The agent detects repeated propose → Continue Chatting cycles and generates apologist meta-commentary.
- Identified minimal fix: frame Continue Chatting as refinement (single word change in drafting prompt) + reframe post-Continue-Chatting tool result from negative to neutral.
- Added benchmark experiment case for before/after comparison.

### Implementation
- **goal-draft.ts**: Changed `"Continue Chatting means keep clarifying"` → `"Continue Chatting means keep refining through normal proposal cycles"` — normalizes iteration as expected refinement without adding new bullet points.
- **goal.ts**: Reframed post-Continue-Chatting tool result: `"User clicked 'Continue Chatting'. The goal was NOT created..."` → `"Goal draft refinement requested (Continue Chatting). The goal was not created — drafting remains active..."` — removes all-caps "NOT" and frames as refinement request.
- **goal-questionnaire.ts**: Added `PI_GOAL_AUTO_CONFIRM=0` override so benchmark harness can disable headless auto-confirm and simulate Continue Chatting.
- **Tests**: Updated existing `goalDraftingPrompt` test to verify the new wording is present in both goal and sisyphus modes.
- **Benchmark**: Created `experiments/cases/C19-iteration-frustration-benchmark/` with INPUT.md (4-turn drafting simulation), benchmark.sh (NDJSON analysis for 9 cycling language patterns), and BENCH.md (before/after usage docs).
- **Spec**: PRODUCT.md and TECH.md documenting root cause, approach, and implementation plan.

### Validation
- All 90 existing tests pass (no regressions).
- New assertions in `goalDraftingPrompt` test verify normalization text appears in both modes.
- Spec documentation complete.

### Decisions
- Minimal approach: single word change in existing prompt line + tool result reframe, no new bullet points, no runtime iteration counter.
- Benchmark patterns chosen to detect apologist/cycling language: "going in circles", "round and round", "apologis", "going around", "feel like we're", "we keep going", "we seem to be", "this is getting", "you seem".
