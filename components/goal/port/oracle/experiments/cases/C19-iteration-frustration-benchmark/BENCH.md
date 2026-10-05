# C19 — Drafting Iteration Frustration Benchmark

Tests whether repeated proposal-refinement cycles trigger the agent to
generate "going in circles" / cycling / apologist language.

## Prerequisites

Same as other experiment cases: working model credentials, pi installed,
and the experiment harness available.

## How to Run

### Before the change (baseline)

```bash
# Stash changes or checkout the baseline commit first
PI_GOAL_AUTO_CONFIRM=0 bash experiments/harness/run.sh C19-iteration-frustration-benchmark
# Find the run directory (shown in output, or check latest):
ls experiments/cases/C19-iteration-frustration-benchmark/runs/
# Analyze the run:
bash experiments/cases/C19-iteration-frustration-benchmark/benchmark.sh experiments/cases/C19-iteration-frustration-benchmark/runs/<run-dir>
```

Expected baseline result: `"verdict": "FAIL"` and `"has_cycling_language": true`
(the agent detects iterative cycles and produces apologist/frustrated commentary).

### After the change

```bash
# Apply the prompt normalization changes, then:
PI_GOAL_AUTO_CONFIRM=0 bash experiments/harness/run.sh C19-iteration-frustration-benchmark
# Analyze the run:
bash experiments/cases/C19-iteration-frustration-benchmark/benchmark.sh experiments/cases/C19-iteration-frustration-benchmark/runs/<run-dir>
```

Expected result: `"verdict": "PASS"` and `"has_cycling_language": false`
(the normalized prompt prevents the agent from generating cycling language).

### Comparing multiple runs

Run each configuration 3+ times for statistical confidence, since model
behavior is non-deterministic:

```bash
PI_GOAL_AUTO_CONFIRM=0 bash experiments/harness/run.sh C19-iteration-frustration-benchmark --count 3
```

Then analyze each run and check how many pass vs fail.

## What the Benchmark Analyzes

The `benchmark.sh` script scans all assistant text messages in the captured
conversation (raw.ndjson) for patterns that indicate the agent perceives
iteration as problematic:

| Pattern | Example |
|---------|---------|
| `going in circles` | "we seem to be going in circles" |
| `round and round` | "going round and round on this" |
| `apologis` | "I apologize", "apologise for the repetition" |
| `going around` | "we keep going around on this" |
| `feel like we're` | "feel like we're stuck" |
| `we keep going` | "we keep going back and forth" |
| `we seem to be` | "we seem to be repeating ourselves" |
| `this is getting` | "this is getting repetitive" |
| `you seem` | "you seem frustrated" |

Any match → verdict FAIL (cycling language detected).
Zero matches → verdict PASS (no cycling language).

## Output Format

```json
{
  "case": "C19-iteration-frustration-benchmark",
  "run_at": "2026-05-17T12:00:00Z",
  "run_dir": "/path/to/run",
  "total_assistant_messages": 8,
  "patterns_checked": ["going in circles", "round and round", ...],
  "pattern_matches": {"going in circles": 0, "apologis": 1, ...},
  "has_cycling_language": false,
  "verdict": "PASS"
}
```
