#!/usr/bin/env python3
"""Writes the model-facing text of the three tools from the golden recorded from the original under Pi:
extensions/tintinweb-subagents/agent_description.txt (the Agent description with {{TYPELIST}} and {{AGENTDIR}} in
place of the two dynamic parts) and tool_specs.json (the parameters of the three tools, and the descriptions of the
two fixed ones). The original is recorded with workflows, scheduling and worktree isolation switched off (the
scenario's setup writes .pi/subagents.json), which is what this port offers."""
import json, os, re
here = os.path.dirname(os.path.abspath(__file__))
ext = os.path.join(here, "..", "extensions", "tintinweb-subagents")
tools = None
for line in open(os.path.join(here, "golden", "tool-tools.jsonl")):
    e = json.loads(line)
    def find(o):
        if isinstance(o, dict):
            if isinstance(o.get("tools"), list) and any(t.get("name") == "Agent" for t in o["tools"]): return o["tools"]
            for v in o.values():
                r = find(v)
                if r: return r
        if isinstance(o, list):
            for v in o:
                r = find(v)
                if r: return r
    tools = find(e)
    if tools: break
by = {t["name"]: t for t in tools}
d = by["Agent"]["description"]
a = d.index("- general-purpose:")
b = d.index("\n\nCustom agents")
d = d[:a] + "{{TYPELIST}}" + d[b:]
assert "<tmp>/agent/agents/<name>.md" in d
d = d.replace("<tmp>/agent/agents/<name>.md", "{{AGENTDIR}}/agents/<name>.md")
open(os.path.join(ext, "agent_description.txt"), "w").write(d)
# subagent_type lists the available types and the agent dir
st = by["Agent"]["parameters"]["properties"]["subagent_type"]
assert "Available types: general-purpose, Explore, Plan." in st["description"]
st["description"] = st["description"].replace("Available types: general-purpose, Explore, Plan.", "Available types: {{TYPES}}.").replace("<tmp>/agent/agents/*.md", "{{AGENTDIR}}/agents/*.md")
# the Agent tool's promptGuidelines are in the source (the request carries only description and schema)
src = open(os.path.join(here, "oracle", "src", "index.ts")).read()
i = src.index('promptSnippet: "Launch autonomous sub-agents')
j = src.index("],", i)
items = re.findall(r'^\s+"((?:[^"\\]|\\.)*)",\s*$', src[i:j], re.M)
json.dump([json.loads('"' + x + '"') for x in items], open(os.path.join(ext, "agent_guidelines.json"), "w"), indent=1, ensure_ascii=False)
spec = {n: {"parameters": by[n]["parameters"], "description": by[n]["description"] if n != "Agent" else None} for n in ("Agent", "get_subagent_result", "steer_subagent")}
json.dump(spec, open(os.path.join(ext, "tool_specs.json"), "w"), indent=1, ensure_ascii=False)
