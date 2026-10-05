---
name: pigpen-code-review
description: Review the diff since a fixed point along two axes, standards and spec, and report them side by side.
disable-model-invocation: true
---

Adapted from Matt Pocock's `code-review` skill (MIT); see the Package's CREDITS.md.

Review the diff between `HEAD` and a fixed point the user names. This is a
user-selected evaluation after the change works. Read the evidence that it works (run
output, test results, a manual check) before reviewing. If there is none, report that
gap instead of substituting a broad automated suite. Focused commands may verify a
concrete finding.

## 1. Pin the fixed point

A commit, branch, tag, or the main branch. If the user named none, ask. Capture the
diff once with `git diff <fixed-point>...HEAD` (three-dot, against the merge-base) and
the commit list with `git log <fixed-point>..HEAD --oneline`. Confirm the ref resolves
and the diff is non-empty before going further.

## 2. Find the spec

In order: issue references in the commit messages; a path the user passed; a spec
under `docs/`, `specs/`, or a scratch directory matching the branch. If none exists,
the spec axis reports "no spec available" rather than inventing one.

## 3. Run the axes

If subagents are available, run the two axes as parallel subagents so they do not
pollute each other's context; otherwise run them one after the other.

- **Standards**: does the code follow this repository's documented standards
  (contributing guide, agent instructions, lint and style rules)? Each finding is a
  judgement call labelled as such, never a hard violation, and anything tooling
  already enforces is skipped.
- **Spec**: does the diff faithfully implement the originating issue or spec? Missing
  behavior, invented behavior, and silent scope changes all count.

## 4. Aggregate

One report, two labelled sections, each finding tied to a file and line. Findings
only; whether a defect can wait is the user's call. End with the single most
important finding, not a summary of all of them.
