# Prompt caching across goal execution

Issue #67 exposes mutable state ahead of conversation history. Preserve a stable provider prefix across ordinary turns, autonomous checkpoints, and tool loops. Current objective, tasks, usage, budget, lifecycle, scheduling, auditor feedback, and compaction steering must remain available at the request tail without persisting full state on every request. Keep historical checkpoints bounded and stable in position. Preserve stale-dispatch guards, tool-result pairing, and delegation isolation.

Caching remains controlled by Pi and the provider; do not override user cache settings or promise cache hits. Audit drafting, tool profiles, auditor and Oracle sessions as well as the reported hook. Validate serialized provider requests without paid API calls and document unavoidable invalidation boundaries.
