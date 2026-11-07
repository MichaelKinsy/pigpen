#!/usr/bin/env python3
"""Posthorse perf basket: Go port vs the TypeScript original, same host, hermetic and offline.

Runs inside the xd6702 container (see run.sh). Python 3 standard library only.

  perf.py run --out DIR [--sides go,ts,pi] [--workloads a,b,...] [--samples 15] [--cells all]
  perf.py summarize DIR [--vs DIR2]

The method (what each metric is and how it is read) is README.md in this directory; this file is its implementation.
"""
import argparse, hashlib, json, math, os, pathlib, random, shutil, signal, statistics, subprocess, sys, threading
import time, urllib.request

HERE = pathlib.Path(__file__).resolve().parent
PKG = HERE.parents[1]                      # components/posthorse
ORACLE = PKG / "port" / "oracle"
GOEXT = pathlib.Path(os.environ.get("PH_PERF_GOEXT", PKG / "extensions" / "posthorse"))   # override: pipeline checks against a stub
SCRATCH = pathlib.Path(os.environ.get("PH_PERF_SCRATCH", "/home/kinsy/ph-work/scratch/PerfLane"))
WS = SCRATCH / "ws"                        # fixed workspace path: archive headers carry this cwd
SETTLE_S = 0.5                             # idle time after "ready" before the idle snapshot
TICK_MS = 1000 // os.sysconf("SC_CLK_TCK")
SIDES = {"go": ("pig", "go"), "ts": ("pig", "ts"), "pi": ("pi", "ts"),     # side -> (host, extension language)
         "go-sf": ("pig-sf", "go"), "ts-sf": ("pig-sf", "ts")}              # second pig (PH_PERF_PIG_SF) with its own SDK (PH_PERF_SDK_SF)
PAIRS = [("go", "ts"), ("go-sf", "ts-sf")]                                  # ratchet pairs: Go against TS on the same pig


def host_binary(host_kind):
    return {"pig": os.environ.get("PIGEQ_PIG"), "pi": os.environ.get("PIGEQ_PI"), "pig-sf": os.environ.get("PH_PERF_PIG_SF")}[host_kind]


def host_sdk(host_kind):
    """The SDK directory the Go extension is built against: the one staged by the pig that runs it."""
    return os.environ.get("PH_PERF_SDK_SF") if host_kind == "pig-sf" else os.environ.get("PIG_SDK_DIR")
WORKLOADS = ["a", "b", "c1", "c100", "c1000", "d50", "d200", "e", "f"]
COLD_WORKLOADS = {"a", "b"}


# ---------------------------------------------------------------- /proc helpers

def stat_fields(pid):
    """ppid, own cpu ms (utime+stime), cpu ms of waited-for children (cutime+cstime), in clock ticks (10 ms)."""
    s = pathlib.Path(f"/proc/{pid}/stat").read_text()
    rest = s[s.rindex(")") + 2:].split()
    return int(rest[1]), (int(rest[11]) + int(rest[12])) * TICK_MS, (int(rest[13]) + int(rest[14])) * TICK_MS


def children_of(pid):
    out = []
    for d in os.listdir("/proc"):
        if d.isdigit():
            try:
                if stat_fields(int(d))[0] == pid:
                    out.append(int(d))
            except (OSError, ValueError, IndexError):
                pass
    return out


def descendants(pid):
    found, todo = [], [pid]
    while todo:
        for k in children_of(todo.pop()):
            found.append(k)
            todo.append(k)
    return found


def proc_mem(pid):
    """Rss, Pss, Anonymous (bytes) from smaps_rollup; VmHWM (peak RSS) and threads from status; cpu ms from stat."""
    out = {}
    for line in pathlib.Path(f"/proc/{pid}/smaps_rollup").read_text().splitlines():
        key, _, rest = line.partition(":")
        if key in ("Rss", "Pss", "Anonymous"):
            out[key.lower()] = int(rest.split()[0]) * 1024
    for line in pathlib.Path(f"/proc/{pid}/status").read_text().splitlines():
        if line.startswith("VmHWM:"):
            out["hwm"] = int(line.split()[1]) * 1024
        elif line.startswith("Threads:"):
            out["threads"] = int(line.split()[1])
    _, out["cpu_ms"], out["cchild_ms"] = stat_fields(pid)
    try:                                                   # write/read syscalls and bytes: one write is one wire frame to the host
        for line in pathlib.Path(f"/proc/{pid}/io").read_text().splitlines():
            key, _, rest = line.partition(":")
            if key in ("syscw", "syscr", "wchar", "rchar"):
                out[key] = int(rest)
    except OSError:
        pass
    out["fds"] = len(os.listdir(f"/proc/{pid}/fd"))
    return out


def sum_mem(rows):
    keys = ("rss", "pss", "anonymous", "hwm", "threads", "cpu_ms", "cchild_ms", "fds", "syscw", "syscr", "wchar", "rchar")
    return {k: sum(r.get(k, 0) for r in rows) for k in keys}


# ---------------------------------------------------------------- host

class Host:
    """One host process (pig or pi) in RPC mode with a reader thread that timestamps every stdout line."""

    def __init__(self, argv, cwd, env):
        self.t0 = time.perf_counter_ns()
        self.p = subprocess.Popen(argv, cwd=cwd, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                  stderr=subprocess.PIPE, start_new_session=True)
        self.events, self.cond, self.stderr = [], threading.Condition(), bytearray()
        self.seq = 0
        threading.Thread(target=self._read, daemon=True).start()
        threading.Thread(target=self._err, daemon=True).start()

    def _read(self):
        for line in self.p.stdout:
            now = time.perf_counter_ns()
            try:
                ev = json.loads(line)
            except ValueError:
                ev = {"type": "malformed", "raw": line[:200].decode("utf8", "replace")}
            with self.cond:
                self.events.append((now, ev))
                self.cond.notify_all()
        with self.cond:
            self.events.append((time.perf_counter_ns(), {"type": "eof"}))
            self.cond.notify_all()

    def _err(self):
        for chunk in iter(lambda: self.p.stderr.read1(65536), b""):
            self.stderr.extend(chunk)

    def send(self, obj):
        self.p.stdin.write((json.dumps(obj) + "\n").encode())
        self.p.stdin.flush()
        return time.perf_counter_ns()

    def wait(self, pred, start=0, timeout=120):
        """Index and (t, event) of the first event at or after start for which pred is true."""
        deadline = time.monotonic() + timeout
        i = start
        with self.cond:
            while True:
                while i < len(self.events):
                    t, ev = self.events[i]
                    if pred(ev):
                        return i, t, ev
                    if ev.get("type") == "eof":
                        raise RuntimeError("host exited: " + self.stderr[-1500:].decode("utf8", "replace"))
                    i += 1
                left = deadline - time.monotonic()
                if left <= 0:
                    raise TimeoutError("timeout waiting for host event; stderr: " + self.stderr[-800:].decode("utf8", "replace"))
                self.cond.wait(left)

    def close(self):
        """Close stdin, wait for exit; returns (returncode, rusage of the whole reaped tree)."""
        try:
            self.p.stdin.close()
        except OSError:
            pass
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            pid, status, ru = os.wait4(self.p.pid, os.WNOHANG)
            if pid:
                return os.waitstatus_to_exitcode(status), ru
            time.sleep(0.005)
        os.killpg(self.p.pid, signal.SIGKILL)
        pid, status, ru = os.wait4(self.p.pid, 0)
        return -9, ru


