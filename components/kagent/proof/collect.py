#!/usr/bin/env python3
"""Per-pod / per-actor memory and CPU sampler for the kagent harness perf basket.

Reads cgroup v2 files on the node that hosts the pods (needs no root for
cgroup files; --pss needs root for /proc/<pid>/smaps_rollup of other users).
Python 3 standard library only.

  collect.py run    --out mem.jsonl --target NAME:ns=NS,sel=LABELSEL ... [--interval 1]
                    [--cgroup NAME:/sys/fs/cgroup/glob ...] [--pss] [--duration S]
  collect.py logs   --out logs.json --source ate-system:<atelet-selector> --source <actor-ns>:<worker-selector> [--since-time RFC3339]
  collect.py report --mem mem.jsonl [--basket basket.json] [--format md|json]

Why cgroup files and not /proc or `kubectl top`: see the memory section of RESULTS.md.
Series names are  <target>|<pod>|pod  (pod cgroup) and  <target>|<pod>|leaf:<name>
(one per child cgroup: a container under runc, a gVisor sandbox under Substrate).
"""
import argparse, glob, json, os, re, signal, statistics, subprocess, sys, time

CG = "/sys/fs/cgroup"
STOP = False


def _stop(*_):
    global STOP
    STOP = True


def read_text(path):
    try:
        with open(path) as f:
            return f.read()
    except OSError:
        return None


def read_int(path):
    t = read_text(path)
    if t is None:
        return None
    t = t.strip()
    return None if t == "max" else int(t)


def read_kv(path):
    out = {}
    t = read_text(path)
    if t:
        for line in t.splitlines():
            k, _, v = line.partition(" ")
            try:
                out[k] = int(v)
            except ValueError:
                pass
    return out


def read_psi(path):
    """memory.pressure: 'some avg10=0.00 ... total=N' -> {'some_avg10': x, 'some_total_us': n, 'full_...'}"""
    out = {}
    t = read_text(path)
    for line in (t or "").splitlines():
        kind, _, rest = line.partition(" ")
        kv = dict(p.split("=") for p in rest.split())
        out[kind + "_avg10"] = float(kv.get("avg10", 0))
        out[kind + "_total_us"] = int(kv.get("total", 0))
    return out


def sample_cgroup(path, with_psi):
    """One reading of a cgroup directory. working_set follows cAdvisor, kubelet and Substrate's
    cgroupstats: memory.current - inactive_file, floored at 0."""
    cur = read_int(os.path.join(path, "memory.current"))
    if cur is None:
        return None
    st = read_kv(os.path.join(path, "memory.stat"))
    cpu = read_kv(os.path.join(path, "cpu.stat"))
    inactive = st.get("inactive_file")
    ws = cur if inactive is None else max(cur - inactive, 0)
    s = {
        "current": cur,
        "working_set": ws,
        "anon": st.get("anon"),
        "file": st.get("file"),
        "shmem": st.get("shmem"),
        "kernel": st.get("kernel"),
        "inactive_file": inactive,
        "peak": read_int(os.path.join(path, "memory.peak")),
        "cpu_usec": cpu.get("usage_usec"),
        "throttled_usec": cpu.get("throttled_usec"),
        "nr_throttled": cpu.get("nr_throttled"),
        "pids": read_int(os.path.join(path, "pids.current")),
    }
    if with_psi:
        psi = read_psi(os.path.join(path, "memory.pressure"))
        s["psi_some_avg10"] = psi.get("some_avg10")
    return {k: v for k, v in s.items() if v is not None}


def cgroup_pids(path, limit=20000):
    pids = []
    for root, _dirs, files in os.walk(path):
        if "cgroup.procs" in files:
            t = read_text(os.path.join(root, "cgroup.procs")) or ""
            pids += [int(x) for x in t.split()]
        if len(pids) > limit:
            break
    return pids


