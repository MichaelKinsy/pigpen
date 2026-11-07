#!/usr/bin/env python3
"""LangSmith extension perf basket: Go port vs the TypeScript original, same host, hermetic and offline.

Runs inside the xd6702 container (see run.sh). Python 3 standard library only.

  perf.py run --out DIR [--sides go,ts,pi] [--workloads a,b,c,...] [--samples 15] [--baselines]
  perf.py summarize DIR [--override DIR2]

The method (what each metric is and how it is read) is README.md in this directory; this file is its implementation.
"""
import argparse, hashlib, json, math, os, pathlib, random, shutil, signal, statistics, struct, subprocess, sys, threading
import time, urllib.request, zlib

HERE = pathlib.Path(__file__).resolve().parent
PKG = HERE.parents[1]                      # components/langsmith
GOEXT = pathlib.Path(os.environ.get("LS_PERF_GOEXT", PKG / "extensions" / "langsmith"))
ORACLE = pathlib.Path(os.environ.get("LS_PERF_ORACLE", "/home/kinsy/ls-work/oracle-run"))   # dist/ + node_modules (langsmith 0.10.5)
TOOLS = PKG / "port" / "tools"             # mockls
SCRATCH = pathlib.Path(os.environ.get("LS_PERF_SCRATCH", "/home/kinsy/ls-work/scratch/LsPerf"))
WS = SCRATCH / "ws"                        # fixed workspace path: run metadata carries this cwd
SETTLE_S = 0.5                             # idle time after "ready" before the ready snapshot
IDLE_S = 2.5                               # a/b: the idle snapshot this long after ready
G_IDLE_S = 10.0                            # g: the idle snapshot this long after the post-session one
TICK_MS = 1000 // os.sysconf("SC_CLK_TCK")
# side -> (host, extension language). nopig/nopi are baselines: the same host with no extension at all.
SIDES = {"go": ("pig", "go"), "ts": ("pig", "ts"), "pi": ("pi", "ts"), "nopig": ("pig", None), "nopi": ("pi", None)}
MAIN_SIDES = ["go", "ts", "pi"]
WORKLOADS = ["a", "b", "c", "d10", "d100", "d1000", "e", "f0", "f200", "f500", "g"]
COLD_WORKLOADS = {"a", "b"}
BASELINE_WORKLOADS = {"a", "c"}
NO_MEM_WORKLOADS = {"f0", "f200", "f500"}
TAGS = ("ready", "post", "end")
GATE = {"busy": None, "met": None}         # the last quiet-machine gate reading (--max-busy), copied into every row


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


def rollup(pid):
    out = {}
    for line in pathlib.Path(f"/proc/{pid}/smaps_rollup").read_text().splitlines():
        key, _, rest = line.partition(":")
        if key in ("Rss", "Pss", "Anonymous"):
            out[key.lower()] = int(rest.split()[0]) * 1024
    return out


def proc_mem(pid):
    """Rss, Pss, Anonymous (bytes) from smaps_rollup; VmHWM (peak RSS) and threads from status; cpu ms from stat; open fds."""
    out = rollup(pid)
    for line in pathlib.Path(f"/proc/{pid}/status").read_text().splitlines():
        if line.startswith("VmHWM:"):
            out["hwm"] = int(line.split()[1]) * 1024
        elif line.startswith("Threads:"):
            out["threads"] = int(line.split()[1])
    _, out["cpu_ms"], out["cchild_ms"] = stat_fields(pid)
    out["fds"] = len(os.listdir(f"/proc/{pid}/fd"))
    return out


def sum_mem(rows):
    keys = ("rss", "pss", "anonymous", "hwm", "threads", "cpu_ms", "cchild_ms", "fds")
    return {k: sum(r.get(k, 0) for r in rows) for k in keys}


def cpu_clock():
    """(busy jiffies, total jiffies, cpus) of the whole machine from /proc/stat (busy = everything but idle and iowait)."""
    with open("/proc/stat") as f:
        first = f.readline().split()
        n = sum(1 for line in f if line.startswith("cpu") and line[3].isdigit())
    vals = list(map(int, first[1:]))
    return sum(vals) - vals[3] - vals[4], sum(vals), n


def busy_cores(a, b):
    """Average number of busy CPUs of the machine between two cpu_clock() readings."""
    return (b[0] - a[0]) / max(1, b[1] - a[1]) * b[2]


def wait_quiet_machine(max_busy, max_wait_s):
    """Between samples: wait until the other lanes use at most max_busy CPUs (nothing of ours is running), at most max_wait_s.
    Returns the last reading (busy CPUs) and whether the gate was met."""
    deadline, last = time.monotonic() + max_wait_s, None
    while True:
        a = cpu_clock()
        time.sleep(3.0)
        last = busy_cores(a, cpu_clock())
        if last <= max_busy:
            return last, True
        if time.monotonic() > deadline:
            return last, False


class Peaks(threading.Thread):
    """Polls smaps_rollup of the host and extension processes while a workload runs; keeps the maxima.
    The kernel's VmHWM is the exact peak RSS of one process; this is the only way to get a peak PSS or Anonymous."""

    def __init__(self, host_pid, ext_pids, own, interval=0.1):
        super().__init__(daemon=True)
        self.host_pid, self.ext_pids, self.own, self.interval = host_pid, list(ext_pids), own, interval
        self.stop_ev = threading.Event()
        self.peak = {}

    def sample(self):
        try:
            host = rollup(self.host_pid)
            ext = [rollup(p) for p in self.ext_pids] if not self.own else []
        except OSError:
            return
        if self.own:
            ext_sum = dict(host)
        else:
            ext_sum = {k: sum(r.get(k, 0) for r in ext) for k in ("rss", "pss", "anonymous")}
        tree = {k: host.get(k, 0) + (0 if self.own else ext_sum.get(k, 0)) for k in ("rss", "pss", "anonymous")}
        for name, src in (("ext", ext_sum), ("tree", tree)):
            for k in ("rss", "pss", "anonymous"):
                key = f"peak_{name}_{k}"
                self.peak[key] = max(self.peak.get(key, 0), src.get(k, 0))

    def run(self):
        while not self.stop_ev.is_set():
            self.sample()
            self.stop_ev.wait(self.interval)

    def stop(self):
        self.stop_ev.set()
        self.join(2)
        self.sample()
        return self.peak


# ---------------------------------------------------------------- host

class Host:
    """One host process (pig or pi) in RPC mode with a reader thread that timestamps every stdout line."""

    def __init__(self, argv, cwd, env, cpus=None):
        if cpus:
            argv = ["taskset", "-c", ",".join(map(str, cpus))] + argv
        self.t0 = time.perf_counter_ns()
        self.p = subprocess.Popen(argv, cwd=cwd, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                  stderr=subprocess.PIPE, start_new_session=True)
        self.events, self.cond, self.stderr = [], threading.Condition(), bytearray()
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

    def close(self, timeout=30):
        """Close stdin, wait for exit; returns (returncode, rusage of the reaped tree, killed)."""
        try:
            self.p.stdin.close()
        except OSError:
            pass
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            pid, status, ru = os.wait4(self.p.pid, os.WNOHANG)
            if pid:
                return os.waitstatus_to_exitcode(status), ru, False
            time.sleep(0.005)
        os.killpg(self.p.pid, signal.SIGKILL)
        pid, status, ru = os.wait4(self.p.pid, 0)
        return -9, ru, True