# ---------------------------------------------------------------- environment, snapshot, model

def host_env(root, mem_dir=None, sdk=None):
    e = {"PATH": os.environ["PATH"], "HOME": str(root / "home"), "TMPDIR": str(root / "tmp"), "LANG": "C.UTF-8", "TERM": "dumb",
         "NO_COLOR": "1", "PIG_HOME": str(root / "pighome"), "PIG_CODING_AGENT_DIR": str(root / "agent"),
         "PI_CODING_AGENT_DIR": str(root / "agent"), "PI_SKIP_VERSION_CHECK": "1", "PI_OFFLINE": "1", "PI_TELEMETRY": "0",
         "PIG_OFFLINE": "1", "GIT_TERMINAL_PROMPT": "0", "GIT_CONFIG_NOSYSTEM": "1", "GIT_AUTHOR_NAME": "perf",
         "GIT_AUTHOR_EMAIL": "perf@example.invalid", "GIT_COMMITTER_NAME": "perf", "GIT_COMMITTER_EMAIL": "perf@example.invalid"}
    for k in ("GOROOT", "GOCACHE", "GOMODCACHE", "GOPROXY", "GOFLAGS"):
        if k in os.environ:
            e[k] = os.environ[k]
    if sdk or os.environ.get("PIG_SDK_DIR"):
        e["PIG_SDK_DIR"] = sdk or os.environ["PIG_SDK_DIR"]
    if os.environ.get("PH_PERF_HOSTPROF"):               # diagnosis only: the host's own CPU profile (PIG_PROFILE=cpu)
        e["PIG_PROFILE"], e["PIG_PROFILE_DIR"] = "cpu", str(root / "hostprof")
        (root / "hostprof").mkdir(parents=True, exist_ok=True)
    if mem_dir:
        e["PH_PERF_MEM_DIR"] = str(mem_dir)
        if os.environ.get("PH_PERF_CPUPROF"):
            e["PH_PERF_CPUPROF"] = os.environ["PH_PERF_CPUPROF"]
    return e


def snapshot_sources(snap):
    """Copy the extension under test once per invocation: another lane's sync cannot change it mid-run.
    plain/  = the shipped sources; mem/ = the same plus the measurement-only reporter (port/perf/mem)."""
    shutil.rmtree(snap, ignore_errors=True)
    for variant in ("plain", "mem"):
        dst = snap / variant / "posthorse"            # a direct Go extension directory is named by its registered identity
        shutil.copytree(GOEXT, dst, ignore=shutil.ignore_patterns("*_test.go"))
        if variant == "mem":
            shutil.copy(HERE / "mem" / "zz_perf_mem.go", dst / "zz_perf_mem.go")
    for variant in ("plain", "mem"):
        dst = snap / variant / "oracle"
        shutil.copytree(ORACLE, dst, ignore=shutil.ignore_patterns("test", "docs", "node_modules"), symlinks=True)
        if (ORACLE / "node_modules").exists():
            os.symlink(ORACLE / "node_modules", dst / "node_modules")
    shutil.copy(HERE / "mem" / "node-mem.ts", snap / "mem" / "oracle" / "node-mem.ts")
    (snap / "mem" / "oracle" / "perf-entry.ts").write_text(
        'import "./node-mem.ts";\nimport posthorse from "./index.ts";\nexport default posthorse;\n')


def ext_path(snap, lang, variant):
    if lang == "go":
        return snap / variant / "posthorse"
    return snap / variant / "oracle" / ("perf-entry.ts" if variant == "mem" else "index.ts")


def start_llm(llmbin, turns, root):
    script = root / "turns.json"
    script.write_text(json.dumps(turns))
    p = subprocess.Popen([str(llmbin), "--script", str(script)], stdin=subprocess.PIPE, stdout=subprocess.PIPE)
    url = p.stdout.readline().decode().strip()
    return p, url


def llm_stats(url):
    with urllib.request.urlopen(url.rsplit("/v1", 1)[0] + "/stats", timeout=5) as r:
        return json.loads(r.read())


