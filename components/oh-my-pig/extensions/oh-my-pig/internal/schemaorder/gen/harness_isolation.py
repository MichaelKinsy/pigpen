#!/usr/bin/env python3
"""Shared isolation helpers for the oh-my-pig proof & oracle harness.

Provides:
- RefusingProxyServer: loopback HTTP/CONNECT proxy that refuses every request
  with 403 and records every attempted target so any non-loopback network call
  is caught and fails the run.
- sandbox_cmd(): wraps a child command in a macOS `sandbox-exec` profile that
  denies all non-loopback outbound IP traffic (without requiring privileges),
  when `sandbox-exec` is available on Darwin.
- find_tagged_pids() / reap_tagged_processes(): locate and terminate every
  process carrying `ORACLE_RUN_TAG=<tag>` in its environment (plus any live
  children of those PIDs), never matching by process name.
- strip_external_env(): removes provider/cloud/search/vcs/proxy/multiplexer
  environment variables by prefix or exact name.
- check_free_disk_space(): refuses to start under 2 GiB free.
"""

from __future__ import annotations

import http.server
import os
import platform
import shutil
import signal
import subprocess
import sys
import threading
import time
from pathlib import Path
from typing import Any

MIN_FREE_BYTES = 2 * 1024 * 1024 * 1024  # 2 GiB

# macOS Seatbelt profile: allow everything except non-loopback outbound IP.
# Unix domain sockets and loopback (127.0.0.1 / ::1 / localhost) stay allowed.
MACOS_LOOPBACK_SANDBOX_PROFILE = (
    '(version 1)'
    '(allow default)'
    '(deny network-outbound (remote ip "*:*"))'
    '(allow network-outbound (remote ip "localhost:*"))'
)

STRIP_ENV_PREFIXES: tuple[str, ...] = (
    "TERN_", "TERM_PROGRAM", "TMUX", "STY", "ZELLIJ",
    "OMP_", "PI_", "PIG_",
    "OPENAI_", "ANTHROPIC_", "GEMINI_", "COPILOT_", "GOOGLE_",
    "EXA_", "BRAVE_", "TAVILY_", "PERPLEXITY_", "FIRECRAWL_",
    "TYPESAFE_", "TINYFISH_", "KEENABLE_", "SOFYA_",
    "JINA_", "PARALLEL_", "KAGI_",
    "GH_", "GLAB_", "GITHUB_", "GITLAB_",
    "AWS_", "AZURE_", "MISTRAL_", "GROQ_", "XAI_", "DEEPSEEK_",
    "OPENROUTER_", "CEREBRAS_", "HF_", "HUGGINGFACE_",
    "SSH_",
)

STRIP_ENV_EXACT: frozenset[str] = frozenset({
    "HTTP_PROXY", "http_proxy",
    "HTTPS_PROXY", "https_proxy",
    "ALL_PROXY", "all_proxy",
    "NO_PROXY", "no_proxy",
    "FTP_PROXY", "ftp_proxy",
    "SSH_AUTH_SOCK", "SSH_AGENT_PID",
})

PROXY_ENV_KEYS: tuple[str, ...] = (
    "HTTP_PROXY", "http_proxy",
    "HTTPS_PROXY", "https_proxy",
    "ALL_PROXY", "all_proxy",
)


# All 98 built-in omp v19.1.1 provider IDs (plus implicit llama.cpp) disabled
# in offline proof/oracle config.yml so background model discovery never probes
# catalog.stencil.so, api.kilo.ai, api.venice.ai, hyper.charm.land, etc.
OMP_DISABLED_BUILTIN_PROVIDERS: tuple[str, ...] = (
    "abliteration", "aiand", "aimlapi", "alibaba-coding-plan", "alibaba-token-plan",
    "amazon-bedrock", "anthropic", "antigravity-cli", "apple", "azure",
    "baseten", "bedrock-mantle", "bifrost", "cerebras", "charm-hyper",
    "chutes", "cline-pass", "cloudflare-ai-gateway", "cloudflare-workers-ai", "commandcode",
    "coralbricks", "coreweave", "cursor", "deepinfra", "deepseek",
    "devin", "factory-droid", "firepass", "fireworks", "github-copilot",
    "gitlab-duo", "gitlab-duo-agent", "gmi-cloud", "google", "google-antigravity",
    "google-vertex", "groq", "helmcode", "huggingface", "inception",
    "kilo", "kimi-code", "kiro", "liquid", "litellm",
    "llama.cpp", "lm-studio", "local", "meta", "minimax", "minimax-cn",
    "minimax-code", "minimax-code-cn", "mistral", "modal", "modular",
    "moonshot", "muse-code", "nace", "nanogpt", "novita",
    "nvidia", "ollama", "ollama-cloud", "openai", "openai-codex",
    "opencode-go", "opencode-zen", "openrouter", "otari", "qianfan",
    "qwen-portal", "sakana", "sambanova", "scaleway", "siliconflow",
    "siliconflow-cn", "singularityapi-dev", "singularityapi-tech", "snowflake", "stepfun",
    "synthetic", "together", "typesafe", "umans", "venice",
    "vercel-ai-gateway", "vllm", "wafer-serverless", "web", "xai",
    "xai-oauth", "xiaomi", "xiaomi-token-plan-ams", "xiaomi-token-plan-cn", "xiaomi-token-plan-sgp",
    "yolo-auto", "zai", "zenmux", "zhipu-coding-plan",
)


