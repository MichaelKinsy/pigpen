---
name: pigpen-handoff
description: Compact the current conversation into a handoff document for the next session's agent.
---

Adapted from Matt Pocock's `handoff` skill (MIT); see the Package's CREDITS.md.

Write a handoff document so a fresh agent can continue this work, and save it to the
operating system's temporary directory, never the workspace. A handoff is session
memory, not a deliverable: do not commit one to the repository.

The document is an index, not a store. Durable knowledge already has a home: specs,
ADRs, glossaries, plans, issues, commits, and diffs. Reference each by path or URL and
never duplicate its content. What belongs inline is only what exists nowhere else yet:
the current state of the task, the next step, the dead ends already tried, and the
constraints spoken but not yet written down. If something inline deserves to outlive
the handoff, file it properly first (a decision record, a glossary term, an issue
comment) and then point at it.

Include a "suggested skills" section naming the skills the next session should invoke,
and name the issue or ticket the session should pick up, if one exists.

Redact secrets: API keys, passwords, personal information.

If the user passed arguments, treat them as what the next session will focus on, and
shape the document for that.
