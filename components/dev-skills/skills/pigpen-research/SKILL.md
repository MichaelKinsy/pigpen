---
name: pigpen-research
description: Investigate a question against primary sources and report findings with citations, provenance, and explicit uncertainty.
---

# Research

Adapted from Matt Pocock's `research` skill (MIT); see the Package's CREDITS.md.

Investigate the question against primary sources: official documentation, source code,
specifications, and first-party APIs. Follow each claim to the source that owns it.

Record each captured claim as one semantic unit:

- **Value:** what the claim says.
- **Source:** the exact citation, file, API, or measurement.
- **Basis:** `stated`, `inferred`, or `unverified`.
- **Reasoning:** why the evidence supports the claim or why uncertainty remains.

Do not present an inference as something the source stated. Do not merge conflicting
claims by silently replacing one source with another; show the conflict.

Findings go, in order:

1. the reply, when the asker is in this session;
2. the issue or decision record the research was meant to inform;
3. the project's established place for research notes, when the evidence fits neither.

If no durable research location exists, keep the findings in the reply and propose a
location. Never create a loose summary or report file merely because the research was
long.

When delegating to a background agent (if your setup has subagents), give it one
factual question, the decision it informs, source priority, exit criteria, and the
required citation shape. It returns evidence and citations to you; it does not decide.
Independent questions may run in parallel. Reduce their claims and surface conflicts
instead of concatenating reports.
