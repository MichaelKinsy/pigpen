#!/usr/bin/env python3
"""Turns the raw output of run.sh into port/perf/results.md.   usage: summarize.py <out dir> [meta text file]"""
import glob, json, os, re, statistics, sys

out = sys.argv[1]
meta = open(sys.argv[2]).read().strip() if len(sys.argv) > 2 else ""
med = statistics.median


def rounds(path_glob):
    """{op: {metric: [value per round]}} from files named <prefix>.<round>.<group>.txt (round 0, the warm-up, is skipped)."""
    res = {}
    for f in sorted(glob.glob(path_glob)):
        round_no = int(f.split(".")[-3])
        if round_no == 0:
            continue
        for line in open(f):
            m = re.match(r"Benchmark(\w+?)(?:-\d+)?\s+(\d+)\s+([\d.]+) ns/op\s+(\d+) B/op\s+(\d+) allocs/op", line)
            if m:
                d = res.setdefault(m.group(1), {})
                d.setdefault("ns", []).append(float(m.group(3)))
                d.setdefault("B", []).append(float(m.group(4)))
                d.setdefault("allocs", []).append(float(m.group(5)))
                continue
            m = re.match(r"Benchmark(\w+) ns/op=(\d+) B/op=(\w+)", line)
            if m:
                d = res.setdefault(m.group(1), {})
                d.setdefault("ns", []).append(float(m.group(2)))
                if m.group(3) != "gc":
                    d.setdefault("B", []).append(float(m.group(3)))
    return res


new, orig, old = rounds(f"{out}/bench/go-new.*.txt"), rounds(f"{out}/bench/orig.*.txt"), rounds(f"{out}/bench/go-old.*.txt")


def fmt_t(ns):
    if ns >= 1e6:
        return f"{ns / 1e6:.2f} ms"
    if ns >= 1e3:
        return f"{ns / 1e3:.1f} µs"
    return f"{ns:.0f} ns"


def fmt_b(b):
    return f"{b / 1024:.1f} KiB" if b >= 1024 else f"{b:.0f} B"


def pct(a, b):
    return f"{(a / b - 1) * 100:+.0f}%"


def m(d, op, k):
    v = d.get(op, {}).get(k)
    return med(v) if v else None


lines = ["# Raw performance numbers (port/perf)", "", meta, ""]

# ---- sizes
lines += ["## Binary sizes", "", "```", open(f"{out}/sizes.txt").read().strip() if os.path.exists(f"{out}/sizes.txt") else "(size stage not run)", "```", ""]
def read(path, default=""):
    return open(path).read().strip() if os.path.exists(path) else default


def sections(label):
    rows = {}
    for line in read(f"{out}/bin/sections-{label}.txt").splitlines():
        mm = re.match(r"\s*\[\s*\d+\]\s+(\S+)\s+\S+\s+[0-9a-f]+\s+[0-9a-f]+\s+([0-9a-f]+)", line)
        if mm and mm.group(1) in (".text", ".rodata", ".gopclntab", ".typelink", ".itablink", ".go.buildinfo", ".data", ".noptrdata", ".bss", ".noptrbss"):
            rows[mm.group(1)] = int(mm.group(2), 16)
    return rows


sn, so = sections("new"), sections("old")
if sn:
    lines += ["ELF section sizes of the standalone extension (stripped build as PiG makes it is the file size above; these are the unstripped build's sections, bytes):", "", "| section | port | 0.1.1 |", "|---|---|---|"]
    lines += [f"| `{k}` | {sn[k]:,} | {so.get(k, 0):,} |" for k in sn] + [""]
def by_package(label):
    pk, total = {}, 0
    for line in read(f"{out}/bin/ext-{label}.nm").splitlines():
        parts = line.split(None, 3)
        if len(parts) < 4 or parts[2] not in "TtRrDdBb" or not parts[1].isdigit():
            continue
        name = re.sub(r"^(type:|go:info\.|go:|\*|\[\]|\.namedata\.)+", "", parts[3].strip())
        mm = re.match(r"^((?:[^\s/(]+/)*[^\s/.(]+)\.", name)
        key = mm.group(1) if mm else "(unnamed)"
        pk[key] = pk.get(key, 0) + int(parts[1])
        total += int(parts[1])
    return pk, total


