---
name: pigpen-diagnosing-bugs
description: Diagnosis loop for hard bugs and performance regressions. Use when the user says diagnose or debug, or reports something broken, throwing, failing, or slow.
---

Adapted from Matt Pocock's `diagnosing-bugs` skill (MIT), condensed; see the Package's
CREDITS.md.

If the project keeps a glossary (`CONTEXT.md`), read it for a mental model of the
modules involved, and check recorded decisions (ADRs) in the area: a surprising
behavior is sometimes a decision that was written down.

## Phase 1: build a feedback loop

This is the skill; everything after is mechanical. Get a **tight** pass/fail signal
that goes red on this bug, and the bug is 90 percent fixed. Start with the real
surface that exposed the failure, then make that loop faster and more repeatable. In
rough order of preference:

1. A reproduction against the running system: the agent itself, a browser, a CLI, an
   API or an SDK.
2. A benchmark that reproduces a performance or scale regression.
3. Replay of a captured trace, payload, or event log through the real path.
4. An existing focused test that already reproduces the bug.
5. A headless browser or caller script that checks the visible symptom.
6. A throwaway harness: the minimal subset of the system that exercises the path.
7. A property or fuzz loop over random inputs, for "sometimes wrong output".
8. A bisection harness, so `git bisect run` can walk to the breaking change.
9. A differential loop: same input through two versions or configs, diffed.
10. A human-in-the-loop script, so manual actions still produce structured output.

Then tighten it: faster (cache setup, narrow scope), sharper (assert the specific
symptom, not "didn't crash"), deterministic (pin time, seed RNG, isolate the
filesystem). A two-second deterministic loop is a superpower; a flaky thirty-second
one is barely better than none.

For non-deterministic bugs, raise the reproduction rate rather than hunting a clean
repro: loop the trigger 100 times, parallelize, stress, inject sleeps. A 50 percent
flake is debuggable; a 1 percent flake is not.

If you genuinely cannot build a loop, stop and say so, list what you tried, and ask
for a reproducing environment, a captured artifact, or permission to add temporary
instrumentation. Do not hypothesize without a loop.

## Phase 2: localize

Consume the loop: bisect the code path, test hypotheses one variable at a time, add
instrumentation the loop reads. State each hypothesis before testing it, so a wrong
guess is information rather than drift.

## Phase 3: fix and prove

Fix the cause, not the symptom. The loop that failed now succeeds, and the benchmark
recovers when performance was the defect. Turn the smallest useful reproduction into
a permanent regression test, and check it can fail (revert the fix, watch it go red).
If the fix reveals a defect out of scope, report it plainly. Naming the defect does
not resolve it.