def pss_of(pids):
    """Sum Pss and Rss (bytes) from /proc/<pid>/smaps_rollup. Unreadable pids are counted in `denied`."""
    pss = rss = denied = 0
    for pid in pids:
        t = read_text(f"/proc/{pid}/smaps_rollup")
        if t is None:
            denied += 1
            continue
        for line in t.splitlines():
            if line.startswith("Pss:"):
                pss += int(line.split()[1]) * 1024
            elif line.startswith("Rss:"):
                rss += int(line.split()[1]) * 1024
    return {"pss": pss, "rss": rss, "nprocs": len(pids), "pss_denied": denied}


def kubectl_pods(ns, sel):
    cmd = ["kubectl", "get", "pods", "-o", "json"]
    cmd += ["-n", ns] if ns else ["-A"]
    if sel:
        cmd += ["-l", sel]
    out = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
    if out.returncode != 0:
        raise RuntimeError(out.stderr.strip())
    pods = []
    for it in json.loads(out.stdout)["items"]:
        if it["status"].get("phase") not in ("Running", "Pending"):
            continue
        cids = {}
        for cs in it["status"].get("containerStatuses", []) + it["status"].get("initContainerStatuses", []):
            cid = cs.get("containerID", "")
            if "://" in cid:
                cids[cid.split("://", 1)[1]] = cs["name"]
        pods.append({"name": it["metadata"]["namespace"] + "/" + it["metadata"]["name"],
                     "uid": it["metadata"]["uid"], "containers": cids})
    return pods


def find_pod_cgroup(root, uid):
    u = uid.replace("-", "_")
    pats = [f"{root}/kubepods.slice/kubepods-pod{u}.slice",
            f"{root}/kubepods.slice/*/kubepods-*-pod{u}.slice",
            f"{root}/kubepods/pod{uid}", f"{root}/kubepods/*/pod{uid}",
            f"{root}/**/*pod{u}*.slice", f"{root}/**/pod{uid}"]
    for p in pats:
        hits = glob.glob(p, recursive=True)
        if hits:
            return hits[0]
    return None


def leaf_name(dirname, containers):
    base = dirname
    for pre in ("cri-containerd-", "crio-", "docker-"):
        if base.startswith(pre):
            base = base[len(pre):]
    base = re.sub(r"\.scope$", "", base)
    if re.match(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}-", base):
        return base  # Substrate actor leaf: <actorUID>-_sandbox, <actorUID>-<container>, <actorUID>-_ckpt
    return containers.get(base, base[:16])


def parse_target(spec):
    """NAME:ns=NS,sel=SELECTOR  (selector may contain commas only if quoted by repeating --target)."""
    name, _, rest = spec.partition(":")
    kv = {}
    for part in re.split(r",(?=\w+=)", rest):
        k, _, v = part.partition("=")
        kv[k] = v
    return {"name": name, "ns": kv.get("ns", ""), "sel": kv.get("sel", "")}


class Sampler:
    def __init__(self, a):
        self.a = a
        self.targets = [parse_target(t) for t in a.target]
        self.extra = [(c.partition(":")[0], c.partition(":")[2]) for c in a.cgroup]
        self.pods = {}          # target name -> list of pods (refreshed)
        self.paths = {}         # uid -> cgroup path
        self.last_refresh = 0
        self.fake = json.load(open(a.fake_pods)) if a.fake_pods else None

    def refresh(self):
        for t in self.targets:
            try:
                self.pods[t["name"]] = self.fake.get(t["name"], []) if self.fake else kubectl_pods(t["ns"], t["sel"])
            except Exception as e:  # keep last known pods; record the problem once
                print(f"collect: pod listing failed for {t['name']}: {e}", file=sys.stderr)
        self.last_refresh = time.monotonic()

    def cgroup_for(self, pod):
        p = self.paths.get(pod["uid"])
        if p and os.path.isdir(p):
            return p
        p = find_pod_cgroup(self.a.cgroup_root, pod["uid"])
        if p:
            self.paths[pod["uid"]] = p
        return p

    def sample(self, n):
        series = {}
        take_pss = self.a.pss and n % self.a.pss_every == 0
        for t in self.targets:
            for pod in self.pods.get(t["name"], []):
                path = self.cgroup_for(pod)
                if not path:
                    continue
                s = sample_cgroup(path, True)
                if s is None:
                    continue
                if take_pss:
                    s.update(pss_of(cgroup_pids(path)))
                series[f"{t['name']}|{pod['name']}|pod"] = s
                try:
                    kids = sorted(os.scandir(path), key=lambda e: e.name)
                except OSError:
                    kids = []
                for e in kids:
                    if e.is_dir():
                        ls = sample_cgroup(e.path, False)
                        if ls is not None:
                            if take_pss:
                                ls.update(pss_of(cgroup_pids(e.path)))
                            series[f"{t['name']}|{pod['name']}|leaf:{leaf_name(e.name, pod['containers'])}"] = ls
                        # Substrate: the worker container scope holds one child cgroup per actor (<actorUID>-_sandbox, ...)
                        try:
                            subs = sorted(os.scandir(e.path), key=lambda x: x.name)[:256]
                        except OSError:
                            subs = []
                        for g in subs:
                            if g.is_dir():
                                gs = sample_cgroup(g.path, False)
                                if gs is not None:
                                    series[f"{t['name']}|{pod['name']}|sub:{g.name}"] = gs
        for name, pattern in self.extra:
            for path in sorted(glob.glob(pattern)):
                s = sample_cgroup(path, True)
                if s is None:
                    continue
                if take_pss:
                    s.update(pss_of(cgroup_pids(path)))
                series[f"{name}|{os.path.basename(path)}|pod"] = s
        return series


