# rea-dashboard

The REA harness UI. It reads everything from what the `rea` member registered (`GetMcpServers`: the `rea` server's Node and
`rea.mjs` paths, and `payload.json` beside them) and from session events. It never sets REA's environment, never changes
the registration and never opens a second REA MCP session.

| Surface | What it shows |
|---|---|
| Header | `◆ REA <version> · <n> tools`, then one row of providers (Hopper, Ghidra, IDA, JADX, Kuna: ✓ ready, ✗ failing, ○ not configured) from one background `rea doctor --json` at load. `REA not registered` when there is no `rea` server (for example `--no-mcp`). |
| Title | `REA · <directory>` |
| Widget `rea-calls` | Up to five running and recent `mcp__rea__*` calls with target, elapsed time and result, including calls made inside codemode scripts (`parentToolCallId`). Hidden until the first REA call. |
| `/rea`, `alt+r` | Full-screen overlay, keyboard driven (Tab or ←/→ for tabs, ↑/↓, Enter, Esc or q). **Investigation**: this branch's REA calls; Enter shows one with its evidence and limitations first. **Targets**: what the calls touched, with *Investigate…* (sends a request to the agent), *Quick inspect* (one `rea inspect-artifact --json`, output capped at 64 KB) and *Derive piglet…* (asks the agent to run `pigpen-derive-a-harness`). **Providers**: doctor's verdicts and the environment variables each provider reads. **About**: REA, Node, payload path and digests; `/rea-runtime verify` checks the unpacked copy. Without a UI, `/rea` prints a one-line summary. |
| Tool cards | Compact call and result lines for direct `mcp__rea__*` calls: tool, target, ok or error, evidence and limitation counts. Calls inside codemode render in the codemode card; the widget and timeline cover them. |
| System prompt | One per-run section, `rea_harness`, that names REA's server and tool prefix. PiG's own prompt is unchanged. |

Each finished REA call is kept in the session as a `rea.call` entry (a bounded summary, never the full result), so the
timeline survives `/resume` and follows `/tree`. Doctor and quick inspect run the embedded Node with the environment left
as is and no shell.
