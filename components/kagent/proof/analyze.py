#!/usr/bin/env python3
"""Join basket runs, node-side memory samples and Substrate logs into per-harness baseline tables.

  analyze.py --dir results/baseline-YYYYMMDD [--images images.json] [--coldstart coldstart.json] --out-json X --out-md Y

Directory layout (written by baseline.sh):
  runs/<w|r><round>-<agent>.json   basket output of one invocation (w = warmup round, excluded)
  mem.jsonl                         collect.py run (cgroup samples)
  logs.json                         collect.py logs (Actor state changed, atelet timing records)

Per session the actor is named session-<contextId> (kagent), its UID comes from the `Actor state changed` records, and its
memory is every cgroup leaf named <uid>-* below the worker pod (the sentry lives in <uid>-_sandbox, and in <uid>-_ckpt while
a checkpoint runs, so the sum follows it).
"""
import argparse, glob, gzip, io, json, os, re, sys

MIB = 1 << 20


def q(vals, p):
    s = sorted(vals)
    if not s:
        return None
    pos = p * (len(s) - 1)
    lo = int(pos)
    return s[-1] if lo + 1 >= len(s) else s[lo] + (pos - lo) * (s[lo + 1] - s[lo])


def stat(vals, nd=1):
    v = [x for x in vals if x is not None]
    if not v:
        return None
    r = lambda x: round(x, nd)
    return {"n": len(v), "median": r(q(v, .5)), "p90": r(q(v, .9)), "p95": r(q(v, .95)), "min": r(min(v)), "max": r(max(v))}


def load_runs(d):
    runs = []
    for path in sorted(glob.glob(os.path.join(d, "runs", "*.json"))):
        m = re.match(r"^([wr])(\d+)-(.+)\.json$", os.path.basename(path))
        if not m:
            continue
        doc = json.load(open(path))
        for r in doc["runs"]:
            r["_agent"] = doc["meta"]["agent"].split("/")[-1]
            r["_warm"] = m.group(1) == "w"
            r["_round"] = int(m.group(2))
            turns = r.get("turns") or []
            r["_ctx"] = turns[0].get("context_id") if turns else None
            runs.append(r)
    return runs


def _open_text(path, mode="rt"):
    return gzip.open(path, mode) if path.endswith(".gz") else open(path, mode)


def load_pressure(d):
    """baseline.sh's pressure.tsv: (tag, agent) -> {psi, waited_s, over}. Empty when the file is absent (older runs)."""
    out = {}
    path = os.path.join(d, "pressure.tsv")
    if os.path.exists(path):
        for line in open(path):
            f = line.rstrip("\n").split("\t")
            if len(f) >= 5:
                out[(f[0], f[1])] = {"psi": float(f[2]), "waited_s": int(f[3]), "over": f[4] == "1"}
    return out


def over_gate_sessions(rows):
    """Measured sessions that were started although host CPU pressure stayed above the gate after GATE_WAIT."""
    return [{"agent": r["agent"], "round": r["round"], **r["pressure"]} for r in rows if not r["warm"] and (r.get("pressure") or {}).get("over")]


def load_mem(path):
    """Read a collector sample file (plain or gzip). Returns (meta, samples)."""
    meta, samples = None, []
    with _open_text(path) as f:
        for line in f:
            d = json.loads(line)
            if "meta" in d:
                meta = d["meta"]
            else:
                samples.append(d)
    return meta, samples


def _gz_bytes(meta, samples):
    lines = [json.dumps({"meta": meta}, separators=(",", ":"))] + [json.dumps(s, separators=(",", ":")) for s in samples]
    buf = io.BytesIO()
    with gzip.GzipFile(fileobj=buf, mode="wb", mtime=0, compresslevel=9) as g:
        g.write(("\n".join(lines) + "\n").encode())
    return buf.getvalue()


