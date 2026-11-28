```
agent(prompt, *, agent?="task", label?=None, schema?=None, schema_mode?=None, isolated?=None, apply?=None, merge?=None, tools?=None) → AgentHandle
  Spawns a background subagent and returns immediately.
  - `agent` selects a discovered agent; omit it to use `task`.
  - Handle: `.id`, `.handle` ("agent://<id>"), `.status`, `.done()`, `.wait(timeout?)` → final text (parsed with `schema`),
    `.send(message)`, `.cancel()`, `.output()`.
  - Unwaited results auto-deliver like async jobs.
  - `schema` overrides agent/session schemas.
  - `schema_mode`: "permissive" warns after retries, "strict" fails; omitted → session setting, else "permissive".
  - `isolated` runs it in a worktree (requires `task.isolation.enabled`).
  - `apply=False` keeps its changes as patch/branch artifacts instead of applying them.
  - `merge=False` forces patch mode, otherwise `task.isolation.merge` decides.
  - `tools`: names of tools you defined with `@tool` / `tool(fn)` that the child may call.
  - Python: call without `await`; `await agent(…)` returns the final result, not a handle (`await h` = `h.wait()`).
  - JS: ONE trailing object — `h = await agent(prompt, { agent, label, schema, schemaMode, isolated, apply, merge, tools })`.
    - Without `await agent(…)`, `.id`/`.handle` are undefined.
    - Every handle method returns a Promise; `.status` is `await h.status()`.
workpool(agent?=None, *, name?=None, context?=None, tools?=None) → WorkPool
  Keep-alive worker pool for a batch of independent items.
  - `.push(*items)` (strings) → item ids; `.status()`; `.peek()`; `.close()`.
  - Pool name = async job id; results auto-deliver.
  - The first full drain closes the pool: push the whole batch while it is active; next wave → new pool.
  - Completely blocked? Leave `eval` and call the `wait` tool (not kernel `wait()`); NEVER poll.
  - JS: `pool = await workpool(agent, { name, context, tools })`; `await` each pool method.
```

The cell `timeout` counts only the cell's own work, not waits on `agent()`/`wait()`/`tool.task(…)`, so a subagent can outlast it.

<dag>
Acyclic waves of handles:
- **Name nodes.** `h = agent(…)` (JS: `await agent(…)`) returns at once; `h.handle` is `agent://<id>`.
- **Wire edges.** Put an upstream `.wait()` result or `.handle` in the downstream prompt. Bulk: `write("local://<name>.md", …)`.
- **Kernel `wait(hs)` (JS: `await wait(hs)`)** = wave barrier. Open-ended item streams → `workpool()`.
- **Isolate failure.** `wait(hs, raise_errors=False)` (JS: `await wait(hs, { raiseErrors: false })`) keeps a failure in its slot; only that subtree degrades.
- **Acyclic only.** Never feed an agent's handle or result back into one of its own upstream agents.
</dag>
