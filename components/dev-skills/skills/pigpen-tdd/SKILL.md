---
name: pigpen-tdd
description: Test-driven development. Use when the user wants to build features or fix bugs test-first, mentions "red-green-refactor", or wants integration tests.
---

Adapted from `skills/engineering/tdd/SKILL.md` of mattpocock/skills at d81f3a1 (MIT). The
changes are listed in the Package's CREDITS.md.

# Test-Driven Development

TDD is the red → green loop. This skill is the reference that makes that loop produce tests worth keeping: what a good test is, where tests go, the anti-patterns, and the rules of the loop. Every section applies on every cycle: consult them before and during the loop, not after.

This is an explicit test-first mode. Load it when the user asks for TDD or when an accepted plan selects red-green-refactor for a crisp contract.

When exploring the codebase, read `GLOSSARY.md` or `CONTEXT.md` (if either exists) so test names and interface vocabulary match the project's domain language, and respect ADRs in the area you're touching.

## What a good test is

Tests verify behavior through public interfaces, not implementation details. Code can change entirely; tests shouldn't. A good test reads like a specification: "user can checkout with valid cart" tells you exactly what capability exists, and it survives refactors because it doesn't care about internal structure.

A test takes its expected value from an independent oracle: the spec, a worked example, a known-good literal. Never from the code's own computation.

## Seams: where tests go

A **seam** is the public boundary you test at: the interface where you observe behavior without reaching inside. Tests live at seams, never against internals.

**Test only at pre-agreed seams.** Before writing any test, write down the seams under test and confirm them with the user. No test is written at an unconfirmed seam. You can't test everything, so agreeing the seams up front is how testing effort lands on the critical paths and complex logic instead of every edge case. Prefer existing seams, and the highest seam possible.

Ask: "What's the public interface, and which seams should we test?"

## Anti-patterns

- **Implementation-coupled**: mocks internal collaborators, tests private methods, or verifies through a side channel (querying the database instead of using the interface). The tell: the test breaks when you refactor but behavior hasn't changed.
- **Tautological**: the assertion recomputes the expected value the way the code does (`expect(add(a, b)).toBe(a + b)`, a snapshot derived by hand the same way, a constant asserted equal to itself), so it passes by construction and can never disagree with the code. Expected values must come from an independent source of truth: a known-good literal, a worked example, the spec.
- **Horizontal slicing**: writing all tests first, then all implementation. Bulk tests verify _imagined_ behavior: you test the _shape_ of things rather than user-facing behavior, the tests go insensitive to real changes, and you commit to test structure before understanding the implementation. Work in **vertical slices** instead: one test → one implementation → repeat, each test a **tracer bullet** that responds to what the last cycle taught you.

## Rules of the loop

- **Red before green.** Write the failing test first, then only enough code to pass it. Don't anticipate future tests or add speculative features.
- **One slice at a time.** One seam, one test, one minimal implementation per cycle.
- **Refactoring is not part of the loop.** It belongs to the review stage (see the `pigpen-code-review` skill), not the red → green implementation cycle.
- **Mutation gate.** A test earns its keep only if it could fail. After green, flip one small detail in the code it covers, a comparison or an operator, and confirm the test goes red; restore, and confirm green. A passing test you could not make fail is decoration. Run the smallest slice early and locally; the full suite once at the end.
- **Cover the edges.** Exercise invalid, empty, boundary, and error-path inputs, not only the happy path.
- **Never bend a test to get green.** Never weaken, skip, hardcode, or delete a test to force it green. If a test is wrong, say so and fix it in the open.
