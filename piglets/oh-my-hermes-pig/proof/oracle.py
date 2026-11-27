#!/usr/bin/env python3
"""
Hermetic behavioral & differential oracle proof runner for pig-hermes.
Drives the compiled `pig-hermes` fused Piglet binary (and optionally pinned
upstream `hermes-agent`) against `pigeq llm --script <turns.json>` over loopback,
with:
- isolated HOME / PIG_HOME / HERMES_HOME / workspace
- refusing HTTP/HTTPS proxy (0 accidental network calls)
- process-tree reaping via ORACLE_RUN_TAG (0 leaked PIDs)
- exact checks on advertised tools, system prompt sections, tool execution
  results, and file/memory/cron side effects.
"""

import json
import os
import re
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path


class RefusingProxyHandler(BaseHTTPRequestHandler):
    attempts = []

    def _refuse(self):
        self.__class__.attempts.append(f"{self.command} {self.path}")
        self.send_response(403)
        self.end_headers()
        self.wfile.write(b"network disabled by pig-hermes oracle")

    do_GET = _refuse
    do_POST = _refuse
    do_PUT = _refuse
    do_DELETE = _refuse
    do_HEAD = _refuse
    do_CONNECT = _refuse

    def log_message(self, format, *args):
        pass


def start_refusing_proxy():
    RefusingProxyHandler.attempts = []
    srv = HTTPServer(("127.0.0.1", 0), RefusingProxyHandler)
    port = srv.server_address[1]
    t = threading.Thread(target=srv.serve_forever, daemon=True)
    t.start()
    return srv, port


