# Credits

`extensions/tintinweb-subagents` is a partial Go port of **@tintinweb/pi-subagents** 0.19.0,
https://github.com/tintinweb/pi-subagents, by **tintinweb** (MIT, Copyright (c) 2026 tintinweb).

- Commit: `4f572eaa04c09d3dbc16e4a5f13a16b295e84e14` (release v0.19.0)
- The unmodified original (its tests included) is kept at [`port/oracle/`](port/oracle) as the equivalence oracle,
  with the license at [`port/oracle/LICENSE`](port/oracle/LICENSE).
- The model-facing text of the three tools (the descriptions and parameter schemas of `Agent`,
  `get_subagent_result` and `steer_subagent`, and the `Agent` guidelines), the prompts of the three default agents
  (`general-purpose`, `Explore`, `Plan`) and the wording of an agent's system prompt are tintinweb's, copied from the
  original byte for byte (`agent_description.txt`, `tool_specs.json`, `agent_guidelines.json`,
  `defaults_text.go`, `prompts.go`). `port/gen-spec.py` extracts them from what the original sends under Pi.

The Go code, the scenarios and the layer-1 tests were written for this Package by Michael Kinsy. The 122 test cases of
the original that cover the shipped slice (agent types and agent files) are ported as twins
(`port/upstream-tests.json` lists every title; `port/slices.json` names what is deferred and why). The event-bus
protocol with `@tintinweb/pi-tasks` and the completion notification follow the original. Modified paths: none of the
original is modified; the port is a separate implementation. What differs is listed in [README.md](README.md) and
[port/PORT.md](port/PORT.md).

This Package is not `nicobailon/pi-subagents`: that is a different project, ported as
[`components/subagents`](../subagents/README.md).