pn, tn = by_package("new")
po, to = by_package("old")
if pn:
    lines += [f"Symbol bytes by package (unstripped build, `go tool nm -size`, text, read-only and data symbols; total {tn:,} port, {to:,} 0.1.1):", "", "| package | port | 0.1.1 |", "|---|---|---|"]
    lines += [f"| `{k}` | {v:,} | {po.get(k, 0):,} |" for k, v in sorted(pn.items(), key=lambda kv: -kv[1])[:16]] + [""]
dn, do = set(read(f"{out}/bin/deps-new.txt").split()), set(read(f"{out}/bin/deps-old.txt").split())
if dn:
    extra = sorted(d for d in dn - do if not d.startswith("internal") and "/internal/" not in d)
    lines += [f"Packages linked: {len(dn)} (port), {len(do)} (0.1.1). Linked only by the port: " + ", ".join(f"`{d}`" for d in extra) + ".", ""]
dic = read(f"{out}/bin/pitui-dictionaries.txt")
if dic:
    total = sum(int(l.split()[4]) for l in dic.splitlines() if len(l.split()) >= 5)
    lines += [f"pitui's embedded word-segmentation dictionaries (`go:embed`, linked through `pitui.Editor`): {total:,} bytes in `.rodata`:", "", "```", dic, "```", ""]
runners = {}
for v in ("new", "old", "orig", "control"):
    for f in glob.glob(f"{out}/pighome/{v}/cache/cells/go/*/runner"):
        runners[v] = os.path.getsize(f)
if runners:
    lines += ["The extension binary as PiG builds and caches it (`$PIG_HOME/cache/cells/go/<hash>/runner`, bytes): " + ", ".join(f"{k} {v:,}" for k, v in runners.items()) + ".", ""]

# ---- in-process
order = ["SessionStart", "ToolGetGoalSummary", "ToolGetGoalTasks", "ToolCreateGoal", "ToolUpdateGoalPause", "ToolSetGoalTasks", "ToolUpdateGoalTask", "CmdGoalList", "CmdGoalStatus", "CmdGoalStatusVerbose", "CmdGoalRefresh", "CmdGoalDirect", "RenderDashboardCompact", "RenderDashboardExpanded", "KeyPlain", "KeyToggleExpand", "SchedulerTick",
         "BigLedgerSessionStart", "BigLedgerGetGoalSummary", "BigLedgerGoalStatus", "BigLedgerRender", "BigLedgerKeyToggleExpand", "BigLedgerUpdateGoalTask"]
n_rounds = len(next(iter(new.values()))["ns"]) if new else 0
lines += [f"## In-process (median of {n_rounds} interleaved rounds)", "",
          "Go: ns, B and allocs per operation (`go test -bench -benchmem`). Original: ns per operation; B is the growth of the V8 used heap over the timed sections (a lower bound; `gc` rows were not measurable). 0.1.1: the commands it implemented.", "",
          "| operation | port | B/op | allocs/op | original | B/op (V8) | port/original | 0.1.1 | port/0.1.1 |", "|---|---|---|---|---|---|---|---|---|"]
for op in order:
    if op not in new:
        continue
    n_ns, n_b, n_a = m(new, op, "ns"), m(new, op, "B"), m(new, op, "allocs")
    o_ns, o_b = m(orig, op, "ns"), m(orig, op, "B")
    p_ns = m(old, op, "ns")
    lines.append(f"| {op} | {fmt_t(n_ns)} | {fmt_b(n_b)} | {n_a:.0f} | {fmt_t(o_ns) if o_ns else 'n/a'} | {fmt_b(o_b) if o_b is not None else 'n/a'} | {pct(n_ns, o_ns) if o_ns else 'n/a'} | {fmt_t(p_ns) if p_ns else 'n/a (refuses)' if op.startswith('Tool') else 'n/a'} | {pct(n_ns, p_ns) if p_ns else 'n/a'} |")
if "RenderPush" in new:
    lines += ["", f"RenderPush (the port's dashboard layout plus the push of its lines, done on every UI update; the original lays out in the host): {fmt_t(m(new, 'RenderPush', 'ns'))}, {fmt_b(m(new, 'RenderPush', 'B'))}, {m(new, 'RenderPush', 'allocs'):.0f} allocs."]