def host_load():
    """Host-wide noise indicators: CPU PSI (some avg10, percent of time some task waited) and 1-minute load."""
    out = {}
    psi = read_text("/proc/pressure/cpu")
    if psi:
        out["cpu_psi_some_avg10"] = float(psi.splitlines()[0].split("avg10=")[1].split()[0])
    la = read_text("/proc/loadavg")
    if la:
        out["load1"] = float(la.split()[0])
    return out


def cmd_run(a):
    signal.signal(signal.SIGINT, _stop)
    signal.signal(signal.SIGTERM, _stop)
    smp = Sampler(a)
    meta = {"meta": {"kernel": os.uname().release, "started": time.time(),
                     "interval": a.interval, "cgroup_root": a.cgroup_root, "targets": a.target, "cgroups": a.cgroup,
                     "pss": a.pss, "page_size": os.sysconf("SC_PAGE_SIZE")}}
    out = open(a.out, "w", buffering=1)
    out.write(json.dumps(meta) + "\n")
    end = time.time() + a.duration if a.duration else None
    n = 0
    nxt = time.monotonic()
    while not STOP and (end is None or time.time() < end):
        if time.monotonic() - smp.last_refresh > a.refresh:
            smp.refresh()
        t = time.time()
        out.write(json.dumps({"t": round(t, 3), "host": host_load(), "series": smp.sample(n)}) + "\n")
        n += 1
        nxt += a.interval
        delay = nxt - time.monotonic()
        if delay > 0:
            time.sleep(delay)
        else:
            nxt = time.monotonic()  # fell behind: do not burst
    out.close()
    print(f"collect: {n} samples -> {a.out}", file=sys.stderr)


# ---------------------------------------------------------------- Substrate usage records

USAGE_MSG = "Actor usage sampled"


def parse_usage_lines(lines):
    """Per actor uid: highest working set / usage / peak and the sample count, from the ateworkers'
    `Actor usage sampled` log records (docs/guides/observability.md). Group by ate.stats.source."""
    actors = {}
    for ln in lines:
        ln = ln.strip()
        if USAGE_MSG not in ln:
            continue
        try:
            r = json.loads(ln)
        except ValueError:
            continue
        if r.get("msg") != USAGE_MSG or r.get("ate.stats.source") in (None, "unspecified"):
            continue
        key = (r.get("ate.actor.uid"), r.get("ate.actor.epoch"))
        a = actors.setdefault(key, {"actor": r.get("ate.actor.name"), "template": r.get("ate.template.name"),
                                    "class": r.get("ate.sandbox.class"), "source": r.get("ate.stats.source"),
                                    "samples": 0, "ws_max": 0, "usage_max": 0, "peak_max": 0, "cpu_s_max": 0.0})
        a["samples"] += 1
        a["ws_max"] = max(a["ws_max"], r.get("ate.stats.memory.working_set", 0))
        a["usage_max"] = max(a["usage_max"], r.get("ate.stats.memory.usage", 0))
        a["peak_max"] = max(a["peak_max"], r.get("ate.stats.memory.peak", 0))
        a["cpu_s_max"] = max(a["cpu_s_max"], r.get("ate.stats.cpu.time", 0.0))
    return list(actors.values())


