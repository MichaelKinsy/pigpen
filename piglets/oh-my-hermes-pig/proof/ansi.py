#!/usr/bin/env python3
"""
Hermetic PTY/ANSI visual proof runner for oh-my-hermes-pig.

Spawns the compiled `oh-my-hermes-pig` binary inside a real PTY (36x120,
TERM=xterm-256color, COLORTERM=truecolor) with:
  - isolated HOME / PIG_HOME / PIG_CODING_AGENT_DIR / HERMES_HOME / OMH_HOME / workspace
  - refusing HTTP/HTTPS proxy (0 accidental network calls)
  - process-tree reaping via ORACLE_RUN_TAG (0 leaked PIDs)
  - interactive startup banner + truecolor ANSI verification
  - interactive `/status`, `/omh-hud`, and `/omh-theme kanagawa` slash-command verification
  - clean exit via `/quit` (exit code 0)

Environment variables:
  OH_MY_HERMES_BIN  Path to compiled oh-my-hermes-pig binary (default: <repo>/dist/bin/oh-my-hermes-pig)
  ANSI_OUT          Optional path to save captured screen transcript (ANSI + plain summary)
"""

import fcntl
import json
import os
import pty
import re
import select
import shutil
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

ANSI_ESCAPE_RE = re.compile(
    r"\x1b(?:\[[0-9;?]*[A-Za-z]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[()][AB012]|[@-Z\\-_])"
)
TRUECOLOR_RE = re.compile(r"\x1b\[(?:38|48);2;\d+;\d+;\d+m")
ANY_SGR_RE = re.compile(r"\x1b\[[0-9;]*m")

ROWS = 36
COLS = 120


class RefusingProxyHandler(BaseHTTPRequestHandler):
    attempts = []

    def _refuse(self):
        self.__class__.attempts.append(f"{self.command} {self.path}")
        self.send_response(403)
        self.end_headers()
        self.wfile.write(b"network disabled by oh-my-hermes-pig ansi proof")

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
        out = subprocess.check_output(
            ["ps", "eww", "-ax", "-o", "pid=,command="], text=True, errors="replace"
        )
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


def strip_ansi(text):
    return ANSI_ESCAPE_RE.sub("", text)


def normalize_ws(text):
    return " ".join(text.split())


def read_until_settled(master_fd, max_wait=3.0, settle_quiet=0.45, min_wait=0.5, stop_substrings=None):
    """Read from PTY until output stops arriving for settle_quiet seconds."""
    buf = bytearray()
    start = time.monotonic()
    last_data = start
    stop_substrings = stop_substrings or []
    while True:
        now = time.monotonic()
        elapsed = now - start
        if elapsed >= max_wait:
            break
        remaining = max_wait - elapsed
        timeout = min(0.08, remaining)
        r, _, _ = select.select([master_fd], [], [], timeout)
        if r:
            try:
                chunk = os.read(master_fd, 65536)
                if not chunk:
                    break
                buf.extend(chunk)
                last_data = time.monotonic()
                if stop_substrings:
                    plain = strip_ansi(buf.decode("utf-8", errors="replace")).lower()
                    if any(s.lower() in plain for s in stop_substrings) and (time.monotonic() - start) >= min_wait:
                        end_drain = time.monotonic() + 0.25
                        while time.monotonic() < end_drain:
                            r2, _, _ = select.select([master_fd], [], [], 0.05)
                            if not r2:
                                break
                            try:
                                c2 = os.read(master_fd, 65536)
                                if not c2:
                                    break
                                buf.extend(c2)
                            except OSError:
                                break
                        break
            except OSError:
                break
        else:
            if elapsed >= min_wait and buf and (now - last_data) >= settle_quiet:
                break
    return buf.decode("utf-8", errors="replace")


