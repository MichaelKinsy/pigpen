# Deferred twin skips

Upstream test cases whose Go twin cannot be written until another slice exists. Each is a `tskip` in the twin tests with the
same reason; the final reconciliation turns them into twins once the slice lands.

Format: ledger file | title | owning slice | what must exist for the twin

capability-ceiling-agent-allowlist | includes allowedAgents in the child runtime config and audit metadata | child session runtime (`runs/shared/child-launch.ts`, `child-runtime-config.ts`) | a child launch builder that composes the child tool plan (`policy_toolplan.go`) with the child runtime config (the ceiling, depth, supervisor and nested route the child reads) and reports the capability audit
capability-ceiling-agent-allowlist | applies the selected agent's descendant ceiling only after its parent-authorized launch | child session runtime (`runs/shared/child-launch.ts`) | the same launch builder, taking `descendantAllowedAgents` and intersecting it into the child's ceiling after the parent-authorized launch
capability-ceiling-agent-allowlist | propagates the detached runner step snapshot through the common launch seam | background runs, detached runner (`runs/background/runner-child-launch.ts`) | the runner's step snapshot (`allowedAgents`, ceiling) passed through the same launch builder
