#!/usr/bin/env python3
"""Writes the model-facing text of the three tools from the golden recorded from the original under Pi:
extensions/tintinweb-subagents/agent_text.json (the Agent description templates of every mode and the optional parts, from
the original's source, checked against the golden) and tool_specs.json (the parameters of the three tools, and the descriptions of the
two fixed ones). The original is recorded with workflows, scheduling and worktree isolation switched off (the
scenario's setup writes .pi/subagents.json), which is what this port offers."""
import json, os, re
import textwrap
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


# ---- the description texts of every mode and the optional parts, from the original's source (index.ts) ----
src = open(os.path.join(here, "oracle", "src", "index.ts")).read()
inv = open(os.path.join(here, "oracle", "src", "invocation-config.ts")).read()

def template_literal(text, start):
    """The text of the JS template literal that opens at text[start] == '`', with its escapes resolved."""
    assert text[start] == "`"
    out, j = [], start + 1
    while True:
        c = text[j]
        if c == "\\":
            n = text[j + 1]
            out.append({"n": "\n", "t": "\t"}.get(n, n))
            j += 2
            continue
        if c == "`":
            return "".join(out)
        out.append(c)
        j += 1

def literal_after(text, marker):
    return template_literal(text, text.index("`", text.index(marker) + len(marker)))

full = literal_after(src, "const fullAgentToolDescription = ")
compact = literal_after(src, "const compactAgentToolDescription = ")
for a, b in (("${buildTypeListText()}", "{{TYPELIST}}"), ("${getAgentDir()}", "{{AGENTDIR}}"),
             ("${isolationGuideline}", "{{ISOLATIONGUIDELINE}}"), ("${scheduleGuideline}", "{{SCHEDULEGUIDELINE}}")):
    assert a in full, a
    full = full.replace(a, b)
for a, b in (("${buildCompactTypeListText()}", "{{COMPACTTYPELIST}}"), ("${getAgentDir()}", "{{AGENTDIR}}"),
             ("${isolationCompactGuideline}", "{{ISOLATIONCOMPACTGUIDELINE}}")):
    assert a in compact, a
    compact = compact.replace(a, b)
assert "${" not in full and "${" not in compact
schedule_guideline = literal_after(src, "const scheduleGuideline = isSchedulingEnabled()\n    ? ")
isolation_guideline = literal_after(src, "const isolationGuideline = isWorktreeIsolationEnabled()\n    ? ")
isolation_compact = literal_after(src, "const isolationCompactGuideline = isWorktreeIsolationEnabled()\n    ? ")
i = src.index("const scheduleParamShape")
schedule_description = re.search(r"description:\s*((?:'[^\n]*' \+\n\s*)*'[^\n]*'),\n", src[i:]).group(1)
schedule_description = "".join(eval(p.strip()) for p in schedule_description.split(" +\n"))
j = inv.index("const isolationParamShape")
isolation_description = eval(re.search(r"description:\s*\n?\s*('(?:[^'\\]|\\.)*'),", inv[j:]).group(1))

text = {"full": full, "compact": compact, "scheduleGuideline": schedule_guideline,
        "isolationGuideline": isolation_guideline, "isolationCompactGuideline": isolation_compact,
        "scheduleDescription": schedule_description, "isolationDescription": isolation_description}

# the all-off description the golden carries must be the full template with every optional part empty
all_off = full.replace("{{ISOLATIONGUIDELINE}}", "").replace("{{SCHEDULEGUIDELINE}}", "")
assert all_off.replace("{{TYPELIST}}", "").replace("{{AGENTDIR}}", "") == d.replace("{{TYPELIST}}", "").replace("{{AGENTDIR}}", ""), "the source template and the recorded description differ"

# the parameter schemas of the optional parts are the ones Pi sent with every feature on
golden = os.path.join(here, "description-golden", "desc-full-sched-iso-wf.jsonl")
if os.path.exists(golden):
    on = None
    for line in open(golden):
        e = json.loads(line)
        def find2(o):
            if isinstance(o, dict):
                if isinstance(o.get("tools"), list) and any(t.get("name") == "Agent" for t in o["tools"]): return o["tools"]
                for v in o.values():
                    r = find2(v)
                    if r: return r
            if isinstance(o, list):
                for v in o:
                    r = find2(v)
                    if r: return r
        on = find2(e)
        if on: break
    props = {t["name"]: t for t in on}["Agent"]["parameters"]["properties"]
    assert props["isolation"]["description"] == isolation_description
    assert props["schedule"]["description"] == schedule_description
    text["isolationParam"] = props["isolation"]
    text["scheduleParam"] = props["schedule"]
else:
    raise SystemExit("record port/description-golden first (pigeq record --scenarios port/description-scenarios ...)")
json.dump(text, open(os.path.join(ext, "agent_text.json"), "w"), indent=1, ensure_ascii=False)
