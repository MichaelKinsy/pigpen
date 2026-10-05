import json, os
OUT_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "scenarios")

def noul(v): return {"type": "noul", "noul": v}
def score(v, c): return {"type": "score", "score": v, "confidence": c, "legend": {"0": "None, it only reads", "1": "Small", "2": "Large", "3": "Severe"}, "probabilities": {"0": 0.05, "1": 0.1, "2": 0.15, "3": 0.7}}
def choice(name, c): return {"type": "choice", "choice": name, "confidence": c, "probabilities": {name: c}}
def gate(d, e, b, i, ic): return {"model": "jev-eq", "answers": {"destructive": noul(d), "exfiltration": noul(e), "beyond_scope": noul(b), "impact": score(i, ic)}, "usage": {"input_tokens": 100, "output_tokens": 4}}
def out(leak, cls, c): return {"model": "jev-eq", "answers": {"leaks_secret": noul(leak), "failure_class": choice(cls, c)}}
CLEAR = gate(0.03, 0.04, 0.4, 0.02, 0.9)
FLAG = gate(0.99, 0.79, 0.98, 3.0, 0.91)
OUTCLEAR = out(0.01, "no_failure", 0.99)

def route(body, times=1):
    # A raw body keeps the answers in question order, as the API returns them (a JSON map would sort them).
    return {"method": "POST", "path": "/v1/systemone", "body": json.dumps(body), "headers": {"Content-Type": "application/json"}, "times": times}

def config(extra=None):
    cfg = {"enabled": True, "acknowledged": True, "display": "plain", "backend": "typesafe",
           "endpoint": "{{server:jev}}/v1/systemone", "model": "jev-eq", "retries": 0, "timeoutMs": 5000}
    for k, v in (extra or {}).items(): cfg[k] = v
    return json.dumps(cfg)

def bash(cmd): return {"name": "bash", "arguments": {"command": cmd}}

def scenario(name, desc, routes, llm, steps, extra_cfg=None, files=None, tools_note=None):
    sc = {"name": name, "description": desc,
          "agentFiles": {"pi-jev.json": config(extra_cfg)},
          "env": {"TYPESAFE_API_KEY": "tsk-eq-key-0123456789"},
          "servers": {"jev": {"recordHeaders": ["authorization"], "routes": routes}}}
    if files: sc["files"] = files
    if llm: sc["llm"] = llm
    sc["steps"] = steps
    with open(f"{out_dir}/{name}.json", "w") as f:
        json.dump(sc, f, indent=2); f.write("\n")

out_dir = OUT_DIR
def prompt(msg, name="prompt", **kw):
    s = {"name": name, "rpc": {"type": "prompt", "message": msg}}
    s.update(kw); return s

scenario("shadow-flagged", "A destructive-looking bash call in shadow mode: one gate request, a warning and a status, the call runs, the clean output gets no notice.",
    [route(FLAG), route(OUTCLEAR)],
    [{"toolCalls": [bash("echo hi")]}, {"text": "done"}],
    [prompt("please run echo hi")])
scenario("clear", "An ordinary bash call: clear status, no notification, no notice on the output.",
    [route(CLEAR), route(OUTCLEAR)],
    [{"toolCalls": [bash("echo hi")]}, {"text": "done"}],
    [prompt("please run echo hi")])
scenario("enforce-decline", "Enforce mode, the user declines the confirmation: the call is blocked with the gate's reason.",
    [route(FLAG)],
    [{"toolCalls": [bash("echo hi")]}, {"text": "blocked"}],
    [prompt("please run echo hi", ui=[{"confirmed": False}])],
    extra_cfg={"gate": {"mode": "enforce"}})
scenario("enforce-accept", "Enforce mode, the user allows the call: it runs, the output is judged.",
    [route(FLAG), route(OUTCLEAR)],
    [{"toolCalls": [bash("echo hi")]}, {"text": "done"}],
    [prompt("please run echo hi", ui=[{"confirmed": True}])],
    extra_cfg={"gate": {"mode": "enforce"}})