TIMING_MSGS = ("Restore timing breakdown", "Checkpoint timing breakdown")
TIMING_KEY = re.compile(r"^(ate|ateworker)\.actor\.(restore|checkpoint)\.duration\.(.+)$")


def parse_timing_lines(lines):
    """atelet / ateworker `Restore|Checkpoint timing breakdown` records (seconds per phase, one per
    operation, unsampled; docs/guides/observability.md). Grouped by operation, layer (atelet or ateworker),
    template, snapshot kind and sandbox class; failed operations (error.type present) are skipped."""
    groups = {}
    for ln in lines:
        if not any(m in ln for m in TIMING_MSGS):
            continue
        try:
            r = json.loads(ln)
        except ValueError:
            continue
        if r.get("msg") not in TIMING_MSGS or "error.type" in r:
            continue
        for k, v in r.items():
            m = TIMING_KEY.match(k)
            if not m or not isinstance(v, (int, float)):
                continue
            layer, op, phase = m.groups()
            key = (op, layer, r.get("ate.template.name"), r.get("ate.snapshot.kind"), r.get("ate.snapshot.fidelity"), r.get("ate.sandbox.class"))
            groups.setdefault(key, {}).setdefault(phase, []).append(v)
    out = []
    for (op, layer, tpl, kind, fid, cls), phases in sorted(groups.items(), key=str):
        out.append({"op": op, "layer": layer, "template": tpl, "snapshot_kind": kind, "fidelity": fid, "class": cls,
                    "phases": {p: {"n": len(v), "median_s": round(q(v, .5), 4), "p95_s": round(q(v, .95), 4), "max_s": round(max(v), 4)}
                               for p, v in sorted(phases.items())}})
    return out


def _epoch(ts):
    """RFC3339 with nanoseconds (Substrate log 'time') -> epoch seconds."""
    import datetime
    m = re.match(r"^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(\.\d+)?Z$", ts or "")
    if not m:
        return None
    base = datetime.datetime.strptime(m.group(1), "%Y-%m-%dT%H:%M:%S").replace(tzinfo=datetime.timezone.utc).timestamp()
    return base + (float(m.group(2)) if m.group(2) else 0.0)


def parse_state_lines(lines):
    """ateapi `Actor state changed` records: one per committed transition (create, resuming, running, suspending,
    suspended, ...). Gives the actor name -> uid map and the server-side timeline of every session."""
    out = []
    for ln in lines:
        if "Actor state changed" not in ln:
            continue
        try:
            r = json.loads(ln)
        except ValueError:
            continue
        if r.get("msg") != "Actor state changed":
            continue
        out.append({"t": _epoch(r.get("time")), "name": r.get("ate.actor.name"), "uid": r.get("ate.actor.uid"),
                    "template": r.get("ate.template.name"), "op": r.get("ate.actor.operation.name"), "state": r.get("ate.actor.state")})
    return out


def parse_timing_raw(lines):
    """One entry per atelet Restore/Checkpoint timing record (not aggregated): time, actor uid, kind, phases in seconds."""
    out = []
    for ln in lines:
        if not any(m in ln for m in TIMING_MSGS):
            continue
        try:
            r = json.loads(ln)
        except ValueError:
            continue
        if r.get("msg") not in TIMING_MSGS or "error.type" in r:
            continue
        ph = {}
        for k, v in r.items():
            m = TIMING_KEY.match(k)
            if m and isinstance(v, (int, float)):
                ph[m.group(1) + "." + m.group(2) + "." + m.group(3)] = v
        out.append({"t": _epoch(r.get("time")), "uid": r.get("ate.actor.uid"), "name": r.get("ate.actor.name"),
                    "template": r.get("ate.template.name"), "op": "restore" if r["msg"].startswith("Restore") else "checkpoint",
                    "kind": r.get("ate.snapshot.kind"), "fidelity": r.get("ate.snapshot.fidelity"), "phases": ph})
    return out