def ship_raw(meta, samples, rows, out, budget):
    """Write the collector's raw samples gzip-compressed to `out` and return a description of what was shipped.

    The full file when it fits `budget` bytes. Otherwise only the samples inside the first activation of every measured run
    (warmup runs dropped); if that still does not fit, also only the series of that actor's leaves. The meta record says which
    kind was shipped, so analyze.py can be re-run on the bundle either way (the per-activation CPU and memory of the shipped
    activations are identical to the full run's)."""
    meta = dict(meta or {})
    kinds = [("full", samples)]
    wins = [(r["activations"][0]["t0"], r["activations"][0]["t1"], r["uid"]) for r in rows if not r["warm"] and r.get("activations")]
    sub = []
    for s in samples:
        for t0, t1, uid in wins:
            if t0 <= s["t"] <= t1:
                sub.append(s)
                break
    kinds.append(("first-activations", sub))
    slim = []
    for s in sub:
        uids = [u for t0, t1, u in wins if t0 <= s["t"] <= t1]
        keep = {k: v for k, v in s["series"].items() if any(k.split("|", 2)[2].startswith("sub:" + u + "-") for u in uids)}
        slim.append({**s, "series": keep})
    kinds.append(("first-activations-actor-leaves-only", slim))
    for kind, smp in kinds:
        m = {**meta, "shipped": kind, "shipped_samples": len(smp), "total_samples": len(samples)}
        data = _gz_bytes(m, smp)
        if len(data) <= budget or kind == kinds[-1][0]:
            with open(out, "wb") as f:
                f.write(data)
            return {"kind": kind, "bytes": len(data), "samples": len(smp), "total_samples": len(samples), "within_budget": len(data) <= budget}


def actor_series(samples, uid):
    """[(t, ws_total, ws_sandbox, {leaf scope: cpu_usec}, peak_sandbox)] over the samples where the actor has any leaf.

    The working set is the sum over the leaves present in that sample. cpu_usec is cumulative per leaf and a leaf can be
    gone from the later samples of an activation, so the per-leaf values are kept and combined by activation_cpu_usec."""
    out = []
    for s in samples:
        ws = 0
        cpu = {}
        sb = pk = None
        for key, v in s["series"].items():
            target, pod, scope = key.split("|", 2)
            if not scope.startswith("sub:" + uid + "-"):
                continue
            ws += v.get("working_set", 0)
            cpu[key] = v.get("cpu_usec", 0)
            if scope.endswith("-_sandbox"):
                sb = v.get("working_set", 0)
                pk = v.get("peak")
        if cpu:
            out.append((s["t"], ws, sb if sb is not None else 0, cpu, pk or 0))
    return out


def activation_cpu_usec(act):
    """CPU of one activation: the sum over its leaves of each leaf's maximum cpu_usec seen in the activation.

    Not the leaves present at the last sample: a leaf (for example the sandbox once the checkpoint leaf takes over) can be
    gone by then and would take its CPU with it."""
    best = {}
    for row in act:
        for k, v in row[3].items():
            if v > best.get(k, 0):
                best[k] = v
    return sum(best.values())


def activations(series, gap=1.5):
    """Split an actor's samples into activations (a gap longer than `gap` seconds means it was suspended in between)."""
    acts, cur = [], []
    for row in series:
        if cur and row[0] - cur[-1][0] > gap:
            acts.append(cur)
            cur = []
        cur.append(row)
    if cur:
        acts.append(cur)
    return acts


def server_timeline(states, name):
    ev = sorted([s for s in states if s["name"] == name and s["t"]], key=lambda s: s["t"])
    resumes, suspends, delay = [], [], None
    t_res = t_sus = None
    for e in ev:
        if e["state"] == "resuming":
            t_res = e["t"]
        elif e["state"] == "running" and t_res is not None:
            resumes.append(e["t"] - t_res)
            t_res = None
        elif e["state"] == "suspending":
            t_sus = e["t"]
        elif e["state"] == "suspended" and t_sus is not None:
            suspends.append(e["t"] - t_sus)
            t_sus = None
    return ev, resumes, suspends


