# Model-context accounting (PR D)

Measures the COMPLETE model-facing request — extension-injected system block,
post-hook message list, and ACTIVE tool schemas — not isolated prompt strings.

    npm run context:measure   # re-capture all fixtures -> baseline-main.json
    npm run context:gate      # deterministic equality + invariants (CI-safe)

## Layout

- `fixtures.mjs` — 24 deterministic scenarios (fixed ids/timestamps).
- `capture-context.mjs` — drives the real extension handlers over a fixture;
  returns { baseSystem, extensionSystem, messages, tools }. Pure function
  calls; no network, no child agent, no live model.
- `measure-context.mjs` — ContextSizeBreakdown + serializeRequest.
- `semantic-invariants.mjs` — SemanticOccurrenceCounts (objective, contracts,
  current task, lifecycle policy markers, checkpoint/unfocused/stale markers).
- `run-measure.mjs` / `run-gate.mjs` — the two npm scripts.
- `baseline-main.json` — committed artifact; update ONLY with a spec rationale in the active campaign.

## Notes

- estimatedTokens = chars/4 heuristic (documented estimate, not live usage).
- Schema size follows the active lifecycle profile. Extension-attributable size includes SDK guidance, injected state, goal-tool/custom results, and child requests. Conversation serialization preserves tool arguments and result metadata.
- Post-issue-#30 invariant enforced by the gate: historical checkpoint payload
  visible to the provider must be 0 chars on every fixture.

## Runtime/token optimization campaign

The 2026-09-07 capture uses actual active profiles and the SDK system-prompt builder (including tool snippets/guidelines), includes host schemas, exercises drafting/compaction, and measures child audit/Oracle request surfaces separately. Explicit get_goal results are included in the read-tool fixture. Character-based estimates remain estimates. `node experiments/context/provider-crosscheck.mjs` compares six executor/auditor/Oracle captures against real SDK provider payloads, intercepting before network dispatch.

Use `CONTEXT_OUTPUT=<file> npm run context:measure` for isolated campaign outputs. The committed main baseline is updated only after semantic gates pass. The baseline rationale is recorded in specs/2026-09-07-runtime-token-optimization/MILESTONES.md.
