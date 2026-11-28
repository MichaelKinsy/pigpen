<setup>
Reusable setup: write a script, run `%load <path>` in a standalone `code` cell.
- Quoted paths and `local://` work; source is not echoed.
- Definitions persist; edited files require another `%load`.
Missing Python dependency: `%pip install <distribution>` (e.g. `PIL` comes from `pillow`).
Missing JS dependency: `%bun add <pkg>` installs into a managed per-project environment; `%environment project` selects project dependencies.
Percent commands are standalone cells. Install preserves kernel state; retry only the failed step.
</setup>
```
budget →
  budget.total, budget.spent(), budget.remaining()
  JS: await budget.total(), await budget.spent(), await budget.remaining()
  Ceiling +Nk advisory; +Nk! hard.
@tool / tool(fn, name=None, description=None)
JS: tool(fn, { name?, description?, parameters? })
  → kernel-hosted tool (schema inferred from type hints).
  Pass its name in task item `tools`, `agent(tools=…)`, or `workpool(tools=…)`; `tool.defined()`, `tool.undefine(name)`.
```