def summarize_activation(a):
    """One activation (list of actor_series rows) as the record stored per run.

    Memory: the leaf working sets are summed per sample, then the maximum over the samples is taken. CPU: activation_cpu_usec."""
    ws = [x[1] for x in a]
    return {"t0": a[0][0], "t1": a[-1][0], "n": len(a), "ws_start_mib": round(ws[0] / MIB, 1),
            "ws_median_mib": round(sorted(ws)[len(ws) // 2] / MIB, 1), "ws_max_mib": round(max(ws) / MIB, 1),
            "sandbox_ws_max_mib": round(max(x[2] for x in a) / MIB, 1),
            "peak_mib": round(max(x[4] for x in a) / MIB, 1),
            "cpu_ms": round(activation_cpu_usec(a) / 1000, 1)}


def per_run(runs, states, timing_raw, samples, pressure=None):
    uid_of = {}
    for s in states:
        uid_of[s["name"]] = s["uid"]
    rows = []
    for r in runs:
        row = {"agent": r["_agent"], "warm": r["_warm"], "round": r["_round"], "ok": r["OK"] if "OK" in r else r.get("ok"),
               "error": r.get("error"), "checks": r.get("checks", {}), "approval": r.get("approval"), "timings": r.get("timings_ms", {})}
        row["pressure"] = (pressure or {}).get((("w" if r["_warm"] else "r") + str(r["_round"]), r["_agent"]))
        tools = [t["name"] for t in (r.get("turns") or [{}])[0].get("tools", []) or []]
        for t in r.get("turns", []):
            tools += [x["name"] for x in t.get("tools", []) or []]
        row["tool_names"] = sorted(set(tools))
        row["mcp_called"] = any(n.endswith("kagent_probe") for n in tools)
        name = "session-" + r["_ctx"] if r["_ctx"] else None
        row["uid"] = uid_of.get(name)
        if name:
            ev, resumes, suspends = server_timeline(states, name)
            row["srv_resume_s"] = [round(x, 3) for x in resumes]
            row["srv_suspend_s"] = [round(x, 3) for x in suspends]
            # delay between the end of the last turn1 stream and the suspend that follows it
            ends = [p["end"] for p in r.get("phases", []) if p["name"].startswith("turn1")]
            if ends:
                te = max(ends)
                nxt = [e["t"] for e in ev if e["state"] == "suspending" and e["t"] >= te - 0.5]
                row["srv_suspend_delay_s"] = round(nxt[0] - te, 3) if nxt else None
        if row["uid"]:
            rs = sorted([t for t in timing_raw if t["uid"] == row["uid"] and t["op"] == "restore"], key=lambda t: t["t"])
            cs = sorted([t for t in timing_raw if t["uid"] == row["uid"] and t["op"] == "checkpoint"], key=lambda t: t["t"])
            row["restore"] = [{"kind": t["kind"], "total_s": t["phases"].get("ate.restore.total"), "phases": t["phases"]} for t in rs]
            row["checkpoint_total_s"] = [t["phases"].get("ate.checkpoint.total") for t in cs]
            acts = activations(actor_series(samples, row["uid"]))
            row["activations"] = []
            for a in acts:
                row["activations"].append(summarize_activation(a))
        rows.append(row)
    return rows


def first_nth(rows, key, i):
    out = []
    for r in rows:
        v = r.get(key) or []
        if len(v) > i:
            out.append(v[i])
    return out


def aggregate(rows, agents):
    res = {}
    for ag in agents:
        rs = [r for r in rows if r["agent"] == ag and not r["warm"]]
        if not rs:
            continue
        a = {"runs": len(rs), "ok": sum(1 for r in rs if r["ok"]), "failed": sum(1 for r in rs if not r["ok"]),
             "errors": sorted({(r["error"] or "")[:120] for r in rs if r["error"]})}
        a["mcp_called"] = sum(1 for r in rs if r["mcp_called"])
        a["approved"] = sum(1 for r in rs if r.get("approval") == "approved")
        a["recall_ok"] = sum(1 for r in rs if r["checks"].get("turn2_recall"))
        a["tools"] = sorted({n for r in rs for n in r["tool_names"]})
        tm = {}
        for r in rs:
            for k, v in r["timings"].items():
                tm.setdefault(k, []).append(v)
        a["client_ms"] = {k: stat(v, 0) for k, v in sorted(tm.items())}
        a["srv"] = {
            "resume_first_s": stat(first_nth(rs, "srv_resume_s", 0), 3),
            "resume_second_s": stat(first_nth(rs, "srv_resume_s", 1), 3),
            "suspend_first_s": stat(first_nth(rs, "srv_suspend_s", 0), 3),
            "suspend_delay_s": stat([r.get("srv_suspend_delay_s") for r in rs], 3),
            "restore_first_total_s": stat([ (r.get("restore") or [{}])[0].get("total_s") for r in rs], 3),
            "restore_second_total_s": stat([ (r.get("restore") or [{}, {}])[1].get("total_s") if len(r.get("restore") or []) > 1 else None for r in rs], 3),
            "restore_first_kind": sorted({(r.get("restore") or [{}])[0].get("kind") or "" for r in rs}),
            "restore_second_kind": sorted({r["restore"][1].get("kind") or "" for r in rs if len(r.get("restore") or []) > 1}),
            "checkpoint_first_total_s": stat([ (r.get("checkpoint_total_s") or [None])[0] for r in rs], 3),
        }
        ph = {}
        for r in rs:
            for i, rest in enumerate(r.get("restore") or []):
                for k, v in rest["phases"].items():
                    if k.startswith("ate.restore.") and not k.endswith(".total"):
                        ph.setdefault(("first" if i == 0 else "later") + ":" + k[len("ate.restore."):], []).append(v)
        a["srv"]["restore_phases_s"] = {k: stat(v, 3) for k, v in sorted(ph.items())}
        mem = {}
        for idx, label in ((0, "act1"), (1, "act2")):
            acts = [r["activations"][idx] for r in rs if len(r.get("activations") or []) > idx]
            if acts:
                for f in ("ws_start_mib", "ws_median_mib", "ws_max_mib", "sandbox_ws_max_mib", "peak_mib", "cpu_ms", "n"):
                    mem.setdefault(label, {})[f] = stat([x[f] for x in acts], 1)
        a["memory"] = mem
        a["activations_per_session"] = stat([len(r.get("activations") or []) for r in rs], 0)
        res[ag] = a
    return res


def worker_baseline(samples):
    """Working set of the shared worker pod, split into samples with no actor cgroup (idle) and with at least one."""
    idle, busy = [], []
    for s in samples:
        ws, has = None, False
        for key, v in s["series"].items():
            target, pod, scope = key.split("|", 2)
            if target != "worker":
                continue
            if scope == "pod":
                ws = v.get("working_set")
            elif scope.startswith("sub:") and re.match(r"^sub:[0-9a-f]{8}-", scope):
                has = True
        if ws is not None:
            (busy if has else idle).append(ws / MIB)
    return {"idle": stat(idle, 1), "with_actor": stat(busy, 1)}


def fmt(s, f="median"):
    return "" if not s else str(s[f])


def to_md(res, agents, meta, images, cold, extra_notes):
    L = []
    L.append("## Capability and correctness (n = measured sessions per agent)\n")
    L.append("| agent | sessions | ok | `[basket]`/`[approve]` done | MCP probe called | approval round trip | recall after suspend | tool names seen |")
    L.append("|---|---:|---:|---:|---:|---:|---:|---|")
    for ag in agents:
        a = res.get(ag)
        if not a:
            continue
        L.append(f"| {ag} | {a['runs']} | {a['ok']} | {a['ok']} | {a['mcp_called']} | {a['approved']} | {a['recall_ok']} | {', '.join(a['tools'])} |")
    L.append("\n## Client latency through the A2A gateway (ms, median / p90 / min / max)\n")
    keys = ["turn1_first_event", "turn1_first_text", "turn1_end_to_end", "turn1_input_required", "approval_to_done", "turn2_first_event", "turn2_first_text", "turn2_total"]
    L.append("| agent | n | " + " | ".join(keys) + " |")
    L.append("|---|---:|" + "---:|" * len(keys))
    for ag in agents:
        a = res.get(ag)
        if not a:
            continue
        cells = []
        for k in keys:
            s = a["client_ms"].get(k)
            cells.append(f"{s['median']:.0f} / {s['p90']:.0f} / {s['min']:.0f} / {s['max']:.0f}" if s else "n/a")
        L.append(f"| {ag} | {a['runs']} | " + " | ".join(cells) + " |")
    L.append("\n## Server side lifecycle (s, median / p90), from Substrate records of the same sessions\n")
    L.append("| agent | first resume (create to running) | restore total, first | restore kind | 2nd resume | restore total, 2nd | restore kind | checkpoint total | suspend start vs turn end (negative = before the client saw the end) | suspend duration |")
    L.append("|---|---:|---:|---|---:|---:|---|---:|---:|---:|")
    for ag in agents:
        a = res.get(ag)
        if not a:
            continue
        s = a["srv"]
        def mp(x):
            return f"{x['median']} / {x['p90']}" if x else "n/a"
        L.append(f"| {ag} | {mp(s['resume_first_s'])} | {mp(s['restore_first_total_s'])} | {','.join(s['restore_first_kind'])} | {mp(s['resume_second_s'])} | "
                 f"{mp(s['restore_second_total_s'])} | {','.join(s['restore_second_kind'])} | {mp(s['checkpoint_first_total_s'])} | {mp(s['suspend_delay_s'])} | {mp(s['suspend_first_s'])} |")
    L.append("\n## Memory per actor activation (MiB, median / p90 / max over sessions)\n")
    L.append("Sum of the actor's cgroup leaves `<actorUID>-*` below the worker pod, working set = memory.current - inactive_file, sampled every "
             f"{meta.get('interval', '?')} s; `peak` is the leaf's memory.peak high-water mark read before the leaf disappeared.\n")
    L.append("| agent | activation | samples | ws first sample | ws median | ws max | sandbox ws max | memory.peak | cpu ms |")
    L.append("|---|---|---:|---:|---:|---:|---:|---:|---:|")
    for ag in agents:
        a = res.get(ag)
        if not a:
            continue
        for label in ("act1", "act2"):
            m = a["memory"].get(label)
            if not m:
                continue
            def t3(f):
                s = m[f]
                return f"{s['median']} / {s['p90']} / {s['max']}" if s else ""
            L.append(f"| {ag} | {label} | {m['n']['median']:.0f} | {t3('ws_start_mib')} | {t3('ws_median_mib')} | {t3('ws_max_mib')} | {t3('sandbox_ws_max_mib')} | {t3('peak_mib')} | {t3('cpu_ms')} |")
    if images:
        L.append("\n## Images\n")
        L.append("| harness | image | compressed size (MiB, amd64 layers) | layers | registry pull, crane (s) |")
        L.append("|---|---|---:|---:|---:|")
        for im in images:
            L.append(f"| {im['name']} | `{im['ref']}` | {im.get('compressed_mib', '')} | {im.get('layers', '')} | {im.get('pull_s', '')} |")
    if cold:
        L.append("\n## Cold start: Agent apply to Ready (s)\n")
        L.append("| harness | n | median | min | max | values |")
        L.append("|---|---:|---:|---:|---:|---|")
        for k, v in cold.items():
            vals = [x for x in v if x is not None]
            if vals:
                L.append(f"| {k} | {len(vals)} | {q(vals, .5):.1f} | {min(vals):.1f} | {max(vals):.1f} | {', '.join(f'{x:.1f}' for x in vals)} |")
    if extra_notes:
        L.append("\n" + extra_notes)
    return "\n".join(L)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--dir", required=True)
    ap.add_argument("--agents", default="")
    ap.add_argument("--images")
    ap.add_argument("--coldstart")
    ap.add_argument("--notes")
    ap.add_argument("--out-json", required=True)
    ap.add_argument("--out-md", required=True)
    ap.add_argument("--mem", help="collector sample file, plain or .gz (default: <dir>/mem.jsonl)")
    ap.add_argument("--raw-out", help="also write the raw 0.25 s samples gzip-compressed here (the data bundle copy, see ship_raw)")
    ap.add_argument("--raw-budget", type=int, default=2_800_000, help="byte budget of --raw-out; a first-activation subset is shipped when the full file exceeds it")
    a = ap.parse_args()
    runs = load_runs(a.dir)
    meta, samples = load_mem(a.mem or os.path.join(a.dir, "mem.jsonl"))
    logs = json.load(open(os.path.join(a.dir, "logs.json")))
    agents = [x for x in a.agents.split(",") if x] or sorted({r["_agent"] for r in runs})
    states = list({(s["name"], s["state"], s["t"]): s for s in logs["states"]}.values())
    timing_raw = list({(t["uid"], t["op"], t["t"]): t for t in logs["timing_raw"]}.values())
    pressure = load_pressure(a.dir)
    rows = per_run(runs, states, timing_raw, samples, pressure)
    over = over_gate_sessions(rows)
    res = aggregate(rows, agents)
    images = json.load(open(a.images)) if a.images else None
    cold = json.load(open(a.coldstart)) if a.coldstart else None
    notes = open(a.notes).read() if a.notes else ""
    wb = worker_baseline(samples)
    if a.raw_out:
        shipped = ship_raw(meta, samples, rows, a.raw_out, a.raw_budget)
        print("analyze: raw samples shipped: " + json.dumps(shipped), file=sys.stderr)
        meta = {**(meta or {}), "raw_shipped": shipped}
    notes += (f"\n## Worker pod (shared by all actors)\n\nWorking set of the `kagent-default` worker pod: idle (no actor cgroup) median {wb['idle']['median']} MiB "
              f"(n={wb['idle']['n']} samples), with an actor running median {wb['with_actor']['median']} MiB (n={wb['with_actor']['n']}). "
              "Actor memory in the tables above is the actor's own sandbox cgroup, not this pod number.\n" if wb["idle"] and wb["with_actor"] else "")
    if pressure:
        notes += (f"\n## Host pressure gate\n\n{len(pressure)} sessions were gated on host CPU pressure; {len(over)} measured session(s) started although pressure stayed above the gate "
                  "after the wait ran out" + (":\n\n" + "\n".join(f"- {o['agent']} round {o['round']}: PSI {o['psi']}, waited {o['waited_s']} s" for o in over) if over else " (none).") + "\n")
    json.dump({"meta": meta, "pressure_over_gate": over, "aggregate": res, "runs": rows, "images": images, "coldstart": cold, "worker": wb}, open(a.out_json, "w"), indent=1)
    open(a.out_md, "w").write(to_md(res, agents, meta or {}, images, cold, notes) + "\n")
    print(f"analyze: {len(rows)} runs, {len(res)} agents", file=sys.stderr)


if __name__ == "__main__":
    main()