def cmd_logs(a):
    """Fetch component logs (one --source NS:SELECTOR per pod group, e.g. ate-system:<atelet selector> and
    the actor namespace's worker pods) and extract per-actor usage and per-operation restore timing."""
    lines = []
    for path in a.file:
        with open(path, errors="replace") as f:
            lines += f.read().splitlines()
    for src in a.source:
        ns, _, sel = src.partition(":")
        cmd = ["kubectl", "logs", "-n", ns, "-l", sel, "--all-containers", "--prefix=false", "--tail=-1", "--max-log-requests=100"]
        if a.since_time:
            cmd += ["--since-time", a.since_time]
        out = subprocess.run(cmd, capture_output=True, text=True, timeout=300)
        if out.returncode != 0:
            print(out.stderr, file=sys.stderr)
            sys.exit(1)
        lines += out.stdout.splitlines()
    res = {"actors": parse_usage_lines(lines), "timing": parse_timing_lines(lines),
           "timing_raw": parse_timing_raw(lines), "states": parse_state_lines(lines)}
    with open(a.out, "w") as f:
        json.dump(res, f, indent=1)
    print(f"collect: {len(res['actors'])} actor activations, {len(res['timing'])} timing groups, {len(res['states'])} state changes -> {a.out}", file=sys.stderr)


# ---------------------------------------------------------------- report

def q(vals, p):
    if not vals:
        return None
    s = sorted(vals)
    pos = p * (len(s) - 1)
    lo = int(pos)
    if lo + 1 >= len(s):
        return s[-1]
    return s[lo] + (pos - lo) * (s[lo + 1] - s[lo])


MIB = 1 << 20


def load_mem(path):
    meta, samples = None, []
    with open(path) as f:
        for line in f:
            d = json.loads(line)
            if "meta" in d:
                meta = d["meta"]
            else:
                samples.append(d)
    return meta, samples


def group_target(series):
    """target -> {'pod': {pod: sample}, 'leaf': {pod|leaf: sample}}; `sub:` series (grandchildren, e.g. Substrate
    actor cgroups inside the worker container scope) are left out of the leaf totals so nothing is counted twice."""
    g = {}
    for key, s in series.items():
        target, pod, scope = key.split("|", 2)
        slot = g.setdefault(target, {"pod": {}, "leaf": {}})
        if scope == "pod":
            slot["pod"][pod] = s
        elif scope.startswith("leaf:"):
            slot["leaf"][pod + "|" + scope] = s
    return g


def total(slot, kind, field):
    vals = [s[field] for s in slot[kind].values() if field in s]
    return sum(vals) if vals else None


def phase_windows(basket):
    """phase name -> list of (start, end) across measured runs."""
    w = {}
    for r in basket.get("runs", []):
        if r.get("warmup"):
            continue
        for p in r.get("phases", []):
            w.setdefault(p["name"], []).append((p["start"], p["end"]))
    return w