# ---------------------------------------------------------------- environment, snapshot, helpers

def host_env(root, mem_dir=None, ls_url=None, prof_dir=None):
    e = {"PATH": os.environ["PATH"], "HOME": str(root / "home"), "TMPDIR": str(root / "tmp"), "LANG": "C.UTF-8", "TERM": "dumb",
         "NO_COLOR": "1", "PIG_HOME": str(root / "pighome"), "PIG_CODING_AGENT_DIR": str(root / "agent"),
         "PI_CODING_AGENT_DIR": str(root / "agent"), "PI_SKIP_VERSION_CHECK": "1", "PI_OFFLINE": "1", "PI_TELEMETRY": "0",
         "PIG_OFFLINE": "1", "GIT_TERMINAL_PROMPT": "0"}
    for k in ("GOROOT", "GOCACHE", "GOMODCACHE", "GOPROXY", "GOFLAGS", "PIG_SDK_DIR"):
        if k in os.environ:
            e[k] = os.environ[k]
    if mem_dir:
        e["LS_PERF_MEM_DIR"] = str(mem_dir)
    if prof_dir:
        e["LS_PERF_PPROF"] = str(prof_dir)
    if ls_url:   # tracing on: what the extension reads (config.ts getConfig: TRACE_TO_LANGSMITH, LANGSMITH_PI_*)
        e.update({"TRACE_TO_LANGSMITH": "true", "LANGSMITH_PI_API_KEY": "lsv2_pt_fake_perf_key", "LANGSMITH_PI_ENDPOINT": ls_url})
    return e


def snapshot_sources(snap):
    """Copy the extension under test once per invocation: another lane's sync cannot change it mid-run.
    plain/ = the shipped sources; mem/ = the same plus the measurement-only reporters (port/perf/mem)."""
    shutil.rmtree(snap, ignore_errors=True)
    for variant in ("plain", "mem"):
        dst = snap / variant / "langsmith"            # a direct Go extension directory is named by its registered identity
        shutil.copytree(GOEXT, dst, ignore=shutil.ignore_patterns("*_test.go", "testdata"))
        if variant == "mem":
            shutil.copy(HERE / "mem" / "zz_perf_mem.go", dst / "zz_perf_mem.go")
        o = snap / variant / "oracle"
        shutil.copytree(ORACLE / "dist", o / "dist", ignore=shutil.ignore_patterns("*.d.ts"))
        shutil.copy(ORACLE / "package.json", o / "package.json")
        os.symlink(ORACLE / "node_modules", o / "node_modules")
        if variant == "mem":
            shutil.copy(HERE / "mem" / "node-mem.js", o / "dist" / "node-mem.js")
            (o / "dist" / "perf-entry.js").write_text('import "./node-mem.js";\nimport ext from "./index.js";\nexport default ext;\n')


def ext_path(snap, lang, variant):
    if lang == "go":
        return snap / variant / "langsmith"
    return snap / variant / "oracle" / "dist" / ("perf-entry.js" if variant == "mem" else "index.js")