def omp_offline_config_yml(extra_yml: str = "") -> str:
    """Build omp config.yml that disables all external network activity."""
    disabled_lines = "\n".join(f"  - {p}" for p in OMP_DISABLED_BUILTIN_PROVIDERS)
    base = (
        "composer:\n"
        "  predictions: false\n"
        "startup:\n"
        "  checkUpdate: false\n"
        "update:\n"
        "  auto: false\n"
        "providers:\n"
        "  webKeyless: false\n"
        "  fetch: native\n"
        "disabledProviders:\n"
        f"{disabled_lines}\n"
    )
    if extra_yml:
        base += "\n" + extra_yml + "\n"
    return base
NO_PROXY_VALUE = "127.0.0.1,localhost,::1"


def clean_base_env() -> dict[str, str]:
    env = os.environ.copy()
    for k in list(env.keys()):
        if k in STRIP_ENV_EXACT or k.startswith(STRIP_ENV_PREFIXES):
            env.pop(k, None)
    return env


def apply_proxy_env(env: dict[str, str], proxy_url: str) -> None:
    for k in PROXY_ENV_KEYS:
        env[k] = proxy_url
    env["NO_PROXY"] = NO_PROXY_VALUE
    env["no_proxy"] = NO_PROXY_VALUE


def sandbox_available() -> bool:
    return platform.system() == "Darwin" and shutil.which("sandbox-exec") is not None


def sandbox_cmd(cmd: list[str]) -> list[str]:
    """Wrap cmd in macOS loopback-only sandbox-exec when available."""
    if os.environ.get("ORACLE_NO_SANDBOX") == "1":
        return cmd
    if sandbox_available():
        return ["sandbox-exec", "-p", MACOS_LOOPBACK_SANDBOX_PROFILE, "--", *cmd]
    return cmd


def check_free_disk_space(target_dir: Path, min_bytes: int = MIN_FREE_BYTES) -> None:
    probe = target_dir
    while not probe.exists() and probe.parent != probe:
        probe = probe.parent
    free = shutil.disk_usage(probe).free
    if free < min_bytes:
        free_gb = free / (1024 ** 3)
        need_gb = min_bytes / (1024 ** 3)
        raise SystemExit(
            f"error: refusing to start — only {free_gb:.2f} GiB free on {probe} (need at least {need_gb:.2f} GiB)"
        )


def dir_size_bytes(path: Path) -> int:
    total = 0
    if not path.exists():
        return 0
    for root, _dirs, files in os.walk(path):
        for name in files:
            fp = os.path.join(root, name)
            try:
                if not os.path.islink(fp):
                    total += os.path.getsize(fp)
            except OSError:
                pass
    return total


def format_bytes(n: int) -> str:
    val = float(n)
    for unit in ("B", "KiB", "MiB", "GiB"):
        if val < 1024 or unit == "GiB":
            return f"{val:.1f} {unit}" if unit != "B" else f"{int(val)} B"
        val /= 1024.0
    return f"{val:.1f} GiB"


# ---------------------------------------------------------------------------
# Refusing proxy listener (records & rejects every network attempt)
# ---------------------------------------------------------------------------