def build_report(mem_path, basket_path=None, idle_first=None):
    meta, samples = load_mem(mem_path)
    windows = {"all": [(samples[0]["t"], samples[-1]["t"])]} if samples else {}
    if basket_path:
        windows.update(phase_windows(json.load(open(basket_path))))
    rows = []
    for pname, spans in windows.items():
        per = {}   # target -> metric -> [values]
        cpu = {}   # target -> [(t, cpu_usec_total)]
        hp = []    # host CPU pressure samples inside the phase
        for s in samples:
            if not any(a <= s["t"] <= b for a, b in spans):
                continue
            if "cpu_psi_some_avg10" in s.get("host", {}):
                hp.append(s["host"]["cpu_psi_some_avg10"])
            for target, slot in group_target(s["series"]).items():
                m = per.setdefault(target, {})
                for kind in ("pod", "leaf"):
                    for field in ("working_set", "current", "anon", "shmem", "file", "pss", "rss"):
                        v = total(slot, kind, field)
                        if v is not None:
                            m.setdefault(f"{kind}_{field}", []).append(v)
                cpu.setdefault(target, []).append((s["t"], total(slot, "pod", "cpu_usec")))
        for target, m in sorted(per.items()):
            row = {"phase": pname, "target": target, "n": len(next(iter(m.values()), [])), "spans": len(spans)}
            for metric, vals in m.items():
                row[metric] = {"median_mib": round(q(vals, .5) / MIB, 1), "p95_mib": round(q(vals, .95) / MIB, 1),
                               "max_mib": round(max(vals) / MIB, 1)}
            cs = [c for c in cpu.get(target, []) if c[1] is not None]
            if len(cs) > 1:
                # CPU between consecutive samples inside spans; cgroup counters are cumulative per pod, so
                # pods that appear or vanish are ignored by taking only non-negative deltas.
                d = [max(b[1] - a[1], 0) for a, b in zip(cs, cs[1:])]
                row["cpu_ms"] = round(sum(d) / 1000, 1)
            if hp:
                row["host_cpu_psi_max"] = max(hp)
            rows.append(row)
    return {"meta": meta, "phases": rows}


def to_markdown(rep):
    lines = ["| phase | target | samples | pod WS median MiB | pod WS p95 | pod WS max | leaf WS median | leaf WS max | pod PSS median | cpu ms | host CPU PSI max % |",
             "|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|"]
    for r in rep["phases"]:
        def g(k, f):
            return r.get(k, {}).get(f, "")
        lines.append(f"| {r['phase']} | {r['target']} | {r['n']} | {g('pod_working_set','median_mib')} | "
                     f"{g('pod_working_set','p95_mib')} | {g('pod_working_set','max_mib')} | "
                     f"{g('leaf_working_set','median_mib')} | {g('leaf_working_set','max_mib')} | "
                     f"{g('pod_pss','median_mib')} | {r.get('cpu_ms','')} | {r.get('host_cpu_psi_max','')} |")
    return "\n".join(lines)


def cmd_report(a):
    rep = build_report(a.mem, a.basket)
    print(json.dumps(rep, indent=1) if a.format == "json" else to_markdown(rep))


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    r = sub.add_parser("run")
    r.add_argument("--out", required=True)
    r.add_argument("--target", action="append", default=[], help="NAME:ns=NS,sel=LABELSELECTOR (repeatable)")
    r.add_argument("--cgroup", action="append", default=[], help="NAME:/sys/fs/cgroup/glob for non-pod cgroups")
    r.add_argument("--interval", type=float, default=1.0)
    r.add_argument("--duration", type=float, default=0, help="seconds; 0 = until SIGINT/SIGTERM")
    r.add_argument("--refresh", type=float, default=5.0, help="seconds between pod listings")
    r.add_argument("--pss", action="store_true", help="also sum PSS/RSS over the cgroup's processes (needs root)")
    r.add_argument("--pss-every", type=int, default=5, help="take PSS every N samples")
    r.add_argument("--cgroup-root", default=CG)
    r.add_argument("--fake-pods", help="JSON {target: [{name,uid,containers}]} instead of kubectl (tests)")
    r.set_defaults(fn=cmd_run)
    l = sub.add_parser("logs")
    l.add_argument("--out", required=True)
    l.add_argument("--source", action="append", default=[], help="NS:LABELSELECTOR of pods whose logs to read (repeatable): atelet pods and the worker pods")
    l.add_argument("--file", action="append", default=[], help="a log file captured earlier (kubectl logs -f > file); container logs rotate, so stream them during long runs")
    l.add_argument("--since-time")
    l.set_defaults(fn=cmd_logs)
    p = sub.add_parser("report")
    p.add_argument("--mem", required=True)
    p.add_argument("--basket")
    p.add_argument("--format", choices=["md", "json"], default="md")
    p.set_defaults(fn=cmd_report)
    a = ap.parse_args()
    a.fn(a)


if __name__ == "__main__":
    main()
