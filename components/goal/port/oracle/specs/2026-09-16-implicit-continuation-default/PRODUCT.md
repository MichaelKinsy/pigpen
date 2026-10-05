# Implicit continuation by default (#63)

Successful executions of active auto-continuing goals continue immediately when the host is ready, without tool, task, progress, or disposition requirements. No inactivity delays or missing-disposition pauses apply by default. Final-task completion can continue into verification and audited goal completion.

One global/project setting, strictExecutionContract, defaults false. Opting in retains explicit ready/wait decisions and one repair then pause. Default mode accepts optional ready but rejects new waits without terminating. This is a user preference, not a protocol the agent should enable to continue.

Preserve explicit lifecycle actions, user cancellation, optional run/token limits, provider recovery, durable ownership and dispatch safeguards. Settings changes never renew allowance or resume paused goals. Honor existing waits and their original deadlines/check/repair bounds even after disabling strict mode; allow same-wait redeclaration, but no new waits. Resume can leave a wait.

Do not restore the pre-PR no-tool gate, introduce progress heuristics, or add pacing settings. Unproductive loops remain possible by design.
