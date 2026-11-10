#!/usr/bin/env python3
"""Kind verification of the PiG harness on kagent v1.0.0 with the mock model: the standard series, then crash-and-RevertSession
(a plain turn and a parked approval) through the raw A2A gateway (localhost:18083) and the revert client. Prints PASS/FAIL per step."""
import json, os, subprocess, sys, threading, time, urllib.request, uuid
GW = "http://localhost:18083/agents/kagent/"
HITL = "https://kagent.dev/extensions/hitl/v1"
REVERT = os.environ["REVERT_BIN"]
def rpc(agent, method, params, retries=0):
    body = json.dumps({"jsonrpc": "2.0", "id": uuid.uuid4().hex, "method": method, "params": params}).encode()
    req = urllib.request.Request(GW + agent, body, {"Content-Type": "application/json", "A2A-Version": "1.0", "x-user-id": "admin@kagent.dev",
                                 "A2A-Extensions": HITL, "Accept": "text/event-stream"})
    evs = []
    with urllib.request.urlopen(req, timeout=300) as resp:
        for raw in resp:
            line = raw.decode().strip()
            if line.startswith("data:"):
                evs.append(json.loads(line[5:].strip()))
            elif line.startswith("{"):
                evs.append(json.loads(line))
    return evs
def msg(text, ctx=None, task=None, hitl=None):
    m = {"messageId": uuid.uuid4().hex, "role": "ROLE_USER", "parts": [{"text": text}]}
    if ctx: m["contextId"] = ctx
    if task: m["taskId"] = task
    if hitl: m["extensions"] = [HITL]; m["metadata"] = {HITL: hitl}
    return m
def stream(agent, retry=False, **kw):
    for attempt in range(8 if retry else 1):
        evs = rpc(agent, "SendStreamingMessage", {"message": msg(**kw)})
        err = [e for e in evs if "error" in e]
        if err and retry and err[0]["error"].get("code") == -32004:
            time.sleep(3); continue
        break
    last, text = None, {}
    for e in evs:
        r = e.get("result", {})
        if "task" in r: last = r["task"]
        if "statusUpdate" in r and last is not None: last["status"] = r["statusUpdate"]["status"]
        if "artifactUpdate" in r:
            a = r["artifactUpdate"]["artifact"]
            for p in a.get("parts", []):
                if "text" in p: text[a["artifactId"]] = text.get(a["artifactId"], "") + p["text"]
    return last, "".join(text.values()), evs
def approve(agent, task, rej=None, retry=False):
    tools = task["status"]["message"]["metadata"][HITL]["tools"]
    ap = {"id": tools[0]["id"], "approved": rej is None}
    if rej: ap["rejection_reason"] = rej
    return stream(agent, retry=retry, text="Rejected" if rej else "Approved", ctx=task["contextId"], task=task["id"],
                  hitl={"type": "tool_approval_response", "approvals": [ap]})
def sh(cmd):
    return subprocess.run(cmd, shell=True, text=True, capture_output=True).stdout.strip()
def res(name, ok, info=""):
    print(("PASS " if ok else "FAIL ") + name, info, flush=True)
def state(ctx):
    return sh(f"{REVERT} -addr localhost:18083 -session {ctx} 2>&1")
def revert(ctx):
    return sh(f"{REVERT} -addr localhost:18083 -session {ctx} -revert 2>&1")
def wpod():
    return sh("kubectl -n kagent get pods -l ate.dev/worker-pool=kagent-default -o jsonpath={.items[0].metadata.name}")
def held(n):
    # the mock logs a request when it ends, so a held call shows only after the crash: wait for the Actor to restore and make the call instead
    time.sleep(18)
    return True
def held_logged(n):
    return sh("kubectl -n kagent logs deploy/kagent-mock --tail=2000").count('"held"') >= n
def controller_log():
    return sh("kubectl -n kagent logs deploy/kagent-controller --since=5m | grep -i -E 'revert|crash' | cut -c1-260 | tail -8")
def crash_worker():
    pod = wpod()
    sh(f"kubectl -n kagent delete pod {pod} --grace-period=0 --force")
    sh("kubectl -n kagent wait --for=condition=Ready pod -l ate.dev/worker-pool=kagent-default --timeout=180s")
    return pod