def write_models_cfg(agent_dir):
    models_cfg = {
        "providers": {
            "offline": {
                "baseUrl": "http://127.0.0.1:1/v1",
                "api": "openai-completions",
                "apiKey": "ansi-proof-dummy-key",
                "models": [
                    {
                        "id": "offline-model",
                        "name": "Offline Proof Model",
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
    (agent_dir / "models.json").write_text(json.dumps(models_cfg), encoding="utf-8")


def run_visual_proof(bin_path, ansi_out=None):
    errors = []
    if not os.path.isfile(bin_path) or not os.access(bin_path, os.X_OK):
        return {
            "ok": False,
            "errors": [f"binary not found or not executable: {bin_path}"],
            "metrics": {},
        }

    tag = f"omh-ansi-{os.getpid()}-{int(time.time() * 1000)}"
    proxy_srv, proxy_port = start_refusing_proxy()
    tmp = tempfile.mkdtemp(prefix="omh-ansi-")
    master_fd = None
    proc = None
    transcript_sections = []

    try:
        work = Path(tmp) / "work"
        home = Path(tmp) / "home"
        pig_home = Path(tmp) / "pig"
        agent_dir = Path(tmp) / "agent"
        hermes_home = Path(tmp) / "hermes"
        omh_home = Path(tmp) / "omh"
        for d in (work, home, pig_home, agent_dir, hermes_home, omh_home):
            d.mkdir(parents=True, exist_ok=True)

        write_models_cfg(agent_dir)

        proxy_url = f"http://127.0.0.1:{proxy_port}"
        env = {
            "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
            "TERM": "xterm-256color",
            "COLORTERM": "truecolor",
            "PIG_OFFLINE": "1",
            "PI_OFFLINE": "1",
            "PI_SKIP_VERSION_CHECK": "1",
            "PIG_TELEMETRY": "0",
            "HOME": str(home),
            "PIG_HOME": str(pig_home),
            "PIG_CODING_AGENT_DIR": str(agent_dir),
            "HERMES_HOME": str(hermes_home),
            "HERMES_PROFILE": "",
            "OMH_HOME": str(omh_home),
            "TMPDIR": tmp,
            "ORACLE_RUN_TAG": tag,
            "HTTP_PROXY": proxy_url,
            "http_proxy": proxy_url,
            "HTTPS_PROXY": proxy_url,
            "https_proxy": proxy_url,
            "NO_PROXY": "127.0.0.1,localhost,::1",
            "no_proxy": "127.0.0.1,localhost,::1",
        }

        master_fd, slave_fd = pty.openpty()
        fcntl.ioctl(slave_fd, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, COLS, 0, 0))

        cmd = [
            bin_path,
            "--provider", "offline",
            "--model", "offline-model",
            "--no-session",
            "--offline",
        ]
        proc = subprocess.Popen(
            cmd,
            stdin=slave_fd,
            stdout=slave_fd,
            stderr=slave_fd,
            cwd=str(work),
            env=env,
            close_fds=True,
            start_new_session=True,
        )
        os.close(slave_fd)

        # 1. Wait for startup screen to settle.
        startup_raw = read_until_settled(
            master_fd,
            max_wait=5.0,
            settle_quiet=0.55,
            min_wait=0.8,
        )
        startup_plain = strip_ansi(startup_raw)
        startup_norm = normalize_ws(startup_plain)
        transcript_sections.append(("STARTUP", startup_raw, startup_norm))

        truecolor_hits = TRUECOLOR_RE.findall(startup_raw)
        sgr_hits = ANY_SGR_RE.findall(startup_raw)

        if not startup_raw.strip():
            errors.append("startup produced empty PTY output")
        if not truecolor_hits:
            errors.append("startup missing truecolor ANSI escape sequences (\\x1b[38;2;... or \\x1b[48;2;...)")
        if not sgr_hits:
            errors.append("startup missing ANSI SGR escape sequences")

        startup_lower = startup_norm.lower()
        has_branding = (
            "oh-my-hermes-pig" in startup_lower
            or "omh[" in startup_lower
            or "hermes" in startup_lower
            or "☤" in startup_plain
        )
        if not has_branding:
            errors.append("startup missing oh-my-hermes-pig / OMH branding")

        has_status_bar = (
            "omh[" in startup_lower
            or "hermes:" in startup_lower
            or "☤" in startup_plain
        )
        if not has_status_bar:
            errors.append("startup missing OMH / hermes status bar indicator")

        # 2. Send /status and verify inherited pig-hermes session status output.
        os.write(master_fd, b"/status\r")
        status_raw = read_until_settled(
            master_fd,
            max_wait=3.0,
            settle_quiet=0.45,
            min_wait=0.35,
            stop_substrings=["hermes session status", "memory.md"],
        )
        status_plain = strip_ansi(status_raw)
        status_norm = normalize_ws(status_plain)
        transcript_sections.append(("/status", status_raw, status_norm))

        if "hermes session status" not in status_norm.lower():
            errors.append(f"/status did not render 'Hermes Session Status'; got: {status_norm[:240]!r}")

        # 3. Send /omh-hud and verify OMH orchestration HUD renders.
        os.write(master_fd, b"/omh-hud\r")
        hud_raw = read_until_settled(
            master_fd,
            max_wait=3.0,
            settle_quiet=0.45,
            min_wait=0.35,
            stop_substrings=["omh hud", "dags:"],
        )
        hud_plain = strip_ansi(hud_raw)
        hud_norm = normalize_ws(hud_plain)
        transcript_sections.append(("/omh-hud", hud_raw, hud_norm))

        if "omh hud" not in hud_norm.lower() and "dags:" not in hud_norm.lower():
            errors.append(f"/omh-hud did not render OMH HUD summary; got: {hud_norm[:240]!r}")

        # 4. Send /omh-theme kanagawa and verify theme switch confirmation without error.
        os.write(master_fd, b"/omh-theme kanagawa\r")
        theme_raw = read_until_settled(
            master_fd,
            max_wait=3.0,
            settle_quiet=0.45,
            min_wait=0.35,
            stop_substrings=["kanagawa"],
        )
        theme_plain = strip_ansi(theme_raw)
        theme_norm = normalize_ws(theme_plain)
        transcript_sections.append(("/omh-theme kanagawa", theme_raw, theme_norm))

        if "kanagawa" not in theme_norm.lower():
            errors.append(f"/omh-theme kanagawa did not confirm theme switch; got: {theme_norm[:240]!r}")
        if "unknown omh theme" in theme_norm.lower() or "error:" in theme_norm.lower():
            errors.append(f"/omh-theme kanagawa reported an error: {theme_norm[:240]!r}")

        # 5. Clean exit via /quit (fallback Ctrl+D / Ctrl+C).
        os.write(master_fd, b"/quit\r")
        exit_raw = read_until_settled(master_fd, max_wait=2.5, settle_quiet=0.35, min_wait=0.2)
        transcript_sections.append(("/quit", exit_raw, normalize_ws(strip_ansi(exit_raw))))

        exit_code = proc.poll()
        if exit_code is None:
            try:
                proc.wait(timeout=2.0)
            except subprocess.TimeoutExpired:
                os.write(master_fd, b"\x04")
                try:
                    proc.wait(timeout=1.5)
                except subprocess.TimeoutExpired:
                    os.write(master_fd, b"\x03\x03")
                    try:
                        proc.wait(timeout=1.5)
                    except subprocess.TimeoutExpired:
                        proc.terminate()
                        proc.wait(timeout=2.0)
            exit_code = proc.returncode

        if exit_code != 0:
            errors.append(f"expected exit code 0, got {exit_code}")

        os.close(master_fd)
        master_fd = None

        leaked = reap_tagged_processes(tag)
        net_attempts = list(RefusingProxyHandler.attempts)
        if leaked > 0:
            errors.append(f"leaked {leaked} tagged child process(es)")
        if net_attempts:
            errors.append(f"accidental external network call(s): {net_attempts}")

        if ansi_out:
            out_path = Path(ansi_out)
            out_path.parent.mkdir(parents=True, exist_ok=True)
            with out_path.open("w", encoding="utf-8") as f:
                for label, raw, norm in transcript_sections:
                    f.write(f"=== {label} (RAW ANSI) ===\n")
                    f.write(raw)
                    f.write(f"\n=== {label} (PLAIN) ===\n")
                    f.write(norm)
                    f.write("\n\n")

        return {
            "ok": len(errors) == 0,
            "errors": errors,
            "metrics": {
                "truecolor_codes": len(truecolor_hits),
                "sgr_codes": len(sgr_hits),
                "startup_bytes": len(startup_raw),
                "exit_code": exit_code,
                "leaked_pids": leaked,
                "network_attempts": len(net_attempts),
            },
        }
    finally:
        if master_fd is not None:
            try:
                os.close(master_fd)
            except OSError:
                pass
        if proc is not None and proc.poll() is None:
            try:
                proc.terminate()
                proc.wait(timeout=2.0)
            except Exception:
                try:
                    proc.kill()
                except Exception:
                    pass
        reap_tagged_processes(tag)
        proxy_srv.shutdown()
        shutil.rmtree(tmp, ignore_errors=True)


def main():
    repo = Path(__file__).resolve().parents[3]
    bin_path = os.environ.get("OH_MY_HERMES_BIN", str(repo / "dist/bin/oh-my-hermes-pig"))
    ansi_out = os.environ.get("ANSI_OUT")

    res = run_visual_proof(bin_path, ansi_out=ansi_out)
    m = res["metrics"]
    status = "PASS" if res["ok"] else "FAIL"
    print(
        f"[{status}] oh-my-hermes-pig PTY/ANSI visual proof "
        f"(truecolor={m.get('truecolor_codes', 0)}, sgr={m.get('sgr_codes', 0)}, "
        f"exit={m.get('exit_code')}, net={m.get('network_attempts', 0)}, "
        f"leaked={m.get('leaked_pids', 0)})"
    )
    if ansi_out:
        print(f"       transcript saved: {ansi_out}")
    for err in res["errors"]:
        print(f"       - {err}")
    sys.exit(0 if res["ok"] else 1)


if __name__ == "__main__":
    main()
