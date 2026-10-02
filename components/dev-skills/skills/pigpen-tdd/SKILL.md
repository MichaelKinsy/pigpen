---
name: pigpen-tdd
description: Test-driven development. Use when building features or fixing bugs test-first, or when the user mentions red-green or integration tests.
---

Adapted from Matt Pocock's `tdd` skill (MIT); see the Package's CREDITS.md. Changes: a
mutation gate, and a stricter rule that tests take their expected values from an
independent oracle.

This skill is an explicit test-first mode. Load it when the user asks for TDD or when
an accepted plan selects red-green-refactor for a crisp contract.

If the project keeps a glossary (`CONTEXT.md` or `GLOSSARY.md`), read it first so test
names and interface vocabulary match the domain language, and respect recorded
decisions (ADRs) in the area you touch.

## Seams

A **seam** is the public boundary you test at: where behavior is observable without
reaching inside. Tests live at seams, never against internals. Before writing any
test, write down the seams under test and confirm them with the user; agreeing them up
front is how testing effort lands on critical paths. Prefer existing seams, and the
highest seam possible.

## What a good test is

A test verifies behavior through the public interface and takes its expected value
from an independent oracle: the spec, a worked example, a known-good literal. Never
from the code's own computation. A good test reads like a specification and survives
refactors.

## Anti-patterns

- **Implementation-coupled**: mocks internal collaborators, tests private methods, or
  verifies through a side channel. The tell: it breaks on refactor when behavior did
  not change.
- **Tautological**: the assertion recomputes the expected value the way the code does,
  so it passes by construction and can never disagree.
- **Horizontal slicing**: all tests first, then all implementation. Work in vertical
  slices instead: one test, one minimal implementation, repeat, each a tracer bullet
  shaped by what the last cycle taught.

## The loop

Red before green: write the failing test, then only enough code to pass it. One seam,
one test, one slice per cycle. Refactoring belongs to review, not to the red-green
cycle.

**Mutation gate.** A test earns its keep only if it could fail. After green, flip one
small detail in the code it covers, a comparison or an operator, and confirm the test
goes red; restore, and confirm green. A passing test you could not make fail is
decoration. Run the smallest slice early and locally; the full suite once at the end.

Exercise invalid, empty, boundary, and error-path inputs, not only the happy path.
Never weaken, skip, hardcode, or delete a test to force it green. If a test is wrong,
say so and fix it in the open.