def reap_tagged_processes(tag):
    """Ensure zero child processes with ORACLE_RUN_TAG remain."""
    try:
        out = subprocess.check_output(["ps", "eww", "-ax", "-o", "pid=,command="], text=True, errors="replace")
    except Exception:
        return 0
    me = os.getpid()
    pids = []
    for line in out.splitlines():
        if f"ORACLE_RUN_TAG={tag}" in line:
            parts = line.strip().split(None, 1)
            if parts and parts[0].isdigit():
                pid = int(parts[0])
                if pid != me:
                    pids.append(pid)
    for pid in pids:
        try:
            os.kill(pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
    if pids:
        time.sleep(0.3)
        for pid in pids:
            try:
                os.kill(pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
    return len(pids)


def run_case(pigeq_bin, pig_hermes_bin, case_path):
    case = json.loads(Path(case_path).read_text())
    name = case["name"]
    tag = f"pighermes-{name}-{os.getpid()}-{int(time.time()*1000)}"

    proxy_srv, proxy_port = start_refusing_proxy()
    tmp = tempfile.mkdtemp(prefix=f"pighermes-oracle-{name}-")
    llm_proc = None
    try:
        work = Path(tmp) / "work"
        home = Path(tmp) / "home"
        pig_home = Path(tmp) / "pig"
        agent_dir = Path(tmp) / "agent"
        hermes_home = Path(tmp) / "hermes"
        for d in (work, home, pig_home, agent_dir, hermes_home):
            d.mkdir(parents=True, exist_ok=True)

        # Seed workspace and hermes_home files
        for rel, content in case.get("files", {}).items():
            fp = work / rel
            fp.parent.mkdir(parents=True, exist_ok=True)
            fp.write_text(content)
        for rel, content in case.get("hermesFiles", {}).items():
            fp = hermes_home / rel
            fp.parent.mkdir(parents=True, exist_ok=True)
            fp.write_text(content)

        # Start pigeq llm --script
        turns_file = Path(tmp) / "turns.json"
        turns_file.write_text(json.dumps(case["llm"]))
        llm_log = Path(tmp) / "llm.jsonl"
        llm_proc = subprocess.Popen(
            [pigeq_bin, "llm", "--script", str(turns_file), "--log", str(llm_log)],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        base_url = llm_proc.stdout.readline().strip()
        if not base_url.startswith("http://127.0.0.1:"):
            stderr_txt = llm_proc.stderr.read()
            raise RuntimeError(f"pigeq llm failed to start: url={base_url!r} stderr={stderr_txt!r}")

        # Write models.json for PiG pointing at pigeq llm (base_url already includes /v1)
        models_cfg = {
            "providers": {
                "oracle": {
                    "baseUrl": base_url,
                    "api": "openai-completions",
                    "apiKey": "oracle-test-key",
                    "models": [
                        {
                            "id": "oracle-model",
                            "name": "Oracle Model",
                            "reasoning": False,
                            "input": ["text"],
                            "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
                            "contextWindow": 128000,
                            "maxTokens": 4096,
                        }
                    ],
                }
            }
        }
        (agent_dir / "models.json").write_text(json.dumps(models_cfg))

        proxy_url = f"http://127.0.0.1:{proxy_port}"
        env = {
            "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
            "HOME": str(home),
            "PIG_HOME": str(pig_home),
            "PIG_CODING_AGENT_DIR": str(agent_dir),
            "HERMES_HOME": str(hermes_home),
            "HERMES_PROFILE": "",
            "TMPDIR": tmp,
            "ORACLE_RUN_TAG": tag,
            "HTTP_PROXY": proxy_url,
            "http_proxy": proxy_url,
            "HTTPS_PROXY": proxy_url,
            "https_proxy": proxy_url,
            "NO_PROXY": "127.0.0.1,localhost,::1",
            "no_proxy": "127.0.0.1,localhost,::1",
        }

        cmd = [
            pig_hermes_bin,
            "--provider", "oracle",
            "--model", "oracle-model",
            "--no-session",
            "-p", case["prompt"],
            "--mode", "json",
        ]
        res = subprocess.run(cmd, cwd=str(work), env=env, capture_output=True, text=True, timeout=45)

        # Close pigeq llm cleanly
        if llm_proc.stdin:
            llm_proc.stdin.close()
        llm_proc.wait(timeout=5)
        llm_proc = None

        # Parse JSONL events from pig-hermes --mode json
        events = []
        for line in res.stdout.splitlines():
            line = line.strip()
            if line.startswith("{"):
                try:
                    events.append(json.loads(line))
                except json.JSONDecodeError:
                    pass

        # Parse LLM requests logged by pigeq llm
        llm_reqs = []
        if llm_log.exists():
            for line in llm_log.read_text().splitlines():
                if line.strip():
                    llm_reqs.append(json.loads(line))

        errors = []
        if res.returncode != 0:
            errors.append(f"non-zero exit {res.returncode}: stderr={res.stderr.strip()[:400]}")

        if RefusingProxyHandler.attempts:
            errors.append(f"accidental network calls: {RefusingProxyHandler.attempts}")

        # Verify advertised tools in first LLM request (pigeq llm logs {"ch":"llm","data":{"messages":...,"tools":...}})
        if llm_reqs:
            first_req = llm_reqs[0].get("data", llm_reqs[0])
            tool_names = sorted(
                (t.get("name") or t.get("function", {}).get("name", ""))
                for t in first_req.get("tools", [])
                if isinstance(t, dict)
            )
            for req_tool in case.get("expectTools", []):
                if req_tool not in tool_names:
                    errors.append(f"missing advertised tool {req_tool!r} (got {tool_names})")
            for forbid_tool in case.get("forbidTools", []):
                if forbid_tool in tool_names:
                    errors.append(f"forbidden built-in tool leaked: {forbid_tool!r}")

            # Check system prompt substrings (from LLM messages OR PiG --mode json system message_start sections)
            sys_msgs = [m.get("content", "") for m in first_req.get("messages", []) if m.get("role") == "system"]
            for ev in events:
                if ev.get("type") == "message_start":
                    msg = ev.get("message", {})
                    if msg.get("role") == "system":
                        if msg.get("content"):
                            sys_msgs.append(msg["content"])
                        for v in (msg.get("sections") or {}).values():
                            sys_msgs.append(str(v))
            sys_text = "\n".join(str(x) for x in sys_msgs)
            for sub in case.get("expectSystemContains", []):
                if sub not in sys_text:
                    errors.append(f"system prompt missing {sub!r}")
        else:
            errors.append("no LLM requests recorded by pigeq llm")

        # Verify tool results in json events
        tool_results = {}
        for ev in events:
            if ev.get("type") == "tool_execution_end":
                tname = ev.get("toolName", "")
                content = ev.get("result", {}).get("content", "")
                if isinstance(content, list):
                    content = "\n".join(b.get("text", "") for b in content if isinstance(b, dict))
                tool_results.setdefault(tname, []).append({
                    "isError": bool(ev.get("isError")),
                    "text": str(content),
                })

        for check in case.get("expectToolResults", []):
            tname = check["tool"]
            idx = check.get("index", 0)
            runs = tool_results.get(tname, [])
            if idx >= len(runs):
                errors.append(f"tool {tname}[{idx}] never ran (ran {len(runs)} times)")
                continue
            got = runs[idx]
            if "isError" in check and got["isError"] != check["isError"]:
                errors.append(f"tool {tname}[{idx}] isError={got['isError']} want {check['isError']} (text={got['text'][:200]!r})")
            for sub in check.get("contains", []):
                if sub not in got["text"]:
                    errors.append(f"tool {tname}[{idx}] output missing {sub!r}; got {got['text'][:300]!r}")

        # Verify workspace/hermes files after run
        for rel, sub in case.get("expectWorkFileContains", {}).items():
            fp = work / rel
            if not fp.exists():
                errors.append(f"expected workspace file {rel} missing")
            elif sub not in fp.read_text():
                errors.append(f"workspace file {rel} missing {sub!r}")

        for rel, sub in case.get("expectHermesFileContains", {}).items():
            fp = hermes_home / rel
            if not fp.exists():
                errors.append(f"expected hermes file {rel} missing")
            elif sub not in fp.read_text():
                errors.append(f"hermes file {rel} missing {sub!r}")

        leaked = reap_tagged_processes(tag)
        if leaked:
            errors.append(f"leaked {leaked} tagged child processes")

        return {
            "name": name,
            "ok": len(errors) == 0,
            "errors": errors,
            "llm_turns": len(llm_reqs),
            "tool_calls": sum(len(v) for v in tool_results.values()),
            "network_attempts": len(RefusingProxyHandler.attempts),
            "leaked_pids": leaked,
        }
    finally:
        if llm_proc is not None:
            try:
                llm_proc.kill()
                llm_proc.wait(timeout=2)
            except Exception:
                pass
        proxy_srv.shutdown()
        shutil.rmtree(tmp, ignore_errors=True)


def main():
    repo = Path(__file__).resolve().parents[3]
    pigeq_bin = os.environ.get("PIGEQ_BIN", str(repo / "dist/bin/pigeq"))
    pig_hermes_bin = os.environ.get("PIG_HERMES_BIN", str(repo / "dist/bin/pig-hermes"))
    cases_dir = Path(__file__).resolve().parent / "cases"
    case_files = sorted(cases_dir.glob("*.json"))
    if not case_files:
        print("no oracle case files found", file=sys.stderr)
        sys.exit(2)

    results = []
    failed = 0
    for cf in case_files:
        r = run_case(pigeq_bin, pig_hermes_bin, cf)
        results.append(r)
        status = "PASS" if r["ok"] else "FAIL"
        print(f"[{status}] {r['name']} (turns={r['llm_turns']}, tool_calls={r['tool_calls']}, net={r['network_attempts']}, leaked={r['leaked_pids']})")
        if not r["ok"]:
            failed += 1
            for e in r["errors"]:
                print(f"       - {e}")

    print(f"\nOracle summary: {len(results)-failed}/{len(results)} passed, {failed} failed")
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
