# Context diff — baseline evolution across the program

All numbers from `experiments/context/baseline-main.json` snapshots
(composed request = extension system block + post-hook messages + ACTIVE tool
schemas; chars).

## PR A (issue #30) — before the harness existed

- Persisted checkpoint content per turn: ~6.4K → 68–73 chars.
- Provider-visible historical checkpoints: N → ≤1 (enforced by context:gate
  since PR D as historicalCheckpointChars = 0 on every fixture).
- 850-entry legacy recovery: ~5,440,000 → 51,850 checkpoint chars (~0.96%).

## PR E — single-source prompt (vs pre-E baseline)

| fixture | before | after | delta |
|---|---|---|---|
| active-regular-10-tasks | 9,655 | 9,476 | −179 |
| active-regular-50-tasks | 9,945 | 9,848 | −97 |
| current-contracted-task | 10,081 | 9,804 | −277 |
| nested-tasks | 10,098 | 10,001 | −97 |
| half-complete-tree | 9,565 | 9,386 | −179 |

Composition changes: current task rendered once; Next-pending only when
nothing else visible; no UI-shortcut text in model payloads; update_goal
schema deduplicated to capability + boundary.

## PR G — Oracle input surface

- update_goal gains `attempted_actions` (≤8 × 240 chars): +136 tool-schema
  bytes/request. Deliberate, documented in CONTEXT-BASELINE.md.

## Semantic invariants held at every step

objective exactly once and [PI GOAL ACTIVE] block exactly once on every
active-block fixture; verification contract exactly once when present;
historical checkpoint payload 0 everywhere; all lifecycle rules stated once
in the canonical policy block.