class RefusingProxyServer:
    """Loopback HTTP/HTTPS proxy that refuses every request and logs it."""

    def __init__(self, log_path: str | None = None) -> None:
        self.log_path = log_path
        self.lock = threading.Lock()
        self.attempts: list[dict[str, Any]] = []
        self._server: http.server.ThreadingHTTPServer | None = None
        self._thread: threading.Thread | None = None
        self.port: int = 0

    @property
    def url(self) -> str:
        return f"http://127.0.0.1:{self.port}"

    def record(self, method: str, target: str, host: str | None) -> None:
        entry = {
            "ts": time.time(),
            "method": method,
            "target": target,
            "host": host or "",
        }
        with self.lock:
            self.attempts.append(entry)
            if self.log_path:
                with open(self.log_path, "a", encoding="utf-8") as fh:
                    import json
                    fh.write(json.dumps(entry, ensure_ascii=False) + "\n")

    def snapshot(self) -> list[dict[str, Any]]:
        with self.lock:
            return list(self.attempts)

    def reset(self) -> None:
        with self.lock:
            self.attempts.clear()

    def start(self) -> int:
        owner = self

        class Handler(http.server.BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def log_message(self, fmt: str, *args: Any) -> None:
                pass

            def _refuse(self) -> None:
                owner.record(self.command, self.path, self.headers.get("Host"))
                body = b"Blocked by oh-my-pig proof refusing proxy (offline run)\n"
                self.send_response(403, "Forbidden")
                self.send_header("Content-Type", "text/plain")
                self.send_header("Content-Length", str(len(body)))
                self.send_header("Connection", "close")
                self.end_headers()
                try:
                    self.wfile.write(body)
                except OSError:
                    pass

            def do_CONNECT(self) -> None:
                self._refuse()

            def do_GET(self) -> None:
                self._refuse()

            def do_POST(self) -> None:
                self._refuse()

            def do_PUT(self) -> None:
                self._refuse()

            def do_DELETE(self) -> None:
                self._refuse()

            def do_HEAD(self) -> None:
                self._refuse()

            def do_OPTIONS(self) -> None:
                self._refuse()

            def do_PATCH(self) -> None:
                self._refuse()

        self._server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.port = self._server.server_address[1]
        self._thread = threading.Thread(target=self._server.serve_forever, daemon=True)
        self._thread.start()
        return self.port

    def stop(self) -> None:
        if self._server is not None:
            self._server.shutdown()
            self._server.server_close()
            self._server = None


# ---------------------------------------------------------------------------
# Process tracking & cleanup by ORACLE_RUN_TAG (NEVER by process name)
# ---------------------------------------------------------------------------

def _all_process_parents() -> dict[int, int]:
    """Return {pid: ppid} for all running processes."""
    parents: dict[int, int] = {}
    try:
        out = subprocess.check_output(["ps", "-ax", "-o", "pid=,ppid="], text=True, stderr=subprocess.DEVNULL)
        for line in out.splitlines():
            parts = line.split()
            if len(parts) >= 2:
                try:
                    parents[int(parts[0])] = int(parts[1])
                except ValueError:
                    pass
    except Exception:
        pass
    return parents


def find_tagged_pids(tag: str) -> list[int]:
    """Find all live PIDs (excluding self) whose environment contains ORACLE_RUN_TAG=<tag>,
    plus any descendant processes of those PIDs."""
    if not tag:
        return []
    needle = f"ORACLE_RUN_TAG={tag}"
    my_pid = os.getpid()
    found: set[int] = set()

    if os.path.isdir("/proc"):
        for entry in os.listdir("/proc"):
            if not entry.isdigit():
                continue
            pid = int(entry)
            if pid == my_pid:
                continue
            try:
                raw = Path(f"/proc/{pid}/environ").read_bytes()
            except OSError:
                continue
            if needle.encode("utf-8") in raw.split(b"\x00"):
                found.add(pid)
    else:
        try:
            out = subprocess.check_output(
                ["ps", "eww", "-ax", "-o", "pid=,command="],
                text=True,
                errors="replace",
                stderr=subprocess.DEVNULL,
            )
            for line in out.splitlines():
                if needle not in line:
                    continue
                parts = line.strip().split(None, 1)
                if not parts:
                    continue
                try:
                    pid = int(parts[0])
                except ValueError:
                    continue
                if pid != my_pid:
                    found.add(pid)
        except Exception:
            pass

    if not found:
        return []

    # Also include any live descendants (covers SIP-protected /bin/* children
    # on macOS whose environ `ps eww` hides).
    parents = _all_process_parents()
    changed = True
    while changed:
        changed = False
        for pid, ppid in parents.items():
            if pid != my_pid and ppid in found and pid not in found:
                found.add(pid)
                changed = True

    return sorted(found)


def _pid_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
        return True
    except ProcessLookupError:
        return False
    except PermissionError:
        return True


def reap_tagged_processes(tag: str, wait_term_s: float = 2.0, wait_kill_s: float = 1.5) -> list[int]:
    """Send SIGTERM then SIGKILL by PID to every process carrying ORACLE_RUN_TAG=<tag>.
    Returns any PIDs still alive after SIGKILL (empty list on clean sweep)."""
    pids = find_tagged_pids(tag)
    if not pids:
        return []

    for pid in pids:
        try:
            os.kill(pid, signal.SIGTERM)
        except OSError:
            pass

    deadline = time.monotonic() + wait_term_s
    while time.monotonic() < deadline:
        remaining = find_tagged_pids(tag)
        if not remaining:
            return []
        time.sleep(0.05)

    remaining = find_tagged_pids(tag)
    for pid in remaining:
        try:
            os.kill(pid, signal.SIGKILL)
        except OSError:
            pass

    deadline = time.monotonic() + wait_kill_s
    while time.monotonic() < deadline:
        still = find_tagged_pids(tag)
        if not still:
            return []
        time.sleep(0.05)

    return find_tagged_pids(tag)


# ---------------------------------------------------------------------------
# CLI helpers for shell scripts (ansi.sh, run.sh)
# ---------------------------------------------------------------------------

def _cli() -> int:
    import argparse
    import json

    parser = argparse.ArgumentParser(description="Harness isolation utilities")
    sub = parser.add_subparsers(dest="cmd", required=True)

    p_proxy = sub.add_parser("proxy", help="Run refusing proxy listener until SIGTERM/SIGINT")
    p_proxy.add_argument("--url-file", required=True)
    p_proxy.add_argument("--log", required=True)

    p_reap = sub.add_parser("reap", help="Reap all processes carrying ORACLE_RUN_TAG")
    p_reap.add_argument("--tag", required=True)

    p_check = sub.add_parser("check-pids", help="Fail non-zero if any process carries ORACLE_RUN_TAG")
    p_check.add_argument("--tag", required=True)

    p_sb = sub.add_parser("sandbox-exec", help="Exec command under loopback-only sandbox if available")
    p_sb.add_argument("argv", nargs=argparse.REMAINDER)

    sub.add_parser("omp-config-yml", help="Print offline omp config.yml to stdout")

    args = parser.parse_args()
    if args.cmd == "proxy":
        proxy = RefusingProxyServer(log_path=args.log)
        proxy.start()
        tmp = f"{args.url_file}.tmp.{os.getpid()}"
        Path(tmp).write_text(proxy.url + "\n", encoding="utf-8")
        os.replace(tmp, args.url_file)

        stop_evt = threading.Event()
        def _stop(_sig: int, _frame: Any) -> None:
            stop_evt.set()
        signal.signal(signal.SIGTERM, _stop)
        signal.signal(signal.SIGINT, _stop)
        signal.signal(signal.SIGHUP, _stop)
        try:
            while not stop_evt.is_set():
                stop_evt.wait(0.2)
        finally:
            proxy.stop()
        return 0

    if args.cmd == "reap":
        leftovers = reap_tagged_processes(args.tag)
        if leftovers:
            sys.stderr.write(f"error: processes still alive for ORACLE_RUN_TAG={args.tag}: {leftovers}\n")
            return 1
        return 0

    if args.cmd == "check-pids":
        leftovers = find_tagged_pids(args.tag)
        if leftovers:
            sys.stderr.write(f"error: leaked processes for ORACLE_RUN_TAG={args.tag}: {leftovers}\n")
            return 1
        return 0

    if args.cmd == "sandbox-exec":
        argv = list(args.argv)
        if argv and argv[0] == "--":
            argv = argv[1:]
        if not argv:
            sys.stderr.write("error: sandbox-exec needs a command\n")
            return 2
        wrapped = sandbox_cmd(argv)
        os.execvp(wrapped[0], wrapped)


    if args.cmd == "omp-config-yml":
        sys.stdout.write(omp_offline_config_yml())
        return 0
    return 2


if __name__ == "__main__":
    raise SystemExit(_cli())
