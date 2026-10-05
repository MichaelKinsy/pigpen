# Milestones

- Trimmed repeated feature explanations and examples; moved detailed patch-release additions into docs/advanced-usage.md. Kept the complete everyday command table and concise run/token-limit guidance. Added the guide to packaged files so the README link works for npm consumers.
- Validation: relative documentation links resolve, npm pack dry-run includes the guide, and git diff --check passes. No runtime tests needed for this documentation-only change.
- User correction: the initial shortening removed too much. Restored the established README verbatim through Settings, including examples, tasks/subtasks, auditing and controls. Retained current compatibility and settings behavior; only the later technical material moves out. Removed the duplicated settings overview from the advanced guide.
- Removed compatibility notice and advanced-documentation cross-reference at the user's request. Added root AGENTS.md guidance to prevent routine feature/release work from expanding the README; necessary factual corrections and automated badge updates remain allowed.
- Release authorized: PR #81 merged at be5054a after all CI checks passed. Preparing documentation-only patch 0.31.8 through the existing publication workflow.