class Svc:
    """A helper process (mockls, fakellm) whose first stdout line announces its URL; lifetime = stdin."""

    def __init__(self, argv, cpus):
        argv = ["taskset", "-c", ",".join(map(str, cpus))] + argv if cpus else argv
        self.p = subprocess.Popen(argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        line = self.p.stdout.readline().decode().strip()
        self.url = line.split()[-1] if line else ""
        if not self.url.startswith("http"):
            raise RuntimeError(f"{argv[0]} did not announce a URL: {line!r}")

    def stats(self, path):
        base = self.url.rsplit("/v1", 1)[0]
        with urllib.request.urlopen(base + path, timeout=5) as r:
            return json.loads(r.read())

    def stop(self):
        try:
            self.p.stdin.close()
        except OSError:
            pass
        try:
            self.p.wait(5)
        except subprocess.TimeoutExpired:
            self.p.kill()
            self.p.wait()


def start_llm(llmbin, turns, root, cpus):
    script = root / "turns.json"
    script.write_text(json.dumps(turns))
    return Svc([str(llmbin), "--script", str(script)], cpus)


def start_mock(mockbin, root, cfg, cpus):
    argv = [str(mockbin), "-addr", "127.0.0.1:0", "-stdin-lifetime"]
    if cfg:
        (root / "mockls.json").write_text(json.dumps(cfg))
        argv += ["-config", str(root / "mockls.json")]
    return Svc(argv, cpus)


def write_config(root, url, context_window=10_000_000):
    (root / "agent").mkdir(parents=True, exist_ok=True)
    if url:
        models = {"providers": {"pf": {"baseUrl": url, "api": "openai-completions", "apiKey": "pf-key", "models": [{
            "id": "pf-1", "name": "pf-1", "reasoning": False, "input": ["text", "image"], "contextWindow": context_window, "maxTokens": 4096,
            "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}]}}}
        (root / "agent" / "models.json").write_text(json.dumps(models))
    (root / "agent" / "settings.json").write_text(json.dumps({"lastChangelogVersion": "9999.0.0", "theme": "dark"}))


# ---------------------------------------------------------------- workloads (deterministic data and model scripts)

WORDS = ("alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima mike november oscar papa quebec romeo sierra "
         "tango uniform victor whiskey xray yankee zulu parser buffer socket kernel render cursor ticket vector module bundle "
         "cache router shader tensor planner reducer scanner loader writer reader").split()


def prose(rng, n):
    return " ".join(rng.choice(WORDS) for _ in range(n))


def prose_bytes(rng, nbytes):
    words, size = [], 0
    while size < nbytes:
        w = rng.choice(WORDS)
        words.append(w)
        size += len(w) + 1
    return " ".join(words)[:nbytes]


def png_noise(rng, side):
    """A valid RGB PNG of incompressible noise (about 3*side*side bytes)."""
    raw = b"".join(b"\x00" + rng.randbytes(side * 3) for _ in range(side))

    def chunk(tag, data):
        return struct.pack(">I", len(data)) + tag + data + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", side, side, 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(raw, 1)) + chunk(b"IEND", b""))


def make_fixtures():
    """Deterministic fixture files, generated once under the scratch tree and copied into every workspace."""
    d = SCRATCH / "fixtures"
    if (d / ".done").exists():
        return d
    shutil.rmtree(d, ignore_errors=True)
    d.mkdir(parents=True)
    rng = random.Random(7)
    for k in range(5):
        (d / f"small-{k}.txt").write_text(f"FIXTURE-SMALL-{k}\n" + "\n".join(prose(rng, 12) for _ in range(12)) + "\n")
    lines, size = ["FIXTURE-BIG"], 0
    while size < 48_000:
        lines.append(prose(rng, 12))
        size += len(lines[-1]) + 1
    (d / "big-48k.txt").write_text("\n".join(lines) + "\n")
    for k in range(3):
        (d / f"img-{k}.png").write_bytes(png_noise(rng, 180))
    (d / ".done").write_text("1")
    return d


def tool_call(i):
    """The i-th tool call of the mixed workload: 60% read (in-process), 25% bash (a subprocess), 15% write."""
    k = i % 20
    if k < 12:
        return "read", {"path": f"fixtures/small-{i % 5}.txt"}
    if k < 17:
        return "bash", {"command": f"echo step-{i}"}
    return "write", {"path": f"out/o-{i % 7}.txt", "content": f"payload {i} " * 20}


def tool_turn(i, name=None, args=None, text=None):
    if name is None:
        name, args = tool_call(i)
    return {"text": text if text is not None else f"Step {i}.", "toolCalls": [{"name": name, "arguments": args}]}


def session_turns(calls_per_prompt, prompts):
    turns, i = [], 0
    for _ in range(prompts):
        for _ in range(calls_per_prompt):
            turns.append(tool_turn(i))
            i += 1
        turns.append({"text": f"Batch done after {i} calls."})
    return turns


def large_turns():
    """Workload e: 100 model requests (99 tool turns, one text turn) -> a 200-message conversation. Six 48 KB tool outputs
    (the largest the host's read tool returns: it truncates at 50 KB), three image reads (180x180 noise PNG, about 97 KB each),
    two assistant messages of 100 KB; the provider payload at the last request is about 1 MB."""
    rng = random.Random(99)
    turns = []
    for t in range(99):
        if t in (10, 25, 40, 55, 70, 85):
            turns.append(tool_turn(t, "read", {"path": "fixtures/big-48k.txt"}))
        elif t in (60, 75, 90):
            turns.append(tool_turn(t, "read", {"path": f"fixtures/img-{(t - 60) // 15}.png"}))
        elif t in (30, 80):
            n, a = tool_call(t)
            turns.append(tool_turn(t, n, a, text=prose_bytes(rng, 100_000)))
        else:
            turns.append(tool_turn(t))
    return turns + [{"text": "All done."}]


def workload_spec(w):
    """What a workload runs: model script, prompts, whether tracing is on, mockls fault config, what follows the prompts."""
    s = {"turns": None, "prompts": [], "tracing": True, "mock": None, "kind": "run"}
    if w == "a":
        s["tracing"], s["kind"] = False, "idle"
    elif w == "b":
        s["kind"] = "idle"
    elif w == "c":
        s["turns"], s["prompts"] = [{"text": "Short answer."}], ["Say something short."]
    elif w in ("d10", "d100", "d1000"):
        n = int(w[1:])
        s["turns"], s["prompts"] = session_turns(10, n // 10), [f"Continue the work, batch {k}." for k in range(n // 10)]
    elif w == "e":
        s["turns"], s["prompts"] = large_turns(), ["Work through the whole task."]
    elif w in ("f0", "f200", "f500"):
        s["turns"] = [tool_turn(0, "read", {"path": "fixtures/small-0.txt"}), tool_turn(1, "bash", {"command": "echo step-1"}),
                      tool_turn(2, "write", {"path": "out/o-0.txt", "content": "payload"}), {"text": "Done."}]
        s["prompts"], s["kind"] = ["Do three small things."], "shutdown"
        if w == "f200":
            s["mock"] = {"faults": [{"route": "*", "delayMs": 200}]}
        if w == "f500":
            s["mock"] = {"faults": [{"route": r, "status": 500} for r in ("multipart", "batch", "runs", "patch")]}
    elif w == "g":
        s["turns"], s["prompts"], s["kind"] = session_turns(2, 20), [f"Run number {k}." for k in range(20)], "steady"
    return s


# ---------------------------------------------------------------- one run

def settle_pids(host, host_kind, has_ext):
    """The extension process(es): descendants of a pig host; the pi host process itself is its own extension host."""
    if not has_ext:
        return []
    if host_kind == "pi":
        return [host.p.pid]
    return descendants(host.p.pid)


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


def text_of(result):
    return "".join(c.get("text", "") for c in (result or {}).get("content", []) if isinstance(c, dict))


def check_result(name, args, ev):
    """A tool call that did nothing would time a trivial path: each result must carry what the call was meant to produce."""
    result = ev.get("result") or {}
    text = text_of(result)
    path = (args or {}).get("path", "")
    if name == "read":
        if path.endswith(".png"):
            if not any(isinstance(c, dict) and c.get("type") == "image" for c in result.get("content", [])):
                raise RuntimeError(f"read {path}: no image part in the result: {json.dumps(result)[:300]}")
        elif path.endswith("big-48k.txt"):
            if "FIXTURE-BIG" not in text or len(text) < 40_000:
                raise RuntimeError(f"read {path}: truncated or empty result ({len(text)} chars)")
        elif "FIXTURE-SMALL" not in text:
            raise RuntimeError(f"read {path}: result lacks the fixture marker: {text[:200]!r}")
    elif name == "bash":
        if "step-" not in text:
            raise RuntimeError(f"bash: result lacks the echoed marker: {text[:200]!r}")


def drive_prompt(host, prompt, cursor):
    """One prompt: tool latencies, time to the first tool start, prompt sent -> final agent_end."""
    out = {"tools": []}
    out["sent_unix_ns"] = time.time_ns()
    sent = host.send({"id": "prompt", "type": "prompt", "message": prompt})
    i, t, ev = host.wait(lambda e: e.get("id") == "prompt", start=cursor, timeout=60)
    if not ev.get("success"):
        raise RuntimeError(f"prompt rejected: {ev}")
    pos, starts, first_tool = i, {}, None
    while True:
        pos, t, ev = host.wait(lambda e: e.get("type") in ("agent_end", "tool_execution_start", "tool_execution_end"), start=pos, timeout=3600)
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
                check_result(began[1], began[2] if isinstance(began[2], dict) else {}, ev)
                out["tools"].append({"tool": began[1], "ms": (t - began[0]) / 1e6})
        else:
            out["turn_ms"], out["end_t"], out["sent_t"] = (t - sent) / 1e6, t, sent
            break
    out["to_first_tool_ms"] = first_tool
    return out


def wait_quiet(mock, quiet_s=0.6, cap_s=180):
    """Wait until mockls has seen no new request for quiet_s; returns the approximate ms from the call to the last request."""
    t0 = time.monotonic()
    last, changed = mock.stats("/__mockls/stats")["requests"], time.monotonic()
    while time.monotonic() - t0 < cap_s:
        time.sleep(0.05)
        n = mock.stats("/__mockls/stats")["requests"]
        if n != last:
            last, changed = n, time.monotonic()
        elif time.monotonic() - changed >= quiet_s:
            return max(0.0, (changed - t0) * 1000)
    raise RuntimeError("mockls never went quiet")


def ls_row(stats):
    by = stats.get("byRoute", {})
    return {"requests": stats["requests"], "ingest_requests": sum(by.get(k, 0) for k in ("multipart", "batch", "runs", "patch")),
            "multipart": by.get("multipart", 0), "info": by.get("info", 0), "bytes": stats.get("bytes", 0),
            "decoded_bytes": stats.get("decodedBytes", 0), "by_route": by}


def cpu_of(pid):
    return stat_fields(pid)[1]


def shutdown_phase(row, host, pids, mock, end_t):
    """Workload f: close stdin right after the run's agent_end (batches still queued in the extension) and time the exit.
    Returns (returncode, rusage, killed) of the host."""
    row["ls_before_close"] = ls_row(mock.stats("/__mockls/stats")) if mock else {"requests": 0}
    ext_only = [p for p in pids if p != host.p.pid]
    cpu0 = sum(cpu_of(p) for p in ext_only)
    t_close = time.perf_counter_ns()
    row["end_to_close_ms"] = (t_close - end_t) / 1e6
    try:
        host.p.stdin.close()
    except OSError:
        pass
    deadline = time.monotonic() + 300
    host_done = ext_gone = rc = ru = None
    cpu_last = cpu0
    while time.monotonic() < deadline:
        if host_done is None:
            pid, status, ru_ = os.wait4(host.p.pid, os.WNOHANG)
            if pid:
                host_done, rc, ru = (time.perf_counter_ns() - t_close) / 1e6, os.waitstatus_to_exitcode(status), ru_
        if ext_only and ext_gone is None:
            cpu, alive = 0, False
            for p in ext_only:
                try:
                    st = pathlib.Path(f"/proc/{p}/stat").read_text()
                    if st[st.rindex(")") + 2] != "Z":
                        alive = True
                        cpu += cpu_of(p)
                except (OSError, ValueError):
                    pass
            if alive:
                cpu_last = max(cpu_last, cpu)
            else:
                ext_gone = (time.perf_counter_ns() - t_close) / 1e6
        if host_done is not None and (not ext_only or ext_gone is not None):
            break
        time.sleep(0.002)
    killed = False
    if host_done is None:
        os.killpg(host.p.pid, signal.SIGKILL)
        pid, status, ru = os.wait4(host.p.pid, 0)
        host_done, rc, killed = (time.perf_counter_ns() - t_close) / 1e6, -9, True
    row["shutdown_ms"] = host_done
    row["ext_gone_ms"] = ext_gone if ext_gone is not None else host_done
    row["shutdown_cpu_ext_ms"] = max(0, cpu_last - cpu0) if ext_only else None
    row["shutdown_cpu_host_ms"] = (ru.ru_utime + ru.ru_stime) * 1000
    return rc, ru, killed


def run_one(cfg, side, variant, workload, cache, idx, snap, bins, warm_root, cpus):
    host_kind, lang = SIDES[side]
    has_ext = lang is not None
    binary = os.environ["PIGEQ_PIG"] if host_kind == "pig" else os.environ["PIGEQ_PI"]
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
    spec = workload_spec(workload)
    tracing = spec["tracing"] and has_ext
    shutil.rmtree(WS, ignore_errors=True)
    work = WS / "work"
    work.mkdir(parents=True)
    shutil.copytree(make_fixtures(), work / "fixtures")
    llm = mock = host = closed = None
    row = {"side": side, "variant": variant, "workload": workload, "cache": cache, "run": idx, "ok": False, "load1": os.getloadavg()[0], "gate_busy": GATE["busy"], "gate_met": GATE["met"]}
    clock0 = cpu_clock()
    try:
        llm = start_llm(bins["fakellm"], spec["turns"], root, cpus["helper"]) if spec["turns"] else None
        mock = start_mock(bins["mockls"], root, spec["mock"], cpus["helper"]) if tracing else None
        write_config(root, llm.url if llm else None)
        args = ["--mode", "rpc", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-context-files", "--no-extensions", "--offline",
                "--no-session"]
        if llm:
            args += ["--provider", "pf", "--model", "pf-1"]
        if has_ext:
            args += ["-e", str(ext_path(snap, lang, variant))]
        prof_dir = None
        if cfg.profile and lang == "go" and variant == "mem":     # CPU and allocation profile of the Go extension (README: Profiling)
            prof_dir = pathlib.Path(cfg.profile) / f"{workload}-{idx}"
            prof_dir.mkdir(parents=True, exist_ok=True)
        host = Host([binary] + args, work, host_env(root, mem_dir, mock.url if mock else None, prof_dir), cpus["tree"])
        host.send({"id": "ready", "type": "get_state"})
        i, t, ev = host.wait(lambda e: e.get("id") == "ready", timeout=300)
        row["startup_ms"] = (t - host.t0) / 1e6
        if not ev.get("success"):
            raise RuntimeError(f"get_state failed: {ev}")
        time.sleep(SETTLE_S)
        pids = settle_pids(host, host_kind, has_ext)
        row["ext_count"] = len(pids)
        if host_kind == "pig" and has_ext and not pids:
            raise RuntimeError("no extension process under the pig host (the extension did not load?) stderr: " + host.stderr[-800:].decode("utf8", "replace"))
        if pids:
            row["ext_cmd"] = pathlib.Path(f"/proc/{pids[0]}/cmdline").read_bytes().replace(b"\0", b" ")[:160].decode("utf8", "replace")
        row["ready"] = snapshot(host, pids, variant, mem_dir, "ready")
        peaks = Peaks(host.p.pid, pids, host_kind == "pi" or not pids)
        peaks.start()
        cursor = len(host.events)
        first_sent_t = end_t = None
        for k, prompt in enumerate(spec["prompts"]):
            r = drive_prompt(host, prompt, cursor)
            cursor = len(host.events)
            row.setdefault("tools", []).extend(r["tools"])
            row["turn_ms"] = row.get("turn_ms", 0) + r["turn_ms"]
            if k == 0:
                row["to_first_tool_ms"], first_sent_unix, first_sent_t = r["to_first_tool_ms"], r["sent_unix_ns"], r["sent_t"]
                row["first_turn_ms"] = r["turn_ms"]
            end_t = r["end_t"]
        if end_t is not None:
            row["work_ms"] = (end_t - first_sent_t) / 1e6
        if llm:
            ls = llm.stats("/stats")
            row["llm"] = ls
            if first_sent_t is not None and ls.get("firstRequestUnixNs"):
                row["to_first_request_ms"] = (ls["firstRequestUnixNs"] - first_sent_unix) / 1e6
            if ls["overrun"]:
                row["note"] = f"model script overrun by {ls['overrun']} requests"
        kind = spec["kind"]
        if kind == "idle":
            time.sleep(IDLE_S - SETTLE_S)
        elif kind == "shutdown":
            closed = shutdown_phase(row, host, pids, mock, end_t)
        elif mock:
            row["flush_tail_ms"] = wait_quiet(mock)
        if kind == "steady":
            time.sleep(1.0)
            row["post"] = snapshot(host, settle_pids(host, host_kind, has_ext), variant, mem_dir, "post")
            time.sleep(G_IDLE_S)
        row["peak"] = peaks.stop()
        if kind != "shutdown":
            pids = settle_pids(host, host_kind, has_ext)
            row["end"] = snapshot(host, pids, variant, mem_dir, "end")
            row["end"]["ext_count"] = len(pids)
        if prof_dir and pids:
            for p in pids:
                os.kill(p, signal.SIGUSR1)
            deadline = time.monotonic() + 30
            while time.monotonic() < deadline and not list(prof_dir.glob("*.done")):
                time.sleep(0.05)
        row["ok"] = True
    except Exception as exc:                                         # recorded, not retried; the caller aborts on a bad row
        row["error"] = f"{type(exc).__name__}: {exc}"
    finally:
        if host is not None:
            rc, ru, killed = closed if closed else host.close(timeout=60)
            row["returncode"], row["cpu_wait4_ms"], row["killed"] = rc, (ru.ru_utime + ru.ru_stime) * 1000, killed
            row["stderr_bytes"] = len(host.stderr)
            row["stderr_errors"] = bytes(host.stderr).count(b"rror")
            if host.stderr and (not row["ok"] or workload == "f500"):
                row["stderr_tail"] = host.stderr[-600:].decode("utf8", "replace")
            row["wall_ms"] = (time.perf_counter_ns() - host.t0) / 1e6
            row["busy_cores_run"] = busy_cores(clock0, cpu_clock())   # whole machine during the sample, ours (about 3) included
        if mock:
            try:
                row["ls"] = ls_row(mock.stats("/__mockls/stats"))
            except Exception as exc:
                row.setdefault("error", f"mockls stats: {exc}")
            mock.stop()
        if llm:
            llm.stop()
    if cold:
        shutil.rmtree(root, ignore_errors=True)
    if row["ok"] and row["returncode"] != 0 and not (workload == "f500" and row["killed"]):
        row["ok"], row["error"] = False, f"host exit code {row['returncode']}"
    if row["ok"] and row["stderr_errors"] and workload != "f500":
        row["ok"], row["error"] = False, "stderr: " + host.stderr[-500:].decode("utf8", "replace")
    return row


# ---------------------------------------------------------------- orchestration

def pick_cpus(spec, want=3):
    """helper CPU (driver, mockls, fakellm) + tree CPUs (host and extension). Auto: the idlest physical cores of the affinity set."""
    allowed = sorted(os.sched_getaffinity(0))
    if spec:
        chosen = [int(c) for c in spec.split(",")]
    else:
        def snap():
            out = {}
            for line in open("/proc/stat"):
                if line.startswith("cpu") and line[3].isdigit():
                    vals = list(map(int, line.split()[1:]))
                    out[int(line.split()[0][3:])] = (vals[3] + vals[4], sum(vals))
            return out
        a = snap()
        time.sleep(1.0)
        b = snap()
        idle = {c: (b[c][0] - a[c][0]) / max(1, b[c][1] - a[c][1]) for c in allowed if c in a and c in b}

        def sibs(c):
            try:
                return [int(x) for x in pathlib.Path(f"/sys/devices/system/cpu/cpu{c}/topology/thread_siblings_list").read_text().strip().replace("-", ",").split(",")]
            except (OSError, ValueError):
                return [c]
        score = {c: min(idle.get(s, 0.0) for s in sibs(c)) for c in idle}
        chosen, used = [], set()
        for c in sorted(score, key=lambda c: -score[c]):
            if c in used:
                continue
            chosen.append(c)
            used.update(sibs(c))
            if len(chosen) == want:
                break
        if len(chosen) < want:
            chosen = allowed[-want:]
    os.sched_setaffinity(0, [chosen[0]])
    return {"helper": [chosen[0]], "tree": chosen[1:], "all": chosen}


def build_helpers():
    bin_dir = SCRATCH / "bin"
    bin_dir.mkdir(parents=True, exist_ok=True)
    env = dict(os.environ, GOWORK="off", CGO_ENABLED="0")
    subprocess.run(["go", "build", "-trimpath", "-ldflags=-s -w", "-o", str(bin_dir / "fakellm"), "."], cwd=HERE / "fakellm", env=env, check=True)
    subprocess.run(["go", "build", "-trimpath", "-ldflags=-s -w", "-o", str(bin_dir / "mockls"), "./cmd/mockls"], cwd=TOOLS, env=dict(env, GOFLAGS="-mod=mod"), check=True)
    return {"fakellm": bin_dir / "fakellm", "mockls": bin_dir / "mockls"}


def versions():
    def run(cmd):
        try:
            return subprocess.check_output(cmd, text=True, stderr=subprocess.STDOUT, env=host_env(SCRATCH / "versions")).strip()
        except Exception as exc:
            return f"unavailable: {exc}"
    (SCRATCH / "versions" / "home").mkdir(parents=True, exist_ok=True)
    langsmith = json.loads((ORACLE / "node_modules" / "langsmith" / "package.json").read_text())["version"]
    return {"pig": run([os.environ["PIGEQ_PIG"], "--version"]), "pi": run([os.environ["PIGEQ_PI"], "--version"]),
            "node": run(["node", "--version"]), "go": run(["go", "version"]), "langsmith_sdk": langsmith,
            "kernel": run(["uname", "-sr"]), "cpu": next((l.split(":", 1)[1].strip() for l in open("/proc/cpuinfo") if l.startswith("model name")), "?")}


def go_binary_report(probe_home):
    """Size of the extension binary pig built for the Go side, with and without symbols."""
    cells_ = sorted((probe_home / "cache" / "cells" / "go").glob("*/runner"), key=lambda p: p.stat().st_mtime)
    if not cells_:
        return {}
    binary = cells_[-1]
    info = {"runner_bytes": binary.stat().st_size, "sha256": hashlib.sha256(binary.read_bytes()).hexdigest()}
    if shutil.which("strip"):
        copy = probe_home.parent / "runner.stripped"
        shutil.copy(binary, copy)
        subprocess.run(["strip", "-s", str(copy)], check=False)
        info["stripped_bytes"] = copy.stat().st_size
    return info


def cmd_run(a):
    out = pathlib.Path(a.out).resolve()
    out.mkdir(parents=True, exist_ok=True)
    sides = a.sides.split(",")
    workloads = WORKLOADS if a.workloads == "all" else a.workloads.split(",")
    cpus = pick_cpus(a.cpus)
    bins = build_helpers()
    snap = SCRATCH / "snap"
    snapshot_sources(snap)
    make_fixtures()
    mem_samples = a.mem_samples if a.mem_samples is not None else a.samples
    go_sources = sorted((snap / "plain" / "langsmith").glob("*.go"))
    meta = {"started": time.strftime("%Y-%m-%dT%H:%M:%S%z"), "sides": sides, "workloads": workloads, "samples": a.samples, "mem_samples": mem_samples,
            "baselines": a.baselines, "cpus": cpus, "loadavg_start": os.getloadavg(), "versions": versions(), "settle_s": SETTLE_S,
            "args": sys.argv[1:], "extension_sources_sha256": hashlib.sha256(b"".join(p.read_bytes() for p in go_sources)).hexdigest(),
            "oracle_index_sha256": hashlib.sha256((ORACLE / "dist" / "index.js").read_bytes()).hexdigest()}
    baselines = []
    if a.baselines:
        if "go" in sides or "ts" in sides:
            baselines.append("nopig")
        if "pi" in sides:
            baselines.append("nopi")
    all_sides = sides + baselines
    log = (out / "samples.jsonl").open("a")
    warm = {(s, v): SCRATCH / "warm" / f"{s}-{v}" for s in all_sides for v in ("plain", "mem")}
    shutil.rmtree(SCRATCH / "warm", ignore_errors=True)
    shutil.rmtree(SCRATCH / "cold", ignore_errors=True)
    # Seed every warm home with three unmeasured launches (application caches populated: pig's Go cell, jiti, Node compile).
    for side in all_sides:
        for variant in (("plain",) if SIDES[side][1] is None else ("plain", "mem")):
            for k in range(3):
                seed = run_one(a, side, variant, a.seed, "warm", f"seed{k}", snap, bins, warm[(side, variant)], cpus)
                if not seed["ok"]:
                    raise SystemExit(f"warm seed failed without retry: {json.dumps(seed)[:1500]}")
    if "go" in sides:   # keep the cell binary pig built for the Go extension, for the size row
        probe = SCRATCH / "sizeprobe"
        shutil.rmtree(probe, ignore_errors=True)
        shutil.copytree(warm[("go", "plain")] / "pighome", probe / "pighome", symlinks=True)
        meta["go_binary"] = go_binary_report(probe / "pighome")
        shutil.rmtree(probe, ignore_errors=True)
    for workload in workloads:
        caches = ("warm", "cold") if workload in COLD_WORKLOADS else ("warm",)
        wsides = all_sides if workload in BASELINE_WORKLOADS else sides
        for cache in caches:
            for i in range(a.samples):
                order = wsides[i % len(wsides):] + wsides[:i % len(wsides)]
                for side in order:
                    if a.max_busy:
                        GATE["busy"], GATE["met"] = wait_quiet_machine(a.max_busy, a.max_wait)
                    if SIDES[side][1] is None or workload in NO_MEM_WORKLOADS or i >= mem_samples:
                        variants = ("plain",)
                    else:
                        variants = ("plain", "mem") if i % 2 == 0 else ("mem", "plain")
                    for variant in variants:
                        row = run_one(a, side, variant, workload, cache, i, snap, bins, warm[(side, variant)], cpus)
                        log.write(json.dumps(row) + "\n")
                        log.flush()
                        tag = f"{workload}/{cache} {side}/{variant} #{i}"
                        if not row["ok"]:
                            print("INVALID", tag, json.dumps(row)[:1500], file=sys.stderr, flush=True)
                            if not a.keep_going:
                                raise SystemExit(f"run failed without retry: {tag}")
                        else:
                            print("ok", tag, f"startup={row.get('startup_ms', 0):.0f}ms wall={row.get('wall_ms', 0):.0f}ms", flush=True)
    meta["finished"] = time.strftime("%Y-%m-%dT%H:%M:%S%z")
    meta["loadavg_end"] = os.getloadavg()
    (out / "meta.json").write_text(json.dumps(meta, indent=2) + "\n")
    shutil.rmtree(SCRATCH / "warm", ignore_errors=True)
    shutil.rmtree(SCRATCH / "cold", ignore_errors=True)
    shutil.rmtree(WS, ignore_errors=True)


# ---------------------------------------------------------------- summary

def q(values, frac):
    v = sorted(values)
    return v[math.ceil(frac * (len(v) - 1))] if v else None


def spread(values):
    """(p90 - p10) / median, in percent: the width of the middle 80 percent of the samples."""
    med = statistics.median(values)
    if len(values) < 3 or med == 0:
        return 0.0
    v = sorted(values)
    lo, hi = v[math.floor(0.1 * (len(v) - 1))], v[math.ceil(0.9 * (len(v) - 1))]
    return 100.0 * (hi - lo) / abs(med)


def metrics_of(row):
    """Flatten one row into {metric: value}; list metrics (per-tool latencies) are pooled by the caller."""
    m = {}
    for k in ("startup_ms", "first_turn_ms", "to_first_tool_ms", "to_first_request_ms", "turn_ms", "work_ms", "flush_tail_ms", "wall_ms",
              "shutdown_ms", "ext_gone_ms", "shutdown_cpu_ext_ms", "shutdown_cpu_host_ms", "end_to_close_ms", "cpu_wait4_ms"):
        if row.get(k) is not None:
            m[k] = row[k]
    for tag in TAGS:
        s = row.get(tag)
        if not s:
            continue
        for part in ("ext", "host"):
            for src, dst in (("rss", "rss"), ("pss", "pss"), ("anonymous", "anon"), ("hwm", "peak_rss"), ("cpu_ms", "cpu_ms"), ("cchild_ms", "cchild_ms"),
                             ("threads", "threads"), ("fds", "fds")):
                if src in s[part]:
                    m[f"{part}_{dst}_{tag}"] = s[part][src]
        rt = s.get("runtime")
        if rt:
            if rt[0]["runtime"] == "go":
                for state in ("asis", "gc"):
                    m[f"heap_used_{state}_{tag}"] = sum(r[state]["heap_alloc"] for r in rt)
                    m[f"heap_inuse_{state}_{tag}"] = sum(r[state]["heap_inuse"] for r in rt)
                    m[f"sys_{state}_{tag}"] = sum(r[state]["sys"] for r in rt)
                    m[f"goroutines_{state}_{tag}"] = sum(r[state]["goroutines"] for r in rt)
                m[f"total_alloc_{tag}"] = sum(r["asis"]["total_alloc"] for r in rt)
                m[f"mallocs_{tag}"] = sum(r["asis"]["mallocs"] for r in rt)
                m[f"num_gc_{tag}"] = sum(r["asis"]["num_gc"] for r in rt)
                m[f"gc_pause_ms_{tag}"] = sum(r["asis"]["pause_total_ns"] for r in rt) / 1e6
            else:
                for state in ("asis", "gc"):
                    m[f"heap_used_{state}_{tag}"] = sum(r[state]["heap_used"] for r in rt)
                    m[f"heap_inuse_{state}_{tag}"] = sum(r[state]["heap_total"] for r in rt)
                    m[f"sys_{state}_{tag}"] = sum(r[state]["rss"] for r in rt)
                    m[f"goroutines_{state}_{tag}"] = sum(r[state]["active_resources"] for r in rt)   # Node: active libuv resources (timers, sockets, ...)
                m[f"num_gc_{tag}"] = sum(r["asis"]["gc_events"] for r in rt)
                m[f"gc_pause_ms_{tag}"] = sum(r["asis"]["gc_pause_ms"] for r in rt)
        own = row["side"] in ("pi", "nopi")            # under Pi the extension runs inside the host process: count it once
        for src in ("rss", "pss"):
            m[f"tree_{src}_{tag}"] = s["host"][src] + (0 if own else s["ext"].get(src, 0))
        # Host and extension process CPU, each with the children it waited for (a cold Go build, tool subprocesses).
        m[f"cpu_total_ms_{tag}"] = s["host"]["cpu_ms"] + s["host"].get("cchild_ms", 0) + (0 if own else s["ext"].get("cpu_ms", 0) + s["ext"].get("cchild_ms", 0))
        if "ext_count" in s:
            m["ext_count"] = s["ext_count"]
    for k, v in (row.get("peak") or {}).items():
        m[k] = v
    if "llm" in row:
        m["llm_requests"] = row["llm"]["requests"]
        m["llm_bytes"] = row["llm"]["bytes"]
    for k, v in (row.get("ls") or {}).items():
        if k != "by_route":
            m[f"ls_{k}"] = v
    if row.get("ls_before_close"):
        m["ls_requests_before_close"] = row["ls_before_close"]["requests"]
    if "end" in row and "ready" in row:
        if row["end"]["ext"]:
            m["cpu_ext_workload_ms"] = row["end"]["ext"].get("cpu_ms", 0) - row["ready"]["ext"].get("cpu_ms", 0)
            m["ext_rss_growth"] = m["ext_rss_end"] - m["ext_rss_ready"]
            m["ext_anon_growth"] = m["ext_anon_end"] - m["ext_anon_ready"]
        m["cpu_total_workload_ms"] = m["cpu_total_ms_end"] - m["cpu_total_ms_ready"]
        for k in ("total_alloc", "mallocs", "num_gc", "gc_pause_ms"):
            if f"{k}_end" in m and f"{k}_ready" in m:
                m[f"{k}_workload"] = m[f"{k}_end"] - m[f"{k}_ready"]
        if "heap_used_asis_end" in m:
            m["heap_growth_asis"] = m["heap_used_asis_end"] - m["heap_used_asis_ready"]
            m["heap_growth_gc"] = m["heap_used_gc_end"] - m["heap_used_gc_ready"]
    if "post" in row and "end" in row and row["end"]["ext"]:
        m["ext_rss_idle_growth"] = row["end"]["ext"]["rss"] - row["post"]["ext"]["rss"]
        m["ext_anon_idle_growth"] = row["end"]["ext"]["anonymous"] - row["post"]["ext"]["anonymous"]
        m["cpu_idle_ms"] = m["cpu_total_ms_end"] - m["cpu_total_ms_post"]
        if "heap_used_asis_end" in m and "heap_used_asis_post" in m:
            m["heap_idle_growth_asis"] = m["heap_used_asis_end"] - m["heap_used_asis_post"]
    return {k: v for k, v in m.items() if v is not None}


def load_rows(path):
    rows = []
    for line in (pathlib.Path(path) / "samples.jsonl").read_text().splitlines():
        row = json.loads(line)
        if row.get("ok") and not str(row.get("run")).startswith("seed"):
            rows.append(row)
    return rows


def cells(rows):
    """(workload, cache, side, variant) -> {metric: [values]}; tool latencies pooled as tool_ms:<tool>."""
    out = {}
    for row in rows:
        cell = out.setdefault((row["workload"], row["cache"], row["side"], row["variant"]), {"_runs": 0})
        cell["_runs"] += 1
        for k, v in metrics_of(row).items():
            cell.setdefault(k, []).append(v)
        for t in row.get("tools", []):
            cell.setdefault(f"tool_ms:{t['tool']}", []).append(t["ms"])
    return out


MEM_PREFIXES = ("heap", "sys_", "total_alloc", "mallocs", "num_gc", "gc_pause", "goroutines")


def variant_of(metric):
    return "mem" if metric.startswith(MEM_PREFIXES) else "plain"


def fmt(metric, v):
    if v is None:
        return "-"
    if metric.endswith("_ms") or "_ms:" in metric or metric.startswith(("tool_ms", "gc_pause")):
        return f"{v:.1f}"
    if any(w in metric for w in ("rss", "pss", "anon", "heap", "sys_", "alloc", "bytes", "growth")) and "mallocs" not in metric:
        return f"{v / 1048576:.2f} MiB" if abs(v) >= 1024 else f"{v:.0f}"
    return f"{v:.1f}" if isinstance(v, float) and not float(v).is_integer() else f"{int(v)}"


# Metrics where Go must be <= TS-in-pig: the owner's basket. INFO rows are printed but not judged.
RATCHET = ["startup_ms", "first_turn_ms", "to_first_request_ms", "to_first_tool_ms", "turn_ms", "work_ms", "flush_tail_ms", "shutdown_ms", "ext_gone_ms",
           "cpu_total_ms_ready", "cpu_total_ms_end", "cpu_total_workload_ms", "cpu_ext_workload_ms", "cpu_idle_ms", "shutdown_cpu_ext_ms",
           "ext_rss_ready", "ext_pss_ready", "ext_anon_ready", "ext_rss_end", "ext_pss_end", "ext_anon_end", "ext_peak_rss_end",
           "peak_ext_rss", "peak_ext_pss", "peak_ext_anonymous", "tree_rss_end", "tree_pss_end", "peak_tree_rss", "peak_tree_pss", "ext_rss_growth",
           "ext_threads_end", "ext_fds_end", "heap_used_asis_end", "heap_used_gc_end", "heap_inuse_asis_end", "heap_inuse_gc_end",
           "sys_asis_end", "sys_gc_end", "gc_pause_ms_workload"]
INFO = ["ext_rss_idle_growth", "heap_growth_asis", "heap_growth_gc", "heap_idle_growth_asis", "num_gc_workload", "goroutines_gc_end", "total_alloc_workload", "mallocs_workload", "ext_count", "llm_requests", "ls_requests", "ls_ingest_requests", "ls_multipart", "ls_bytes",
        "ls_decoded_bytes", "ls_info", "ls_requests_before_close", "ext_anon_growth", "ext_anon_idle_growth", "goroutines_asis_end", "cpu_wait4_ms", "wall_ms",
        "host_rss_end", "host_pss_end", "host_cpu_ms_end", "shutdown_cpu_host_ms"]


def cell_text(metric, vals):
    if not vals:
        return "-"
    sp = spread(vals)
    return f"{fmt(metric, statistics.median(vals))} ±{sp:.0f}%{'!' if sp > 15 and not metric.startswith(('ls_', 'llm_', 'ext_count')) else ''}"


def ratio_text(a, b):
    if not a or not b:
        return "-"
    ma, mb = statistics.median(a), statistics.median(b)
    if mb == 0:
        return "-" if ma == 0 else "inf"
    return f"{ma / mb:.2f}"


def table(c, sides, wc, metrics):
    lines = []
    for wl, cache in wc:
        ns = {s: len(c.get((wl, cache, s, "plain"), {}).get("startup_ms", [])) for s in sides}
        cols = [s for s in sides if ns.get(s)]
        lines += [f"### Workload {wl}, {cache} (" + ", ".join(f"{s} n={ns[s]}" for s in cols) + ")", ""]
        ratios = {"go", "ts"} <= set(cols)
        lines += ["| Metric | " + " | ".join(cols) + (" | go/ts | go/pi |" if ratios else " |"), "|---|" + "---:|" * (len(cols) + (2 if ratios else 0))]
        for metric in metrics:
            v = {s: c.get((wl, cache, s, variant_of(metric)), {}).get(metric) for s in cols}
            if not any(v.values()):
                continue
            row = [cell_text(metric, v[s]) for s in cols]
            if ratios:
                row += [ratio_text(v["go"], v["ts"]), ratio_text(v["go"], v.get("pi")) if "pi" in cols else "-"]
            lines.append(f"| {metric} | " + " | ".join(row) + " |")
        lines.append("")
    return lines


def parity(c, keys, sides):
    """The same scripted workload must send the model the same number of requests on every side, and LangSmith comparable bytes."""
    out = []
    for wl, cache in keys:
        seen = {}
        for s in sides:
            vals = c.get((wl, cache, s, "plain"), {}).get("llm_requests")
            if vals:
                seen[s] = sorted(set(vals))
        if len({json.dumps(v) for v in seen.values()}) > 1:
            out.append(f"- PARITY MISMATCH {wl}/{cache} llm_requests: {seen}")
        for metric in ("ls_ingest_requests", "ls_decoded_bytes"):
            med = {s: statistics.median(c[(wl, cache, s, "plain")][metric]) for s in sides if c.get((wl, cache, s, "plain"), {}).get(metric)}
            if len(med) > 1 and min(med.values()) > 0 and max(med.values()) / min(med.values()) > 1.05 and wl != "f500":
                out.append(f"- NOTE {wl}/{cache} {metric} differs by more than 5%: " + ", ".join(f"{s}={v:.0f}" for s, v in med.items()))
    return out


def cmd_summarize(a):
    rows = load_rows(a.dir)
    if a.override:
        over = load_rows(a.override)
        replaced = {r["workload"] for r in over}
        rows = [r for r in rows if r["workload"] not in replaced] + over
    if a.quiet:
        rows = [r for r in rows if r.get("busy_cores_run") is not None and r["busy_cores_run"] <= a.quiet]
    suffix = "-quiet" if a.quiet else ""
    c = cells(rows)
    keys = sorted({(w, ca) for (w, ca, _, _) in c}, key=lambda k: (WORKLOADS.index(k[0]), k[1]))
    sides = [s for s in ("go", "ts", "pi", "nopig", "nopi") if any(k[2] == s for k in c)]
    all_metrics = sorted({m for cell in c.values() for m in cell if m != "_runs"})
    headline = [m for m in RATCHET + INFO if m in all_metrics] + sorted(m for m in all_metrics if m.startswith("tool_ms:"))
    full = ["# LangSmith perf summary (every metric)", ""] + table(c, sides, keys, all_metrics)
    (pathlib.Path(a.dir) / f"summary-full{suffix}.md").write_text("\n".join(full) + "\n")
    lines = ["# LangSmith perf summary", "",
             "Cells are `median ±spread%` over fresh-process samples (spread = (p90-p10)/median; `!` marks more than 15 %). Ratios are of medians "
             "(below 1.00: Go is smaller or faster). `nopig`/`nopi` are the same host with no extension. Metric definitions: README.md. Every metric: summary-full.md.", ""]
    lines += table(c, sides, keys, headline)
    mism = parity(c, keys, [s for s in MAIN_SIDES if s in sides])
    lines += ["## Equal work across sides (model requests, LangSmith requests and bytes)", ""] + (mism or ["identical model request counts; LangSmith ingest requests and bytes within 5 %."]) + [""]
    reqs = ["## LangSmith requests per workload (median over samples; the timer-leak divergence D3 shows as more, smaller ingest requests under ts/pi)", "",
            "| Workload | go ingest req | ts ingest req | pi ingest req | go decoded bytes | ts decoded bytes | pi decoded bytes | go/ts bytes |", "|---|---:|---:|---:|---:|---:|---:|---:|"]
    for wl, cache in keys:
        got = {s: c.get((wl, cache, s, "plain"), {}) for s in MAIN_SIDES}
        if not any(g.get("ls_ingest_requests") for g in got.values()):
            continue

        def med(s, k):
            v = got[s].get(k)
            return statistics.median(v) if v else None

        def txt(s, k, f):
            return f(med(s, k)) if med(s, k) is not None else "-"
        bg, bt = med("go", "ls_decoded_bytes"), med("ts", "ls_decoded_bytes")
        ratio = f"{bg / bt:.3f}" if bg and bt else "-"
        reqs.append(f"| {wl}/{cache} | " + " | ".join(txt(s, "ls_ingest_requests", lambda x: f"{x:.0f}") for s in MAIN_SIDES) + " | "
                    + " | ".join(txt(s, "ls_decoded_bytes", lambda x: f"{x / 1048576:.2f} MiB") for s in MAIN_SIDES) + f" | {ratio} |")
    lines += reqs + [""]
    worse = []
    if "go" in sides and "ts" in sides:
        worse = ["## Metrics where go is worse than ts (same pig host, Node extension runtime)", "",
                 "| Workload | Metric | go | ts | go/ts | separated |", "|---|---|---:|---:|---:|---|"]
        n = 0
        for wl, cache in keys:
            for metric in RATCHET + sorted(m for m in all_metrics if m.startswith("tool_ms:")):
                g = c.get((wl, cache, "go", variant_of(metric)), {}).get(metric)
                t = c.get((wl, cache, "ts", variant_of(metric)), {}).get(metric)
                if not g or not t:
                    continue
                gm, tm = statistics.median(g), statistics.median(t)
                if gm > tm:
                    sep = "yes (p10 of go above p90 of ts)" if sorted(g)[math.floor(0.1 * (len(g) - 1))] > q(t, .9) else "no (ranges overlap)"
                    n += 1
                    worse.append(f"| {wl}/{cache} | {metric} | {fmt(metric, gm)} | {fmt(metric, tm)} | {gm / tm if tm else float('inf'):.2f} | {sep} |")
        worse += ["", f"Metrics worse: {n}."]
    text = "\n".join(lines + worse) + "\n"
    (pathlib.Path(a.dir) / f"summary{suffix}.md").write_text(text)
    print(text)
    return 1 if any("MISMATCH" in m for m in mism) else 0


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    r = sub.add_parser("run")
    r.add_argument("--out", required=True)
    r.add_argument("--sides", default="go,ts,pi")
    r.add_argument("--workloads", default="all")
    r.add_argument("--samples", type=int, default=15)
    r.add_argument("--mem-samples", type=int, default=None, help="samples of the mem variant per cell (default: --samples)")
    r.add_argument("--baselines", action="store_true", help="also run the same host with no extension (nopig, nopi) in workloads a and c")
    r.add_argument("--cpus", default=os.environ.get("LS_PERF_CPUS", ""), help="helper,tree1,tree2 (default: the three idlest physical cores)")
    r.add_argument("--keep-going", action="store_true")
    r.add_argument("--max-busy", type=float, default=0, help="before every sample wait until the other lanes use at most this many CPUs (0: no gate)")
    r.add_argument("--max-wait", type=float, default=900, help="longest wait of the --max-busy gate in seconds; the row records gate_met=false when it ran out")
    r.add_argument("--profile", default="", help="directory: the mem variant of side go also writes CPU and allocation pprof files there")
    r.add_argument("--seed", default="c", help="workload of the three unmeasured warm-up launches")
    s = sub.add_parser("summarize")
    s.add_argument("dir")
    s.add_argument("--quiet", type=float, default=0, help="keep only samples taken while the whole machine used at most this many CPUs (busy_cores_run, ours included); writes summary-quiet.md")
    s.add_argument("--override", help="a second run dir: its workloads replace those of the first (a rerun of a noisy workload)")
    a = ap.parse_args()
    if a.cmd == "run":
        if a.samples < 15:
            print("note: fewer than 15 samples per cell is a smoke run, not a baseline", file=sys.stderr)
        return cmd_run(a)
    return cmd_summarize(a)


if __name__ == "__main__":
    sys.exit(main())
