# AGENTS.md

Spec directories live under `specs` unless a nested AGENTS.md documents a more specific convention.
Spec directory names use `YYYY-MM-DD-kebab-feature`, for example `2026-05-13-drafting-runtime-simplification`.
Spec directories include `PRODUCT.md`, when implementation planning is useful `TECH.md`, and a free-form `MILESTONES.md` implementation log.
`MILESTONES.md` records meaningful implementation milestones, failed attempts, setbacks, fixes, validation notes, and decisions without a strict schema.
When a user steers behavior mid-workflow, update `PRODUCT.md` first when behavior changes, then `TECH.md`, then implementation, tests, and `MILESTONES.md` as needed.

## README scope

Keep `README.md` as the existing user overview through Settings, followed by License. Do not append feature explanations, release notes, compatibility/version notices, troubleshooting, implementation details, validation results, or links advertising additional documentation unless the user explicitly requests that README change. A code change does not by itself authorize expanding the README.

Make only necessary factual corrections within existing sections; keep them brief and preserve the layout and examples. Put detailed usage and troubleshooting in `docs/`, release changes in `CHANGELOG.md`, and implementation/validation notes in the relevant spec. Preserve automated ranking badge updates.