# ---- standard series
t, txt, _ = stream("pig-basket", text="[basket] go")
res("1 basket", t["status"]["state"] == "TASK_STATE_COMPLETED" and txt.startswith("BASKET-DONE"), txt)
t, _, _ = stream("pig-approve", text="[approve] go")
ok = t["status"]["state"] == "TASK_STATE_INPUT_REQUIRED"
t2, txt, _ = approve("pig-approve", t)
res("2a approve", ok and t2["status"]["state"] == "TASK_STATE_COMPLETED" and txt.startswith("APPROVE-DONE"), txt)
t, _, _ = stream("pig-approve", text="[approve] go")
t2, txt, evs = approve("pig-approve", t, "policy says no: nope-4242")
res("2b reject", t2["status"]["state"] == "TASK_STATE_COMPLETED" and "nope-4242" in json.dumps(evs), txt)
t, _, _ = stream("pig-basket", text="remember [m-4242] ok")
time.sleep(8)
t2, txt, _ = stream("pig-basket", text="[recall] go", ctx=t["contextId"])
res("3a recall across suspend", txt == "RECALL [m-4242]", txt)

# ---- A: a plain turn is running when the Actor's worker is killed; RevertSession; the session keeps the earlier turn
t, _, _ = stream("pig-basket", text="remember [m-5151] ok")
ctx = t["contextId"]; time.sleep(20)
out = {}
def turn(): 
    try: out["r"] = stream("pig-basket", text="[hold] go", ctx=ctx)
    except Exception as e: out["e"] = repr(e)
th = threading.Thread(target=turn, daemon=True); th.start()
held(1)
print("   before:", state(ctx))
pod = crash_worker(); print("   deleted worker", pod)
time.sleep(15)
print("   state after the crash:", state(ctx))
r1 = revert(ctx); print("   RevertSession:", r1)
r2 = revert(ctx); print("   RevertSession again (lost-response retry):", r2)
print("   controller:", controller_log()); res("A-hold the interrupted turn had a model call in flight", held_logged(1))
res("A2 RevertSession returns the Session READY", "RevertSession ok: RUNTIME_STATE_READY" in r1 and "RevertSession ok: RUNTIME_STATE_READY" in r2, r1)
t2, txt, _ = stream("pig-basket", retry=True, text="[recall] go", ctx=ctx)
res("A3 the reverted session still recalls its earlier turn", txt == "RECALL [m-5151]", txt)
th.join(30); print("   interrupted caller:", out.get("e") or (out.get("r")[0] or {}).get("status", {}).get("state"))

# ---- B: a parked approval is resumed, the Actor crashes during the continuation; RevertSession; the approval is asked for again
tp, _, _ = stream("pig-approve", text="[approve] [holdafter] go")
res("B1 parked", tp["status"]["state"] == "TASK_STATE_INPUT_REQUIRED")
ctx = tp["contextId"]; time.sleep(20)
out2 = {}
def resume():
    try: out2["r"] = approve("pig-approve", tp)
    except Exception as e: out2["e"] = repr(e)
th = threading.Thread(target=resume, daemon=True); th.start()
held(2)
pod = crash_worker(); print("   deleted worker", pod)
time.sleep(15)
print("   state after the crash:", state(ctx))
r1 = revert(ctx); print("   RevertSession:", r1)
print("   controller:", controller_log()); res("B-hold the continuation had a model call in flight", held_logged(2))
res("B3 RevertSession returns the Session READY", "RevertSession ok: RUNTIME_STATE_READY" in r1, r1)
th.join(30); print("   interrupted caller:", out2.get("e") or (out2.get("r")[0] or {}).get("status", {}).get("state"))
cur = None
for _ in range(1):
    cur = rpc("pig-approve", "GetTask", {"id": tp["id"]}) if False else None
try:
    t3, txt, evs = approve("pig-approve", tp, retry=True)
    print("   second approval:", t3 and t3["status"]["state"], txt[:80], json.dumps([e for e in evs if "error" in e])[:300])
    res("B4 the approval parked in the snapshot runs once more and the task completes", t3 is not None and t3["status"]["state"] == "TASK_STATE_COMPLETED" and txt.startswith("APPROVE-DONE"), txt)
except Exception as e:
    res("B4 second approval", False, repr(e))
t5, txt5, evs5 = stream("pig-approve", retry=True, text="[recall] go", ctx=ctx)
print("   new task on the reverted approval session:", t5 and t5["status"]["state"], txt5[:60], json.dumps([e for e in evs5 if "error" in e])[:300])
res("B5 a new message on the reverted session is answered", t5 is not None and t5["status"]["state"] == "TASK_STATE_COMPLETED", txt5)
print("DONE")
