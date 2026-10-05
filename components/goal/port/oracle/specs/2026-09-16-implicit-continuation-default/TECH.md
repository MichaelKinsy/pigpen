# Implementation

Use the existing scheduler atomic write and claim path for inferred ready decisions. Reload effective settings at settlement/declaration/claim. Default settlement ignores repairUsed outside outstanding waits. Convert unclaimed historical repair decisions to ordinary ready before scheduling and again at claim if policy changed. Keep persisted scheduler version and historical fields compatible.

Add the boolean through layered settings parsing, resolution, persistence, reporting and settings UI. Make prompt guidance conditional on strict mode or an outstanding grandfathered wait and include both in the fragment cache key. Keep tool schemas stable; reject new waits at the mutation boundary before modifying state.

Validate declaration-free execution, strict regressions, settings inheritance and mode changes, queued repairs, grandfathered waits, cancellation/claims, SDK retries/compaction, and prompt context. Run typecheck, lint, full tests and context gate.
