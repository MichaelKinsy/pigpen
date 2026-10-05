# PRODUCT — Complete model-context accounting

## Purpose

This PR changes NO shipped behavior. It creates the measurement evidence that
lets later token-saving PRs (single-source prompts, UI/model payload
separation) prove they removed real duplicate bytes from the COMPLETE model
request — not just isolated prompt functions — without weakening outcomes.

The existing B4 benchmark measures `taskListBlock`, `continuationPrompt`, and
`goalPrompt` separately. It does not measure the composed request after
`before_agent_start`: extension system-prompt injection, the `context` hook's
message transformations, active tool schemas, historical custom messages, or
checkpoint history. This harness measures all of it.

## What ships

- `npm run context:measure` — captures and measures every fixture's composed
  request and writes `experiments/context/baseline-main.json`.
- `npm run context:gate` — CI-safe gate: recomputes the baseline in-process,
  requires deterministic equality with the committed artifact, requires tool
  schemas in every breakdown, requires checkpoint history ≤1 (post-#30), and
  requires every fixture ID covered. No network; no child agents.

## Fixtures

Twenty deterministic scenarios covering: taskless/10/50-task goals, current
contracted task, nested tasks, half-complete trees, long objectives, Sisyphus,
token budgets at 0%/75%/limit, paused, blocked, multiple open goals, latest
auditor rejection, post-compaction turn, stale checkpoint, guided drafting,
guided proposal, completion audit, audit rejection/rework, and get_goal
default/verbose.