scenario("output-leak", "The output judge flags a secret: a notification, a status, and a notice appended to the result the model reads.",
    [route(CLEAR), route(out(0.94, "no_failure", 0.9))],
    [{"toolCalls": [bash("echo API_KEY=abc123")]}, {"text": "done"}],
    [prompt("show me the key")])
scenario("output-advice", "The output judge classifies a failure: advice appended to the result, no notification.",
    [route(CLEAR), route(out(0.01, "transient", 1.0))],
    [{"toolCalls": [bash("echo timeout; exit 3")]}, {"text": "retry"}],
    [prompt("run the flaky command")])
scenario("output-low-confidence", "The failure class is below output.minConfidence: the result is left alone.",
    [route(CLEAR), route(out(0.01, "environment", 0.42))],
    [{"toolCalls": [bash("echo odd; exit 2")]}, {"text": "hm"}],
    [prompt("run it")])
scenario("write-elision", "A write call with a long body: the gate's request carries the first 400 characters and an elision marker, never the body.",
    [route(CLEAR)],
    [{"toolCalls": [{"name": "write", "arguments": {"path": "big.txt", "content": "x" * 900}}]}, {"text": "written"}],
    [prompt("write the file")])
scenario("cache-identical-calls", "The same call twice: judged once (gate and output).",
    [route(CLEAR), route(OUTCLEAR)],
    [{"toolCalls": [bash("echo same")]}, {"toolCalls": [bash("echo same")]}, {"text": "done"}],
    [prompt("run it twice")])
scenario("unjudged-tools", "read is not a judged tool: no request at all.",
    [route(CLEAR, 0)],
    [{"toolCalls": [{"name": "read", "arguments": {"path": "a.txt"}}]}, {"text": "read"}],
    [prompt("read a.txt")], files={"a.txt": "hello\n"})
scenario("jev-ask", "The model calls jev_ask with a noul, a choice and a score question.",
    [route({"model": "jev-eq", "usage": {"input_tokens": 12, "output_tokens": 3}, "answers": {
        "relevant": noul(0.93), "label": {"type": "choice", "choice": "bug", "confidence": 0.9, "probabilities": {"feature": 0.1, "bug": 0.9}},
        "quality": score(1.75, 0.8)}})],
    [{"toolCalls": [{"name": "jev_ask", "arguments": {"state": "the diff", "questions": [
        {"id": "relevant", "type": "noul", "instructions": "Is this relevant?"},
        {"id": "label", "type": "choice", "instructions": "Which bucket?", "options": [{"name": "bug", "description": "Defect"}, {"name": "feature"}]},
        {"id": "quality", "type": "score", "instructions": "How thorough?", "levels": ["Superficial", "Adequate", "Thorough"]}]}}]},
     {"text": "asked"}],
    [prompt("ask jev about the diff")])
scenario("commands", "/jev mode, last, output, check, off and on.",
    [route(FLAG), route(out(0.94, "transient", 0.8)), route(gate(0.99, 0.1, 0.1, 1.0, 0.9))],
    [{"toolCalls": [bash("echo hi")]}, {"text": "done"}],
    [prompt("please run echo hi"),
     {"name": "last", "rpc": {"type": "prompt", "message": "/jev last"}},
     {"name": "output", "rpc": {"type": "prompt", "message": "/jev output"}},
     {"name": "mode-enforce", "rpc": {"type": "prompt", "message": "/jev mode enforce"}},
     {"name": "mode-bogus", "rpc": {"type": "prompt", "message": "/jev mode bogus"}},
     {"name": "check", "rpc": {"type": "prompt", "message": "/jev check Rm -RF /tmp/Build"}},
     {"name": "off", "rpc": {"type": "prompt", "message": "/jev off"}},
     {"name": "on", "rpc": {"type": "prompt", "message": "/jev on"}}])
