---
name: pigpen-handoff
description: Compact the current conversation into a handoff document for another agent to pick up.
argument-hint: "What will the next session be used for?"
disable-model-invocation: true
---

Adapted from `skills/productivity/handoff/SKILL.md` of mattpocock/skills at d81f3a1 (MIT).
The changes are listed in the Package's CREDITS.md.

Write a handoff document summarising the current conversation so a fresh agent can continue the work. Save to the temporary directory of the user's OS - not the current workspace. A handoff is session memory, not a deliverable: do not commit one to the repository.

The document is an index, not a store. Durable knowledge already has a home: specs, ADRs, glossaries, plans, issues, commits, and diffs. Do not duplicate content already captured in those artifacts; reference them by path or URL instead. What belongs inline is only what exists nowhere else yet: the current state of the task, the next step, the dead ends already tried, and the constraints spoken but not yet written down. If something inline deserves to outlive the handoff, file it properly first (a decision record, a glossary term, an issue comment) and then point at it.

Include a "suggested skills" section in the document, naming which skills the next agent should load, and name the issue or ticket the session should pick up, if one exists.

Redact any sensitive information, such as API keys, passwords, or personally identifiable information.

If the user passed arguments, treat them as a description of what the next session will focus on and tailor the doc accordingly.
