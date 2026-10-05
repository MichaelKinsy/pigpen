import json, os, subprocess, sys, time, threading, queue, tempfile
BIN = sys.argv[1]; LLM = sys.argv[2]   # pig binary, pigeq
root = tempfile.mkdtemp(prefix="jev-e2e-")
for d in ("home", "pighome", "agent", "work"): os.makedirs(f"{root}/{d}")
gate = {"answers": {"destructive": 0.99, "exfiltration": 0.1, "beyond_scope": 0.2, "impact": {"0": 0, "1": 0, "2": 0.1, "3": 0.9}}}
out = {"answers": {"leaks_secret": 0.96, "failure_class": {"transient": 0, "environment": 0, "code_bug": 0, "permission": 0, "user_error": 0, "no_failure": 1}}}
ask = {"answers": {"relevant": 0.9}}
turns = [{"toolCalls": [{"name": "jev_ask", "arguments": {"state": "the diff", "questions": [{"id": "relevant", "type": "noul", "instructions": "Is this relevant?"}]}}]}, {"text": json.dumps(ask)}, {"text": "done"}]
json.dump(turns, open(f"{root}/turns.json", "w"))
llm = subprocess.Popen([LLM, "llm", "--script", f"{root}/turns.json", "--log", f"{root}/llm.log"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
base = llm.stdout.readline().strip().split()[-1]
json.dump({"providers": {"eq-llm": {"baseUrl": base, "api": "openai-completions", "apiKey": "eq-key", "models": [{"id": "eq-1", "name": "eq-1", "reasoning": False, "input": ["text"], "contextWindow": 100000, "maxTokens": 4096, "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}]}}}, open(f"{root}/agent/models.json", "w"))
env = {"PATH": os.environ["PATH"], "HOME": f"{root}/home", "PIG_HOME": f"{root}/pighome", "PIG_CODING_AGENT_DIR": f"{root}/agent", "PI_CODING_AGENT_DIR": f"{root}/agent", "TERM": "dumb", "LANG": "C.UTF-8", "PI_OFFLINE": "1", "PI_SKIP_VERSION_CHECK": "1"}
p = subprocess.Popen([BIN, "--mode", "rpc", "--no-session", "--offline", "--provider", "eq-llm", "--model", "eq-1"], cwd=f"{root}/work", env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
q = queue.Queue()
threading.Thread(target=lambda: [q.put(json.loads(l)) for l in p.stdout if l.startswith("{")], daemon=True).start()
def send(o): p.stdin.write(json.dumps(o) + "\n"); p.stdin.flush()
ui = []
def pump(until, timeout=90):
    end = time.time() + timeout
    while time.time() < end:
        try: ev = q.get(timeout=1)
        except queue.Empty: continue
        if ev.get("type") == "extension_ui_request":
            ui.append({k: ev.get(k) for k in ("method", "title", "message", "statusKey", "statusText", "notifyType")})
            if ev["method"] == "confirm": send({"type": "extension_ui_response", "id": ev["id"], "confirmed": True})
        if until(ev): return ev
    raise SystemExit("timeout waiting; ui=%s" % json.dumps(ui, indent=1))
send({"type": "prompt", "message": "/jev on", "id": "1"}); pump(lambda e: e.get("type") == "response" and e.get("id") == "1"); time.sleep(0.5)
send({"type": "prompt", "message": "ask jev if the diff is relevant", "id": "2"}); pump(lambda e: e.get("type") == "agent_end")
time.sleep(0.5)
send({"type": "prompt", "message": "/jev", "id": "3"}); pump(lambda e: e.get("type") == "response" and e.get("id") == "3"); time.sleep(0.5)
for u in ui: print(json.dumps(u, ensure_ascii=False))
print("---- llm requests:", sum(1 for _ in open(f"{root}/llm.log")))
p.stdin.close(); llm.stdin.close(); p.wait(timeout=20)
print("root", root)
