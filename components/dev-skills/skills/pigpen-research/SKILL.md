---
name: pigpen-research
description: Investigate a question against high-trust primary sources and capture the findings. Use when the user wants a topic researched, docs or API facts gathered, or reading legwork delegated.
---

Adapted from `skills/engineering/research/SKILL.md` of mattpocock/skills at d81f3a1 (MIT).
The changes are listed in the Package's CREDITS.md.

Investigate the question. If your setup has subagents and the work can run in the background, spin up a **background agent** to do the research so you keep working while it reads; otherwise do it yourself.

The job:

1. Investigate the question against **primary sources** (official docs, source code, specs, first-party APIs), not a secondary write-up of them. Follow every claim back to the source that owns it.
2. Record each captured claim as one semantic unit:
   - **Value:** what the claim says.
   - **Source:** the exact citation, file, API, or measurement.
   - **Basis:** `stated`, `inferred`, or `unverified`.
   - **Reasoning:** why the evidence supports the claim or why uncertainty remains.

   Do not present an inference as something the source stated. Do not merge conflicting claims by silently replacing one source with another; show the conflict.
3. Put the findings where they belong, in order: the reply, when the asker is in this session; the issue or decision record the research was meant to inform; the project's established place for research notes, when the evidence fits neither (match the existing convention). If no durable location exists, keep the findings in the reply and propose one. Never create a loose summary or report file merely because the research was long.

When delegating, give the agent one factual question, the decision it informs, source priority, exit criteria, and the required citation shape. It returns evidence and citations to you; it does not decide. Independent questions may run in parallel. Reduce their claims and surface conflicts instead of concatenating reports.