def write_config(root, url, context_window=100000):
    (root / "agent").mkdir(parents=True, exist_ok=True)
    if url:
        models = {"providers": {"pf": {"baseUrl": url, "api": "openai-completions", "apiKey": "pf-key", "models": [{
            "id": "pf-1", "name": "pf-1", "reasoning": False, "input": ["text"], "contextWindow": context_window, "maxTokens": 4096,
            "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}]}}}
        (root / "agent" / "models.json").write_text(json.dumps(models))
    (root / "agent" / "settings.json").write_text(json.dumps({"lastChangelogVersion": "9999.0.0", "theme": "dark"}))


# ---------------------------------------------------------------- workloads (deterministic data and model scripts)

WORDS = ("alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima mike november oscar papa quebec romeo sierra "
         "tango uniform victor whiskey xray yankee zulu parser buffer socket kernel render cursor ticket vector module bundle "
         "cache router shader tensor planner reducer scanner loader writer reader").split()


def prose(rng, n):
    return " ".join(rng.choice(WORDS) for _ in range(n))


def seed_notes(ws, count):
    rng = random.Random(1000 + count)
    base = ws / ".pi" / "notes"
    for k in range(count):
        d = base / f"area-{k % 10:02d}"
        d.mkdir(parents=True, exist_ok=True)
        needle = " quartz-needle" if k % max(1, count // 3) == 0 else ""
        (d / f"note-{k:04d}.md").write_text(f"# Note {k}\n\n{prose(rng, 90)}{needle}\n\n- {prose(rng, 12)}\n- {prose(rng, 12)}\n")


def notes_turns(count, rounds=6):
    """The scripted notes session: per round list, search, read, write, append (30 calls at 6 rounds), then a text turn."""
    turns = []
    for r in range(rounds):
        k = (r * 7) % count
        read = f"area-{k % 10:02d}/note-{k:04d}.md"
        wr = f"perf/write-{r}.md"
        turns += [
            {"toolCalls": [{"name": "notes", "arguments": {"op": "list"}}]},
            {"toolCalls": [{"name": "notes", "arguments": {"op": "search", "query": "quartz-needle"}}]},
            {"toolCalls": [{"name": "notes", "arguments": {"op": "read", "path": read}}]},
            {"toolCalls": [{"name": "notes", "arguments": {"op": "write", "path": wr, "content": "# Task\n\n" + prose(random.Random(r), 60) + "\n"}}]},
            {"toolCalls": [{"name": "notes", "arguments": {"op": "append", "path": wr, "content": "progress " + prose(random.Random(100 + r), 10)}}]},
        ]
    return turns + [{"text": "notes done"}]


# Archive: deterministic Pi-format session files (cwd = the fixed workspace) of mixed user/assistant/tool entries.
ARCHIVE_VERSION = 1
RARE = "zebrafish-ledger-742"


def entry_id(file_no, n):
    return "%08x" % ((file_no * 2654435761 + n * 40503 + 12345) & 0xFFFFFFFF)


def make_archive(mb, force=False):
    d = SCRATCH / "archives" / f"{mb}mb"
    marker = d / ".complete"
    if marker.exists() and marker.read_text() == str(ARCHIVE_VERSION) and not force:
        return d
    shutil.rmtree(d, ignore_errors=True)
    d.mkdir(parents=True)
    per_file = 5 * 1024 * 1024
    files = (mb * 1024 * 1024) // per_file
    rng = random.Random(4242)
    hit_id = None
    for f in range(files):
        name = d / f"2026-10-{1 + f % 28:02d}T00-00-00-{f:03d}Z_perf{f:04d}.jsonl"
        with open(name, "w") as out:
            out.write(json.dumps({"type": "session", "version": 3, "id": f"perf{f:04d}", "timestamp": "2026-10-01T00:00:00.000Z", "cwd": str(WS / "work")}) + "\n")
            size, n, parent = 0, 0, None
            while size < per_file:
                eid = entry_id(f, n)
                if n % 3 == 0:
                    msg = {"role": "user", "content": [{"type": "text", "text": prose(rng, rng.randint(20, 90))}], "timestamp": 1760000000000 + n}
                elif n % 3 == 1:
                    text = prose(rng, rng.randint(60, 220))
                    if f % 3 == 0 and n % 997 == 1:
                        text += " " + RARE
                        hit_id = hit_id or eid
                    msg = {"role": "assistant", "content": [{"type": "text", "text": text}, {"type": "toolCall", "id": f"call_{f}_{n}", "name": "read", "arguments": {"path": f"src/{rng.choice(WORDS)}.go"}}],
                           "api": "openai-completions", "provider": "pf", "model": "pf-1", "usage": {"input": 100, "output": 50, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 150}, "stopReason": "toolUse", "timestamp": 1760000000000 + n}
                else:
                    msg = {"role": "toolResult", "toolCallId": f"call_{f}_{n - 1}", "toolName": "read", "content": [{"type": "text", "text": prose(rng, rng.randint(100, 400))}], "isError": False, "timestamp": 1760000000000 + n}
                line = json.dumps({"type": "message", "id": eid, "parentId": parent, "timestamp": "2026-10-01T00:00:00.000Z", "message": msg}, separators=(",", ":")) + "\n"
                out.write(line)
                size += len(line)
                parent = eid
                n += 1
        os.utime(name, (1760000000 + f * 60, 1760000000 + f * 60))
    (d / ".hit").write_text(hit_id or "")
    marker.write_text(str(ARCHIVE_VERSION))
    return d


def archive_turns(hit):
    return [
        {"toolCalls": [{"name": "history", "arguments": {"op": "search", "query": RARE, "all": True}}]},
        {"toolCalls": [{"name": "history", "arguments": {"op": "search", "query": "the kernel", "all": True, "limit": 50}}]},
        {"toolCalls": [{"name": "history", "arguments": {"op": "read", "id": hit}}]},
        {"text": "history done"},
    ]


def rollover_turns(prompts=20, per_prompt=10):
    """200 model requests in 20 prompts of 9 tool turns and a closing text turn. The reported usage follows the request
    size, so the transcript growth (one 24 KB read in most prompts, notes writes and appends, a get_context_remaining) walks
    Pi's compaction threshold, which Posthorse hooks: reminders arrive in the band below the line and the rollover at the
    run's end. Only calls that cannot fail near the line are scripted (no paged notes reads)."""
    rng = random.Random(7)
    turns = []
    for p in range(prompts):
        for i in range(per_prompt - 1):
            k = (p * 3 + i) % 6
            if k in (0, 3):
                calls = [{"name": "notes", "arguments": {"op": "write", "path": "task.md", "content": "# Task\n\n" + prose(rng, 80) + "\n"}}]
            elif k == 1:
                calls = [{"name": "get_context_remaining", "arguments": {}}]
            elif k in (2, 4):
                calls = [{"name": "notes", "arguments": {"op": "append", "path": "task.md", "content": "step " + prose(rng, 8)}}]
            else:
                calls = [{"name": "read", "arguments": {"path": "fixtures/big.txt"}}]
            turns.append({"text": "working " + prose(rng, 6), "toolCalls": calls})
        turns.append({"text": "step batch complete"})
    return turns


def seed_big_file(ws, kb=24):
    d = ws / "fixtures"
    d.mkdir(parents=True, exist_ok=True)
    rng = random.Random(99)
    lines, size = [], 0
    while size < kb * 1024:
        line = prose(rng, 14)
        lines.append(line)
        size += len(line) + 1
    (d / "big.txt").write_text("\n".join(lines) + "\n")


def settle_pids(host, host_kind):
    """The extension process(es): descendants of a pig host; the pi host process itself is its own extension host."""
    if host_kind == "pi":
        return [host.p.pid]
    return descendants(host.p.pid)


# ---------------------------------------------------------------- one run

def run_one(cfg, side, variant, workload, cache, idx, snap, llmbin, warm_root):
    host_kind, lang = SIDES[side]
    binary = host_binary(host_kind)
    cold = cache == "cold"
    root = (SCRATCH / "cold" / f"{side}-{variant}-{workload}-{idx}") if cold else warm_root
    if cold:
        shutil.rmtree(root, ignore_errors=True)
    mem_dir = root / "mem" if variant == "mem" else None
    for d in ("home", "agent", "tmp", "pighome") + (("mem",) if mem_dir else ()):
        (root / d).mkdir(parents=True, exist_ok=True)
    if mem_dir:
        for f in mem_dir.glob("*"):
            f.unlink()
    shutil.rmtree(WS, ignore_errors=True)
    work = WS / "work"
    work.mkdir(parents=True)
    subprocess.run(["git", "init", "-q"], cwd=work, env=host_env(root), check=True)
    turns, prompt, session_dir = None, None, None
    if workload == "b":
        turns, prompt = [{"toolCalls": [{"name": "get_context_remaining", "arguments": {}}]}, {"text": "remaining checked"}], "How much context remains?"
    elif workload in ("c1", "c100", "c1000"):
        n = int(workload[1:])
        seed_notes(work, n)
        turns, prompt = notes_turns(n), "Exercise the notes tool."
    elif workload in ("d50", "d200"):
        arch = make_archive(int(workload[1:]))
        session_dir = WS / "sessions"
        session_dir.mkdir()
        subprocess.run(["cp", "-al", *map(str, arch.glob("*.jsonl")), str(session_dir)], check=True)
        turns, prompt = archive_turns((arch / ".hit").read_text()), "Search the history archive."
    elif workload == "e":
        seed_big_file(work)
        session_dir = WS / "sessions"
        session_dir.mkdir()
        turns, prompt = rollover_turns(), [f"Continue the task, batch {k}." for k in range(20)]
    llm, url = (start_llm(llmbin, turns, root) if turns else (None, None))
    write_config(root, url)
    args = ["--mode", "rpc", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files", "--no-extensions", "--offline"]
    args += ["--session-dir", str(session_dir)] if session_dir else ["--no-session"]
    if url:
        args += ["--provider", "pf", "--model", "pf-1"]
    args += ["-e", str(ext_path(snap, lang, variant))]
    row = {"side": side, "variant": variant, "workload": workload, "cache": cache, "run": idx, "ok": False,
           "load1": os.getloadavg()[0]}
    host = Host([binary] + args, work, host_env(root, mem_dir, host_sdk(host_kind)))
    try:
        sent = host.send({"id": "ready", "type": "get_state"})
        i, t, ev = host.wait(lambda e: e.get("id") == "ready", timeout=120)
        row["startup_ms"] = (t - host.t0) / 1e6
        if not ev.get("success"):
            raise RuntimeError(f"get_state failed: {ev}")
        time.sleep(SETTLE_S)
        pids = settle_pids(host, host_kind)
        row["ext_count"] = len(pids)
        if host_kind != "pi" and not pids:
            raise RuntimeError("no extension process under the pig host (the extension did not load?) stderr: " + host.stderr[-800:].decode("utf8", "replace"))
        row["ready"] = snapshot(host, pids, variant, mem_dir, "ready")
        cursor = len(host.events)
        if prompt:
            row.update(drive_prompts(host, prompt if isinstance(prompt, list) else [prompt], cursor, workload))
            row["llm"] = llm_stats(url)
            if workload == "e":
                row["session"] = session_counts(session_dir)
            if row["llm"]["overrun"]:
                row["note"] = f"model script overrun by {row['llm']['overrun']} requests"
        pids = settle_pids(host, host_kind)
        row["end"] = snapshot(host, pids, variant, mem_dir, "end")
        row["end"]["ext_count"] = len(pids)
        row["ok"] = True
    except Exception as exc:                                         # recorded, not retried; the caller aborts on a bad row
        row["error"] = f"{type(exc).__name__}: {exc}"
    finally:
        rc, ru = host.close()
        row["returncode"], row["cpu_tree_ms"] = rc, (ru.ru_utime + ru.ru_stime) * 1000
        row["stderr_bytes"] = len(host.stderr)
        if host.stderr and not row["ok"]:
            row["stderr_tail"] = host.stderr[-1000:].decode("utf8", "replace")
        if llm:
            llm.stdin.close()
            llm.wait()
    row["wall_ms"] = (time.perf_counter_ns() - host.t0) / 1e6
    if mem_dir:                                           # diagnosis only: PH_PERF_CPUPROF=1 leaves CPU profiles in scratch/prof
        for prof in mem_dir.glob("*.pprof"):
            (SCRATCH / "prof").mkdir(exist_ok=True)
            shutil.copy(prof, SCRATCH / "prof" / f"{side}-{workload}-{idx}-{prof.name}")
    if os.environ.get("PH_PERF_HOSTPROF"):
        (SCRATCH / "prof").mkdir(exist_ok=True)
        for prof in (root / "hostprof").glob("*"):
            shutil.copy(prof, SCRATCH / "prof" / f"host-{side}-{variant}-{workload}-{idx}-{prof.name}")
            prof.unlink()
    if cold:
        shutil.rmtree(root, ignore_errors=True)
    if row["ok"] and row["returncode"] != 0:
        row["ok"], row["error"] = False, f"host exit code {row['returncode']}"
    if row["ok"] and b"rror" in bytes(host.stderr):
        row["ok"], row["error"] = False, "stderr: " + host.stderr[-500:].decode("utf8", "replace")
    return row


def session_counts(session_dir):
    """What the run left in the session file: proof that the scripted 200 turns really rolled over and reminded."""
    counts = {"entries": 0, "context_window": 0, "compaction": 0, "custom_message": 0, "tool_results": 0}
    for f in session_dir.glob("*.jsonl"):
        for line in f.read_text().splitlines():
            counts["entries"] += 1
            for key in ("context_window", "compaction", "custom_message"):
                if line.startswith('{"type":"%s"' % key):
                    counts[key] += 1
            if '"role":"toolResult"' in line:
                counts["tool_results"] += 1
    return counts


def snapshot(host, ext_pids, variant, mem_dir, tag):
    snap = {"host": proc_mem(host.p.pid), "ext": sum_mem([proc_mem(p) for p in ext_pids]) if ext_pids else {}}
    snap["ext_pids"] = ext_pids
    if variant == "mem" and ext_pids:
        before = set(mem_dir.glob("*.json"))
        for p in ext_pids:
            os.kill(p, signal.SIGUSR2)
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline and len(set(mem_dir.glob("*.json")) - before) < len(ext_pids):
            time.sleep(0.01)
        got = sorted(set(mem_dir.glob("*.json")) - before)
        snap["runtime"] = [json.loads(f.read_text()) for f in got]
        if len(got) != len(ext_pids):
            raise RuntimeError(f"memory reporter answered {len(got)} of {len(ext_pids)} processes at {tag}")
    return snap


# A search that finds nothing would time a trivial path: the result of these calls must contain the planted needle.
EXPECT = {"history.search:rare": RARE, "notes.search": "quartz-needle"}


def drive_prompts(host, prompts, cursor, workload):
    out = {"tools": []}
    for p in prompts:
        r = drive_prompt(host, p, cursor, workload)
        cursor = len(host.events)
        out["tools"] += r["tools"]
        out["turn_ms"] = out.get("turn_ms", 0) + r["turn_ms"]
        out.setdefault("to_first_tool_ms", r["to_first_tool_ms"])
        if "first_turn_ms" in r:
            out["first_turn_ms"] = r["first_turn_ms"]
    return out


def drive_prompt(host, prompt, cursor, workload):
    out = {"tools": []}
    sent = host.send({"id": "prompt", "type": "prompt", "message": prompt})
    i, t, ev = host.wait(lambda e: e.get("id") == "prompt", start=cursor, timeout=60)
    if not ev.get("success"):
        raise RuntimeError(f"prompt rejected: {ev}")
    pos = i
    starts = {}
    first_tool = None
    while True:
        pos, t, ev = host.wait(lambda e: e.get("type") in ("agent_end", "tool_execution_start", "tool_execution_end"), start=pos, timeout=600)
        pos += 1
        kind = ev["type"]
        if kind == "tool_execution_start":
            starts[ev.get("toolCallId")] = (t, ev.get("toolName", "?"), ev.get("args"))
            if first_tool is None:
                first_tool = (t - sent) / 1e6
        elif kind == "tool_execution_end":
            began = starts.pop(ev.get("toolCallId"), None)
            if ev.get("isError"):
                raise RuntimeError(f"tool error: {json.dumps(ev)[:400]}")
            if began is not None:
                args = began[2] if isinstance(began[2], dict) else {}
                op = args.get("op")
                label = f"{began[1]}.{op}" if op else began[1]
                if began[1] == "history" and op == "search":
                    label += ":rare" if RARE in str(args.get("query")) else ":common"
                need = EXPECT.get(label)
                text = "".join(c.get("text", "") for c in (ev.get("result") or {}).get("content", []) if isinstance(c, dict))
                if need and need not in text.lower():
                    raise RuntimeError(f"{label}: result lacks {need!r} (the workload did not do its work): {text[:300]!r}")
                out["tools"].append({"tool": label, "ms": (t - began[0]) / 1e6})
        else:
            # An automatic rollover may continue the run: agent_end is final only when no agent_start follows.
            try:
                host.wait(lambda e: e.get("type") == "agent_start", start=pos, timeout=0.4)
            except TimeoutError:
                out["turn_ms"] = (t - sent) / 1e6
                break
    out["to_first_tool_ms"] = first_tool
    if workload == "b":
        out["first_turn_ms"] = out["turn_ms"]
    return out


# ---------------------------------------------------------------- many sessions (workload f)

def run_sessions(cfg, side, idx, snap, count, warm_root):
    host_kind, lang = SIDES[side]
    binary = host_binary(host_kind)
    root = warm_root
    for d in ("home", "agent", "tmp", "pighome"):
        (root / d).mkdir(parents=True, exist_ok=True)
    write_config(root, None)
    hosts, row = [], {"side": side, "variant": "plain", "workload": "f", "cache": "warm", "run": idx, "ok": False, "sessions": count, "load1": os.getloadavg()[0]}
    try:
        t0 = time.perf_counter_ns()
        for k in range(count):
            work = SCRATCH / "wsf" / f"s{k}"
            work.mkdir(parents=True, exist_ok=True)
            args = ["--mode", "rpc", "--no-session", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files", "--no-extensions", "--offline",
                    "-e", str(ext_path(snap, lang, "plain"))]
            h = Host([binary] + args, work, host_env(root, None, host_sdk(host_kind)))
            hosts.append(h)
            h.send({"id": "ready", "type": "get_state"})
            h.wait(lambda e: e.get("id") == "ready", timeout=180)
        row["all_ready_ms"] = (time.perf_counter_ns() - t0) / 1e6
        time.sleep(SETTLE_S * 2)
        host_rows, ext_rows, counts = [], [], []
        for h in hosts:
            pids = settle_pids(h, host_kind)
            counts.append(len(pids))
            host_rows.append(proc_mem(h.p.pid))
            ext_rows += [proc_mem(p) for p in pids]
        if host_kind != "pi" and min(counts) < 1:
            raise RuntimeError("a session has no extension process")
        row["end"] = {"host": sum_mem(host_rows), "ext": sum_mem(ext_rows), "ext_count": sum(counts)}
        row["end"]["ext"]["ext_avg_rss"] = row["end"]["ext"]["rss"] / count
        row["end"]["ext"]["ext_avg_pss"] = row["end"]["ext"]["pss"] / count
        row["ok"] = True
    except Exception as exc:
        row["error"] = f"{type(exc).__name__}: {exc}"
    finally:
        for h in hosts:
            h.close()
        shutil.rmtree(SCRATCH / "wsf", ignore_errors=True)
    return row


# ---------------------------------------------------------------- orchestration

def pin(cpus):
    allowed = sorted(os.sched_getaffinity(0))
    if cpus:
        want = set()
        for part in cpus.split(","):
            lo, _, hi = part.partition("-")
            want.update(range(int(lo), int(hi or lo) + 1))
        chosen = [c for c in allowed if c in want]
    else:
        chosen = allowed[-6:]
    os.sched_setaffinity(0, chosen)
    return chosen


def build_helpers():
    bin_dir = SCRATCH / "bin"
    bin_dir.mkdir(parents=True, exist_ok=True)
    env = dict(os.environ, GOWORK="off", CGO_ENABLED="0")
    subprocess.run(["go", "build", "-trimpath", "-ldflags=-s -w", "-o", str(bin_dir / "fakellm"), "."], cwd=HERE / "fakellm", env=env, check=True)
    return bin_dir / "fakellm"


def versions():
    def run(cmd):
        try:
            return subprocess.check_output(cmd, text=True, stderr=subprocess.STDOUT, env=host_env(SCRATCH / "versions")).strip()
        except Exception as exc:
            return f"unavailable: {exc}"
    (SCRATCH / "versions" / "home").mkdir(parents=True, exist_ok=True)
    extra = {"pig_sf": run([os.environ["PH_PERF_PIG_SF"], "--version"]), "pig_sf_sdk": os.environ.get("PH_PERF_SDK_SF")} if os.environ.get("PH_PERF_PIG_SF") else {}
    return {**extra, "pig": run([os.environ["PIGEQ_PIG"], "--version"]), "pi": run([os.environ["PIGEQ_PI"], "--version"]), "pig_sdk": os.environ.get("PIG_SDK_DIR"),
            "node": run(["node", "--version"]), "go": run(["go", "version"]),
            "kernel": run(["uname", "-sr"]), "cpu": next((l.split(":", 1)[1].strip() for l in open("/proc/cpuinfo") if l.startswith("model name")), "?")}


def go_binary_report(side):
    """Size of the extension binary pig built for a Go side, with and without symbols."""
    cells = sorted((SCRATCH / "sizeprobe" / side / "pighome" / "cache" / "cells" / "go").glob("*/runner"), key=lambda p: p.stat().st_mtime)
    if not cells:
        return {}
    binary = cells[-1]
    info = {"runner_bytes": binary.stat().st_size, "sha256": hashlib.sha256(binary.read_bytes()).hexdigest()}
    if shutil.which("strip"):
        copy = SCRATCH / "sizeprobe" / side / "runner.stripped"
        shutil.copy(binary, copy)
        subprocess.run(["strip", "-s", str(copy)], check=False)
        info["stripped_bytes"] = copy.stat().st_size
    return info


def cmd_run(a):
    out = pathlib.Path(a.out).resolve()
    out.mkdir(parents=True, exist_ok=True)
    sides = a.sides.split(",")
    workloads = WORKLOADS if a.workloads == "all" else a.workloads.split(",")
    cpus = pin(a.cpus)
    llmbin = build_helpers()
    snap = SCRATCH / "snap"
    snapshot_sources(snap)
    meta = {"started": time.strftime("%Y-%m-%dT%H:%M:%S%z"), "sides": sides, "workloads": workloads, "samples": a.samples,
            "cpus": cpus, "loadavg_start": os.getloadavg(), "versions": versions(), "settle_s": SETTLE_S, "sessions": a.sessions,
            "args": sys.argv[1:], "extension_sources_sha256": hashlib.sha256(b"".join(p.read_bytes() for p in sorted((snap / "plain" / "posthorse").glob("*.go")))).hexdigest()}
    rows_path = out / "samples.jsonl"
    log = rows_path.open("a")
    warm = {(s, v): SCRATCH / "warm" / f"{s}-{v}" for s in sides for v in ("plain", "mem")}
    shutil.rmtree(SCRATCH / "warm", ignore_errors=True)
    shutil.rmtree(SCRATCH / "cold", ignore_errors=True)
    # Seed every warm home with three unmeasured launches (application caches populated, as startup_timing.py does).
    for side in sides:
        for variant in ("plain", "mem"):
            for k in range(3):
                seed = run_one(a, side, variant, a.seed, "warm", f"seed{k}", snap, llmbin, warm[(side, variant)])
                if not seed["ok"]:
                    raise SystemExit(f"warm seed failed without retry: {json.dumps(seed)[:1500]}")
    shutil.rmtree(SCRATCH / "sizeprobe", ignore_errors=True)
    for side in sides:   # keep the cell binary each pig built for a Go extension, for the size row
        if SIDES[side][1] == "go":
            probe = SCRATCH / "sizeprobe" / side
            shutil.copytree(warm[(side, "plain")] / "pighome", probe / "pighome", symlinks=True)
            meta.setdefault("go_binaries", {})[side] = go_binary_report(side)
    if "go" in meta.get("go_binaries", {}):
        meta["go_binary"] = meta["go_binaries"]["go"]
    for workload in workloads:
        caches = ("warm", "cold") if workload in COLD_WORKLOADS else ("warm",)
        if workload == "f":
            caches = ("warm",)
        for cache in caches:
            for i in range(a.first, a.first + a.samples):
                order = sides[i % len(sides):] + sides[:i % len(sides)]
                for side in order:
                    variants = ("plain",) if workload == "f" else (("plain", "mem") if i % 2 == 0 else ("mem", "plain"))
                    for variant in variants:
                        if workload == "f":
                            row = run_sessions(a, side, i, snap, a.sessions, warm[(side, "plain")])
                        else:
                            row = run_one(a, side, variant, workload, cache, i, snap, llmbin, warm[(side, variant)])
                        log.write(json.dumps(row) + "\n")
                        log.flush()
                        tag = f"{workload}/{cache} {side}/{variant} #{i}"
                        if not row["ok"]:
                            print("INVALID", tag, json.dumps(row)[:1500], file=sys.stderr)
                            if not a.keep_going:
                                raise SystemExit(f"run failed without retry: {tag}")
                        else:
                            print("ok", tag, f"startup={row.get('startup_ms', row.get('all_ready_ms', 0)):.0f}ms", flush=True)
    meta["finished"] = time.strftime("%Y-%m-%dT%H:%M:%S%z")
    meta["loadavg_end"] = os.getloadavg()
    (out / ("meta.json" if a.first == 0 else f"meta-topup-{a.first}-{'-'.join(workloads)}.json")).write_text(json.dumps(meta, indent=2) + "\n")
    shutil.rmtree(SCRATCH / "warm", ignore_errors=True)
    shutil.rmtree(SCRATCH / "cold", ignore_errors=True)
    shutil.rmtree(WS, ignore_errors=True)


# ---------------------------------------------------------------- summary

def q(values, frac):
    v = sorted(values)
    return v[math.ceil(frac * (len(v) - 1))] if v else None


def metrics_of(row):
    """Flatten one row into {metric: value}; list metrics (per-tool latencies) are pooled by the caller."""
    m = {}
    if "startup_ms" in row:
        m["startup_ms"] = row["startup_ms"]
    if "all_ready_ms" in row:
        m["all_ready_ms"] = row["all_ready_ms"]
    for k in ("first_turn_ms", "to_first_tool_ms", "turn_ms"):
        if row.get(k) is not None:
            m[k] = row[k]
    m["cpu_wait4_ms"] = row.get("cpu_tree_ms")
    for tag in ("ready", "end"):
        s = row.get(tag)
        if not s:
            continue
        for part, name in (("ext", "ext"), ("host", "host")):
            for src, dst in (("rss", "rss"), ("pss", "pss"), ("anonymous", "anon"), ("hwm", "peak_rss"), ("cpu_ms", "cpu_ms"), ("cchild_ms", "cchild_ms"), ("threads", "threads")):
                if src in s[part]:
                    m[f"{name}_{dst}_{tag}"] = s[part][src]
        if "ext_avg_rss" in s["ext"]:
            m["ext_avg_rss_end"], m["ext_avg_pss_end"] = s["ext"]["ext_avg_rss"], s["ext"]["ext_avg_pss"]
        if "ext_count" in s:
            m["ext_count"] = s["ext_count"]
        rt = s.get("runtime")
        if rt:
            if rt[0]["runtime"] == "go":
                for state in ("asis", "gc"):
                    m[f"heap_used_{state}_{tag}"] = sum(r[state]["heap_alloc"] for r in rt)
                    m[f"heap_inuse_{state}_{tag}"] = sum(r[state]["heap_inuse"] for r in rt)
                    m[f"sys_{state}_{tag}"] = sum(r[state]["sys"] for r in rt)
                m[f"total_alloc_{tag}"] = sum(r["asis"]["total_alloc"] for r in rt)
                m[f"mallocs_{tag}"] = sum(r["asis"]["mallocs"] for r in rt)
                m[f"num_gc_{tag}"] = sum(r["asis"]["num_gc"] for r in rt)
            else:
                for state in ("asis", "gc"):
                    m[f"heap_used_{state}_{tag}"] = sum(r[state]["heap_used"] for r in rt)
                    m[f"heap_inuse_{state}_{tag}"] = sum(r[state]["heap_total"] for r in rt)
                    m[f"sys_{state}_{tag}"] = sum(r[state]["rss"] for r in rt)
                m[f"num_gc_{tag}"] = sum(r["asis"]["gc_events"] for r in rt)
    for tag in ("ready", "end"):
        s = row.get(tag)
        if s:
            own = row["side"] == "pi"            # under Pi the extension runs inside the host process: count it once
            for src, dst in (("rss", "rss"), ("pss", "pss")):
                m[f"tree_{dst}_{tag}"] = s["host"][src] + (0 if own else s["ext"].get(src, 0))
            # Host and extension process CPU, each with the children it waited for (a cold Go build, git run by the extension).
            m[f"cpu_total_ms_{tag}"] = s["host"]["cpu_ms"] + s["host"].get("cchild_ms", 0) + (0 if own else s["ext"].get("cpu_ms", 0) + s["ext"].get("cchild_ms", 0))
    if "session" in row:
        for k, v in row["session"].items():
            m[f"session_{k}"] = v
    if "llm" in row:
        m["llm_requests"] = row["llm"]["requests"]
    if "end" in row and "ready" in row:
        m["cpu_ext_workload_ms"] = row["end"]["ext"].get("cpu_ms", 0) - row["ready"]["ext"].get("cpu_ms", 0)
        m["cpu_total_workload_ms"] = m["cpu_total_ms_end"] - m["cpu_total_ms_ready"]
        for k in ("syscw", "syscr", "wchar", "rchar"):      # information: extension wire traffic during the workload
            if k in row["end"]["ext"] and k in row["ready"]["ext"] and row["side"] != "pi":
                m[f"ext_{k}_workload"] = row["end"]["ext"][k] - row["ready"]["ext"][k]
        for k in ("total_alloc", "mallocs", "num_gc"):
            if f"{k}_end" in m and f"{k}_ready" in m:
                m[f"{k}_workload"] = m[f"{k}_end"] - m[f"{k}_ready"]
    return {k: v for k, v in m.items() if v is not None}


def load_rows(path):
    p = pathlib.Path(path)
    rows = []
    for line in (p / "samples.jsonl").read_text().splitlines():
        row = json.loads(line)
        if row.get("ok") and not str(row.get("run")).startswith("seed"):
            rows.append(row)
    return rows


def cells(rows):
    """(workload, cache, side, variant) -> {metric: [values]}; tool latencies pooled as tool_ms:<tool.op>."""
    out = {}
    for row in rows:
        key = (row["workload"], row["cache"], row["side"], row["variant"])
        cell = out.setdefault(key, {"_runs": 0})
        cell["_runs"] += 1
        for k, v in metrics_of(row).items():
            cell.setdefault(k, []).append(v)
        for t in row.get("tools", []):
            cell.setdefault(f"tool_ms:{t['tool']}", []).append(t["ms"])
    return out


def fmt(metric, v):
    if v is None:
        return "-"
    if metric.endswith("_ms") or "_ms:" in metric or metric.startswith("tool_ms"):
        return f"{v:.1f}"
    if any(w in metric for w in ("rss", "pss", "anon", "heap", "sys_", "alloc")) and "mallocs" not in metric:
        return f"{v / 1048576:.2f} MiB" if v >= 1024 else f"{v:.0f}"
    return f"{v:.1f}" if isinstance(v, float) and not float(v).is_integer() else f"{int(v)}"


# Metrics that carry the ratchet: Go (side go, pig host) must be <= TS (side ts, same pig host) on each.
RATCHET = ["startup_ms", "first_turn_ms", "to_first_tool_ms", "turn_ms", "cpu_total_ms_end", "cpu_total_workload_ms", "cpu_ext_workload_ms", "ext_cpu_ms_end", "ext_rss_end",
           "ext_pss_end", "tree_rss_end", "tree_pss_end", "ext_peak_rss_end", "ext_anon_end", "ext_rss_ready", "ext_pss_ready", "heap_used_gc_end", "heap_used_asis_end",
           "heap_inuse_gc_end", "heap_inuse_asis_end", "sys_asis_end", "sys_gc_end", "ext_avg_rss_end", "ext_avg_pss_end", "all_ready_ms"]


def table(cells_, sides, workload_cache, metrics):
    lines = []
    for wl, cache in workload_cache:
        lines += [f"### Workload {wl}, {cache}", "", "| Metric | " + " | ".join(f"{s} median / p90 (n)" for s in sides) + " |", "|---|" + "---:|" * len(sides)]
        for metric in metrics:
            row, any_ = [], False
            for s in sides:
                variant = "mem" if (metric.startswith(("heap", "sys", "total_alloc", "mallocs", "num_gc"))) else "plain"
                vals = cells_.get((wl, cache, s, variant), {}).get(metric)
                if vals:
                    any_ = True
                    row.append(f"{fmt(metric, statistics.median(vals))} / {fmt(metric, q(vals, .9))} ({len(vals)})")
                else:
                    row.append("-")
            if any_:
                lines.append(f"| {metric} | " + " | ".join(row) + " |")
        lines.append("")
    return lines


def parity(c, keys, sides):
    """The same scripted workload must leave the same model request count and session records on every side."""
    out = []
    for wl, cache in keys:
        for metric in ("llm_requests", "session_entries", "session_compaction", "session_custom_message", "session_tool_results"):
            seen = {}
            for s in sides:
                vals = c.get((wl, cache, s, "plain"), {}).get(metric)
                if vals:
                    seen[s] = sorted(set(vals))
            if len({json.dumps(v) for v in seen.values()}) > 1:
                out.append(f"- PARITY MISMATCH {wl}/{cache} {metric}: {seen}")
    return out


def spread(c, keys, sides, ordered):
    out = []
    for wl, cache in keys:
        for metric in ordered:
            if metric not in RATCHET and not metric.startswith("tool_ms:"):
                continue
            variant = "mem" if metric.startswith(("heap", "sys")) else "plain"
            for s in sides:
                v = c.get((wl, cache, s, variant), {}).get(metric)
                if not v or s == "pi":
                    continue
                med = statistics.median(v)
                if med <= 0:
                    continue
                sp = (q(v, .9) - med) / med
                if sp > .05:
                    out.append(f"- {wl}/{cache} {s} {metric}: {sp * 100:.1f}% (n={len(v)}, median {fmt(metric, med)})")
    return out


def cmd_summarize(a):
    rows = load_rows(a.dir)
    other = load_rows(a.vs) if a.vs else []
    c = cells(rows + other)
    keys = sorted({(w, ca) for (w, ca, _, _) in c}, key=lambda k: (WORKLOADS.index(k[0]), k[1]))
    sides = [s for s in ("go", "ts", "go-sf", "ts-sf", "pi") if any(k[2] == s for k in c)]
    all_metrics = sorted({m for cell in c.values() for m in cell if m != "_runs"})
    ordered = [m for m in RATCHET if m in all_metrics] + [m for m in all_metrics if m not in RATCHET]
    headline = [m for m in ordered if m in RATCHET or m.startswith(("tool_ms:", "total_alloc_workload", "mallocs_workload", "num_gc_workload", "ext_count", "llm_requests", "session_"))]
    lines = ["# Posthorse perf summary", "", "Medians and p90 over fresh-process samples; n in parentheses. Metric definitions: README.md. Every metric: summary-full.md.", ""]
    full = ["# Posthorse perf summary (every metric)", ""] + table(c, sides, keys, ordered)
    (pathlib.Path(a.dir) / "summary-full.md").write_text("\n".join(full) + "\n")
    lines += table(c, sides, keys, headline)
    mismatches = parity(c, keys, sides)
    lines += (["## Behaviour parity of the workloads (model requests, session records)", ""] + (mismatches or ["identical across sides."]) + [""])
    verdict, misses = [], 0
    for lhs, rhs, gate in [(g, t, True) for g, t in PAIRS] + [("go-sf", "go", False)]:
        if lhs not in sides or rhs not in sides:
            continue
        what = "Go <= TS on the same pig" if gate else "information: new pig and SDK against old (go-sf <= go)"
        verdict += [f"## Ratchet: {lhs} <= {rhs} ({what}), every metric", "", f"| Workload | Metric | {lhs} median | {rhs} median | {lhs} p90 | {rhs} p90 | verdict |", "|---|---|---:|---:|---:|---:|---|"]
        fails = 0
        for wl, cache in keys:
            for metric in ordered:
                if metric not in RATCHET and not metric.startswith("tool_ms:"):
                    continue
                variant = "mem" if metric.startswith(("heap", "sys")) else "plain"
                g = c.get((wl, cache, lhs, variant), {}).get(metric)
                t = c.get((wl, cache, rhs, variant), {}).get(metric)
                if not g or not t:
                    continue
                gm, tm, g9, t9 = statistics.median(g), statistics.median(t), q(g, .9), q(t, .9)
                bad = gm > tm
                warn = g9 > t9
                fails += bad
                verdict.append(f"| {wl}/{cache} | {metric} | {fmt(metric, gm)} | {fmt(metric, tm)} | {fmt(metric, g9)} | {fmt(metric, t9)} | {('FAIL (median)' if gate else 'worse') if bad else (('pass, p90 worse' if gate else 'not worse, p90 worse') if warn else ('pass' if gate else 'not worse'))} |")
        verdict += ["", f"Median misses ({lhs} vs {rhs}): {fails}." if gate else f"Metrics where {lhs} is worse than {rhs}: {fails}.", ""]
        if gate:
            misses += fails
    noisy = spread(c, keys, sides, ordered)
    spreadlines = ["", "## Relative spread (p90-p50)/p50 above 5% (go and ts sides, ratchet metrics)", ""] + (noisy or ["none: every ratchet metric of every cell is within 5%."]) + [""]
    text = "\n".join(lines + verdict + spreadlines) + "\n"
    (pathlib.Path(a.dir) / "summary.md").write_text(text)
    print(text)
    return 1 if mismatches or misses else 0


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    r = sub.add_parser("run")
    r.add_argument("--out", required=True)
    r.add_argument("--sides", default="go,ts,pi")
    r.add_argument("--workloads", default="all")
    r.add_argument("--samples", type=int, default=15)
    r.add_argument("--sessions", type=int, default=60)
    r.add_argument("--cpus", default=os.environ.get("PH_PERF_CPUS", ""))
    r.add_argument("--keep-going", action="store_true")
    r.add_argument("--first", type=int, default=0, help="first run index (a top-up run appends rows from this index on)")
    r.add_argument("--seed", default="b", help="workload of the three unmeasured warm-up launches (b exercises the tools)")
    s = sub.add_parser("summarize")
    s.add_argument("dir")
    s.add_argument("--vs")
    a = ap.parse_args()
    if a.cmd == "run":
        if a.samples < 15:
            print("note: fewer than 15 samples per cell is a smoke run, not a baseline", file=sys.stderr)
        return cmd_run(a)
    return cmd_summarize(a)


if __name__ == "__main__":
    sys.exit(main())
