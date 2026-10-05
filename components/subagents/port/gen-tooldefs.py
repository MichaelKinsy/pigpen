#!/usr/bin/env python3
"""Writes extensions/pi-subagents/tooldefs.json: the four tool definitions the model sees (description and JSON Schema), taken
from the request the ORIGINAL sent under Pi 1.0.0 (port/golden/list.jsonl), so the Go port declares the same tools."""
import json, os
here = os.path.dirname(os.path.abspath(__file__))
reqs = [json.loads(l) for l in open(os.path.join(here, "golden", "list.jsonl"))]
reqs = [e["data"] for e in reqs if e.get("ch") == "llm"]
tools = {t["name"]: t for t in reqs[1]["tools"]}
out = {n: {"description": tools[n]["description"], "parameters": tools[n]["parameters"]} for n in ("bg_wait", "subagents_enable", "subagent_supervisor", "subagent")}
with open(os.path.join(here, "..", "extensions", "pi-subagents", "tooldefs.json"), "w") as f:
    json.dump(out, f, indent=1, ensure_ascii=False)
    f.write("\n")