lines += ["", "Per-round values (ns/op), port | original | 0.1.1:", ""]
for op in order:
    if op in new:
        lines.append(f"- {op}: " + " ".join(f"{x:.0f}" for x in new[op]["ns"]) + " | " + (" ".join(f"{x:.0f}" for x in orig[op]["ns"]) if op in orig else "-") + " | " + (" ".join(f"{x:.0f}" for x in old[op]["ns"]) if op in old else "-"))
lines.append("")

# ---- rpc
rpc_path = f"{out}/rpc/rpc.json"
if os.path.exists(rpc_path):
    d = json.load(open(rpc_path))
    res = d["results"]
    subs = d["subjects"]
    by = {s: [r for r in res if r["subject"] == s] for s in subs}
    lines += [f"## Through a real pig (RPC mode, {d['runs']} interleaved runs, CPUs {d['cpus']})", "",
              "### Cold start: spawn of pig to the `get_commands` response (ms)", "", "| subject | median | min | max | extension's share over the control | pig's trace: extension spawn to handshake |", "|---|---|---|---|---|---|"]
    ctrl = {"control": "control", "control-piglet": "control-piglet"}
    for s in subs:
        v = [r["coldMs"] for r in by[s]]
        base = "control-piglet" if s.endswith("piglet") else "control"
        share = "" if s.startswith("control") else f"{med(v) - med([r['coldMs'] for r in by[base]]):+.1f} ms"
        spans = []
        for r in by[s]:
            for k in r["marks"]:
                mm = re.match(r"extension\.(.+)\.spawn-start$", k)
                if mm and f"extension.{mm.group(1)}.handshake-done" in r["marks"]:
                    spans.append(r["marks"][f"extension.{mm.group(1)}.handshake-done"] - r["marks"][k])
        lines.append(f"| {s} | {med(v):.1f} | {min(v):.1f} | {max(v):.1f} | {share} | {f'{med(spans):.1f} ms' if spans else 'n/a'} |")
    lines += ["", "First start of each subject (the warm-up round: PiG builds a Go extension from source, jiti transpiles the TypeScript; a Piglet needs neither), ms: " + ", ".join(f"{r['subject']} {r['coldMs']:.0f}" for r in d.get("firstStart", [])), ""]
    lines += ["", "Every round's cold start (ms): ", ""]
    for s in subs:
        lines.append(f"- {s}: " + " ".join(f"{r['coldMs']:.1f}" for r in by[s]))
    lines += ["", "Pig's own marks, median (ms since process start): ", "", "| subject | " + " | ".join(["extension-discovery-done", "extensions-loaded"]) + " | extension phases |", "|---|---|---|---|"]
    for s in subs:
        mk = {}
        for r in by[s]:
            for k, v in r["marks"].items():
                mk.setdefault(k, []).append(v)
        phases = "; ".join(f"{k.split('.', 2)[2]} {med(v):.0f}" for k, v in sorted(mk.items(), key=lambda kv: med(kv[1])) if k.startswith("extension.") and k.count(".") >= 2)
        lines.append(f"| {s} | {med(mk.get('extension-discovery-done', [float('nan')])):.0f} | {med(mk.get('extensions-loaded', [float('nan')])):.0f} | {phases} |")

    lines += ["", "### Tools and commands (ms; each run's median over its calls, then the median over runs; the first call of each tool in a run is dropped)", "", "| operation | " + " | ".join(s for s in subs if any(r['tools'] or r['cmds'] for r in by[s])) + " | new/orig (fused vs orig where both) |", "|---|" + "---|" * (len([s for s in subs if any(r['tools'] or r['cmds'] for r in by[s])]) + 1)]
    active = [s for s in subs if any(r["tools"] or r["cmds"] for r in by[s])]
    names = []
    for s in active:
        for r in by[s]:
            for k in list(r["tools"]) + list(r["cmds"]):
                if k not in names:
                    names.append(k)

    def per_run(s, name):
        vals = []
        for r in by[s]:
            series = r["tools"].get(name) or r["cmds"].get(name)
            if series and len(series) > 1:
                vals.append(med(series[1:]))
        return vals

    for name in names:
        row = {s: per_run(s, name) for s in active}
        cells = [f"{med(row[s]):.2f}" if row[s] else "n/a" for s in active]
        ratio = f"{pct(med(row['new']), med(row['orig']))}" if row.get("new") and row.get("orig") else "n/a"
        lines.append(f"| {name} | " + " | ".join(cells) + f" | {ratio} |")
    lines.append("")
print("\n".join(lines))
