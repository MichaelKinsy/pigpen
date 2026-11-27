#!/usr/bin/env python3
"""Capture omp v19.1.1's real advertised tool schemas over loopback HTTP.

Drives the installed `omp` binary with 0 network access (loopback-only macOS
sandbox + refusing proxy + disabled built-in providers) and `PI_NO_INTENT=1`
so `i` is not injected. Runs targeted sessions to elicit every tool oh-my-pig
registers, capturing the exact `tools` array (preserving JSON key order) from
the OpenAI-compatible chat-completions request sent by omp.
"""

from __future__ import annotations

import argparse
import hashlib
import http.server
import json
import os
import shutil
import subprocess
import sys
import tempfile
import threading
import time
import uuid
from pathlib import Path
from typing import Any

sys.dont_write_bytecode = True

SCRIPT_DIR = Path(__file__).resolve().parent
if str(SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPT_DIR))

from harness_isolation import (  # noqa: E402
    RefusingProxyServer,
    apply_proxy_env,
    check_free_disk_space,
    clean_base_env,
    omp_offline_config_yml,
    reap_tagged_processes,
    sandbox_available,
    sandbox_cmd,
)

# Tools oh-my-pig registers (excluding internal eval preludes like browser).
TARGET_TOOLS: tuple[str, ...] = (
    "read",
    "write",
    "edit",
    "grep",
    "glob",
    "find",
    "bash",
    "ask",
    "todo",
    "web_search",
    "eval",
    "task",
    "wait",
    "yield",
    "ast_grep",
    "ast_edit",
    "resolve",
    "reject",
    "propose",
    "goal",
    "checkpoint",
    "rewind",
    "debug",
    "github",
    "lsp",
    "manage_skill",
)


def _extract_tools_ordered(body: dict[str, Any]) -> list[dict[str, Any]]:
    tools_raw = body.get("tools") or []
    parsed: list[dict[str, Any]] = []
    for t in tools_raw:
        fn = t.get("function") if isinstance(t, dict) else None
        if isinstance(fn, dict) and fn.get("name"):
            parsed.append({
                "name": fn["name"],
                "description": fn.get("description", ""),
                "parameters": fn.get("parameters", {}),
            })
    return parsed


def _msg_text(msg: dict[str, Any]) -> str:
    c = msg.get("content", "")
    if isinstance(c, str):
        return c
    if isinstance(c, list):
        parts: list[str] = []
        for item in c:
            if isinstance(item, dict):
                if item.get("type") == "text":
                    parts.append(str(item.get("text", "")))
                elif "text" in item:
                    parts.append(str(item["text"]))
        return "\n".join(parts)
    return str(c)


class CaptureLLMServer:
    """Loopback OpenAI-compatible server that captures advertised tools on every request."""

    def __init__(self, parent_marker: str, parent_calls: list[list[dict[str, Any]]] | None = None) -> None:
        self.parent_marker = parent_marker
        self.parent_calls = parent_calls or []
        self.lock = threading.Lock()
        self.parent_turn = 0
        self.captured_parent: list[list[dict[str, Any]]] = []
        self.captured_child: list[list[dict[str, Any]]] = []
        self._server: http.server.ThreadingHTTPServer | None = None
        self._thread: threading.Thread | None = None
        self.port = 0

    def start(self) -> int:
        parent = self

        class Handler(http.server.BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def log_message(self, fmt: str, *args: Any) -> None:
                pass

            def _send_json(self, code: int, payload: Any) -> None:
                raw = json.dumps(payload).encode("utf-8")
                self.send_response(code)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

            def _stream_sse(
                self,
                model_id: str,
                chunk_id: str,
                *,
                content: str = "",
                tool_calls: list[tuple[str, str, str]] | None = None,
            ) -> None:
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.send_header("Cache-Control", "no-cache")
                self.send_header("Connection", "close")
                self.end_headers()

                def emit(delta: dict[str, Any], finish: str | None) -> bool:
                    chunk = {
                        "id": chunk_id,
                        "object": "chat.completion.chunk",
                        "created": 1,
                        "model": model_id,
                        "choices": [{"index": 0, "delta": delta, "finish_reason": finish}],
                    }
                    try:
                        self.wfile.write(f"data: {json.dumps(chunk)}\n\n".encode("utf-8"))
                        self.wfile.flush()
                        return True
                    except (BrokenPipeError, ConnectionResetError, OSError):
                        return False

                if not emit({"role": "assistant", "content": ""}, None):
                    return
                if content and not emit({"content": content}, None):
                    return
                if tool_calls:
                    for j, (call_id, name, args_str) in enumerate(tool_calls):
                        if not emit(
                            {
                                "tool_calls": [
                                    {
                                        "index": j,
                                        "id": call_id,
                                        "type": "function",
                                        "function": {"name": name, "arguments": args_str},
                                    }
                                ]
                            },
                            None,
                        ):
                            return
                    if not emit({}, "tool_calls"):
                        return
                else:
                    if not emit({}, "stop"):
                        return

                usage_chunk = {
                    "id": chunk_id,
                    "object": "chat.completion.chunk",
                    "created": 1,
                    "model": model_id,
                    "choices": [],
                    "usage": {"prompt_tokens": 100, "completion_tokens": 10, "total_tokens": 110},
                }
                try:
                    self.wfile.write(f"data: {json.dumps(usage_chunk)}\n\n".encode("utf-8"))
                    self.wfile.write(b"data: [DONE]\n\n")
                    self.wfile.flush()
                except (BrokenPipeError, ConnectionResetError, OSError):
                    pass

            def do_GET(self) -> None:
                if self.path.rstrip("/") in ("/v1/models", "/models"):
                    self._send_json(
                        200,
                        {"object": "list", "data": [{"id": "proof-1", "object": "model", "created": 1, "owned_by": "proof"}]},
                    )
                    return
                self._send_json(404, {"error": "not found"})

            def do_POST(self) -> None:
                if not self.path.endswith("/chat/completions"):
                    self._send_json(404, {"error": f"unsupported path {self.path}"})
                    return
                n = int(self.headers.get("Content-Length", "0"))
                raw = self.rfile.read(n) if n > 0 else b"{}"
                # Python 3.7+ json.loads preserves exact key insertion order in dicts.
                body = json.loads(raw.decode("utf-8"))
                model_id = str(body.get("model") or "proof-1")
                messages = body.get("messages") or []
                tools = _extract_tools_ordered(body)

                all_user_text = "\n".join(
                    _msg_text(m) for m in messages if m.get("role") == "user"
                )
                is_parent = parent.parent_marker in all_user_text

                if not is_parent:
                    with parent.lock:
                        cidx = len(parent.captured_child)
                        parent.captured_child.append(tools)
                    # If yield tool is advertised, call it so the subagent finishes cleanly on turn 1.
                    has_yield = any(t["name"] == "yield" for t in tools)
                    if has_yield and cidx == 0:
                        self._stream_sse(
                            model_id,
                            f"child-{cidx}",
                            tool_calls=[(f"child_yield_{cidx}", "yield", json.dumps({"data": {"ok": True}}))],
                        )
                    else:
                        self._stream_sse(model_id, f"child-{cidx}", content="CHILD_DONE")
                    return

                with parent.lock:
                    idx = parent.parent_turn
                    parent.parent_turn += 1
                    parent.captured_parent.append(tools)
                    batch = parent.parent_calls[idx] if idx < len(parent.parent_calls) else []

                if batch:
                    dispatched = [
                        (f"call_{idx}_{j}", str(spec["tool"]), json.dumps(spec.get("arguments", {}), ensure_ascii=False))
                        for j, spec in enumerate(batch)
                    ]
                    self._stream_sse(model_id, f"parent-{idx}", tool_calls=dispatched)
                else:
                    self._stream_sse(model_id, f"parent-done-{idx}", content="CAPTURE_DONE")

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


def _prepare_omp_env(
    base_dir: Path,
    llm_url: str,
    proxy_url: str,
    run_tag: str,
    extra_config_yml: str,
    *,
    shared_natives_dir: Path | None = None,
) -> tuple[str, dict[str, str]]:
    work_dir = base_dir / "work"
    home_dir = base_dir / "home"
    tmp_dir = base_dir / "tmp"
    for d in (
        work_dir,
        home_dir / ".config",
        home_dir / ".local" / "share",
        home_dir / ".local" / "state",
        home_dir / ".cache",
        tmp_dir,
    ):
        d.mkdir(parents=True, exist_ok=True)

    work_real = os.path.realpath(str(work_dir))
    home_real = os.path.realpath(str(home_dir))
    tmp_real = os.path.realpath(str(tmp_dir))

    agent_dir = Path(home_real) / ".omp" / "agent"
    agent_dir.mkdir(parents=True, exist_ok=True)
    (agent_dir / "models.yml").write_text(
        f"""providers:
  proof:
    baseUrl: "{llm_url}"
    apiKey: "proof-key"
    api: "openai-completions"
    models:
      - id: "proof-1"
        name: "Proof 1"
        reasoning: false
        input: ["text"]
        cost:
          input: 0
          output: 0
          cacheRead: 0
          cacheWrite: 0
        contextWindow: 128000
        maxTokens: 4096
""",
        encoding="utf-8",
    )
    config_yml = omp_offline_config_yml(extra_config_yml)
    (agent_dir / "config.yml").write_text(config_yml, encoding="utf-8")

    env = clean_base_env()
    apply_proxy_env(env, proxy_url)
    env.update({
        "HOME": home_real,
        "ORACLE_RUN_TAG": run_tag,
        "PI_NO_INTENT": "1",
        "PI_CODING_AGENT_DIR": str(agent_dir),
        "PI_CODING_AGENT_SESSION_DIR": str(agent_dir / "sessions"),
        "PI_CONFIG_DIR": ".omp",
        "PI_CONFIG_FILES": "",
        "OMP_PROFILE": "",
        "PI_PROFILE": "",
        "OMP_SKIP_SETUP": "1",
        "OMP_AUTORESEARCH_DB_DIR": str(Path(tmp_real) / "autoresearch"),
        "OMP_WORKTREE_DIR": str(Path(tmp_real) / "wt"),
        "OMP_GITHUB_CACHE_DB": str(Path(tmp_real) / "github-cache.db"),
        "CLAUDE_CONFIG_DIR": str(Path(home_real) / ".claude"),
        "COPILOT_HOME": str(Path(home_real) / ".copilot"),
        "PI_NO_TITLE": "1",
        "PI_AI_CODEX_CLIENT_VERSION": "0.1.0",
        "PI_NOTIFICATIONS": "off",
        "OTEL_SDK_DISABLED": "true",
        "XDG_CONFIG_HOME": str(Path(home_real) / ".config"),
        "XDG_DATA_HOME": str(Path(home_real) / ".local" / "share"),
        "XDG_STATE_HOME": str(Path(home_real) / ".local" / "state"),
        "XDG_CACHE_HOME": str(Path(home_real) / ".cache"),
        "TMPDIR": tmp_real,
        "PI_NATIVES_DIR": str(shared_natives_dir) if shared_natives_dir else str(Path(home_real) / ".omp" / "natives"),
        "GIT_TERMINAL_PROMPT": "0",
    })
    return work_real, env


def _base_omp_cmd(omp_bin: str) -> list[str]:
    return [
        omp_bin,
        "--model", "proof/proof-1",
        "--no-session",
        "--offline",
        "--no-extensions",
        "--no-rules",
        "--no-context-files",
        "--no-title",
        "--no-skills",
        "--auto-approve",
    ]


def run_print_session(
    omp_bin: str,
    label: str,
    work_root: Path,
    proxy: RefusingProxyServer,
    run_tag: str,
    extra_config_yml: str,
    *,
    parent_calls: list[list[dict[str, Any]]] | None = None,
    extra_args: list[str] | None = None,
    shared_natives_dir: Path | None = None,
    timeout_s: int = 45,
) -> dict[str, Any]:
    marker = f"[schema-capture:{label}]"
    llm = CaptureLLMServer(marker, parent_calls=parent_calls)
    port = llm.start()
    llm_url = f"http://127.0.0.1:{port}/v1"
    base_dir = work_root / label
    shutil.rmtree(base_dir, ignore_errors=True)
    work_real, env = _prepare_omp_env(
        base_dir, llm_url, proxy.url, run_tag, extra_config_yml, shared_natives_dir=shared_natives_dir
    )
    cmd = _base_omp_cmd(omp_bin) + ["-p", "--mode", "json"]
    if extra_args:
        cmd.extend(extra_args)
    cmd.append(f"{marker} Capture tool schemas.")
    exec_cmd = sandbox_cmd(cmd)
    try:
        sp = subprocess.run(
            exec_cmd,
            cwd=work_real,
            env=env,
            capture_output=True,
            text=True,
            timeout=timeout_s,
        )
        rc = sp.returncode
        stderr = sp.stderr
    except subprocess.TimeoutExpired as exc:
        rc = 124
        stderr = (exc.stderr or "") if isinstance(exc.stderr, str) else ""
    finally:
        llm.stop()
        reap_tagged_processes(run_tag)
    return {
        "label": label,
        "rc": rc,
        "stderr": stderr[-2000:],
        "parent": llm.captured_parent,
        "child": llm.captured_child,
    }


def run_rpc_session(
    omp_bin: str,
    label: str,
    work_root: Path,
    proxy: RefusingProxyServer,
    run_tag: str,
    extra_config_yml: str,
    commands: list[dict[str, Any]],
    *,
    mode: str = "rpc",
    shared_natives_dir: Path | None = None,
    timeout_s: int = 45,
) -> dict[str, Any]:
    marker = f"[schema-capture:{label}]"
    llm = CaptureLLMServer(marker)
    port = llm.start()
    llm_url = f"http://127.0.0.1:{port}/v1"
    base_dir = work_root / label
    shutil.rmtree(base_dir, ignore_errors=True)
    work_real, env = _prepare_omp_env(
        base_dir, llm_url, proxy.url, run_tag, extra_config_yml, shared_natives_dir=shared_natives_dir
    )
    cmd = _base_omp_cmd(omp_bin) + ["--mode", mode]
    exec_cmd = sandbox_cmd(cmd)

    proc = subprocess.Popen(
        exec_cmd,
        cwd=work_real,
        env=env,
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    responses: list[dict[str, Any]] = []
    stderr_lines: list[str] = []

    def _read_stderr() -> None:
        assert proc.stderr is not None
        for line in proc.stderr:
            stderr_lines.append(line)

    t_err = threading.Thread(target=_read_stderr, daemon=True)
    t_err.start()

    try:
        assert proc.stdin is not None and proc.stdout is not None
        deadline = time.monotonic() + timeout_s
        ready = False
        while time.monotonic() < deadline:
            line = proc.stdout.readline()
            if not line:
                break
            line = line.strip()
            if not line:
                continue
            try:
                msg = json.loads(line)
            except json.JSONDecodeError:
                continue
            if msg.get("type") == "ready":
                ready = True
                break
        if ready:
            for c in commands:
                if c.get("type") == "prompt" and "message" in c:
                    c = dict(c)
                    c["message"] = f"{marker} {c['message']}"
                proc.stdin.write(json.dumps(c) + "\n")
                proc.stdin.flush()
                want_id = c.get("id")
                wait_prompt_done = c.get("type") == "prompt"
                got_resp = want_id is None
                got_done = not wait_prompt_done
                while time.monotonic() < deadline and not (got_resp and got_done):
                    line = proc.stdout.readline()
                    if not line:
                        break
                    line = line.strip()
                    if not line:
                        continue
                    try:
                        msg = json.loads(line)
                    except json.JSONDecodeError:
                        continue
                    if msg.get("type") == "response" and msg.get("id") == want_id:
                        responses.append(msg)
                        got_resp = True
                    elif msg.get("type") in ("prompt_result", "agent_end"):
                        got_done = True
            proc.stdin.close()
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.terminate()
            try:
                proc.wait(timeout=3)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=2)
    finally:
        llm.stop()
        reap_tagged_processes(run_tag)
        t_err.join(timeout=1.0)

    return {
        "label": label,
        "rc": proc.returncode,
        "stderr": "".join(stderr_lines)[-2000:],
        "responses": responses,
        "parent": llm.captured_parent,
        "child": llm.captured_child,
    }


def run_acp_session(
    omp_bin: str,
    label: str,
    work_root: Path,
    proxy: RefusingProxyServer,
    run_tag: str,
    extra_config_yml: str,
    *,
    shared_natives_dir: Path | None = None,
    timeout_s: int = 45,
) -> dict[str, Any]:
    marker = f"[schema-capture:{label}]"
    llm = CaptureLLMServer(marker)
    port = llm.start()
    llm_url = f"http://127.0.0.1:{port}/v1"
    base_dir = work_root / label
    shutil.rmtree(base_dir, ignore_errors=True)
    work_real, env = _prepare_omp_env(
        base_dir, llm_url, proxy.url, run_tag, extra_config_yml, shared_natives_dir=shared_natives_dir
    )
    cmd = _base_omp_cmd(omp_bin) + ["--mode", "acp"]
    exec_cmd = sandbox_cmd(cmd)
    proc = subprocess.Popen(
        exec_cmd,
        cwd=work_real,
        env=env,
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    stderr_lines: list[str] = []

    def _read_stderr() -> None:
        assert proc.stderr is not None
        for line in proc.stderr:
            stderr_lines.append(line)

    t_err = threading.Thread(target=_read_stderr, daemon=True)
    t_err.start()
    rpc_log: list[dict[str, Any]] = []

    def send_rpc(req_id: int, method: str, params: dict[str, Any]) -> dict[str, Any] | None:
        assert proc.stdin is not None and proc.stdout is not None
        frame = {"jsonrpc": "2.0", "id": req_id, "method": method, "params": params}
        proc.stdin.write(json.dumps(frame) + "\n")
        proc.stdin.flush()
        deadline = time.monotonic() + timeout_s
        while time.monotonic() < deadline:
            line = proc.stdout.readline()
            if not line:
                return None
            line = line.strip()
            if not line:
                continue
            try:
                msg = json.loads(line)
            except json.JSONDecodeError:
                continue
            if msg.get("id") == req_id:
                rpc_log.append(msg)
                return msg
        return None

    try:
        init_res = send_rpc(1, "initialize", {"protocolVersion": 1, "clientCapabilities": {}})
        new_res = send_rpc(2, "session/new", {"cwd": work_real, "mcpServers": []})
        session_id = ((new_res or {}).get("result") or {}).get("sessionId")
        if session_id:
            send_rpc(3, "session/set_mode", {"sessionId": session_id, "modeId": "plan"})
            send_rpc(
                4,
                "session/prompt",
                {
                    "sessionId": session_id,
                    "prompt": [{"type": "text", "text": f"{marker} Capture plan mode tools."}],
                },
            )
        assert proc.stdin is not None
        proc.stdin.close()
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.terminate()
            try:
                proc.wait(timeout=3)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=2)
    finally:
        llm.stop()
        reap_tagged_processes(run_tag)
        t_err.join(timeout=1.0)

    return {
        "label": label,
        "rc": proc.returncode,
        "stderr": "".join(stderr_lines)[-2000:],
        "rpc_log": rpc_log,
        "parent": llm.captured_parent,
        "child": llm.captured_child,
    }


def run_pty_plan_session(
    omp_bin: str,
    label: str,
    work_root: Path,
    proxy: RefusingProxyServer,
    run_tag: str,
    extra_config_yml: str,
    *,
    shared_natives_dir: Path | None = None,
    timeout_s: int = 30,
) -> dict[str, Any]:
    """Run omp in interactive mode under a PTY with plan.defaultOnStartup=true and an initial message."""
    import pty as pty_mod
    import select

    marker = f"[schema-capture:{label}]"
    llm = CaptureLLMServer(marker)
    port = llm.start()
    llm_url = f"http://127.0.0.1:{port}/v1"
    base_dir = work_root / label
    shutil.rmtree(base_dir, ignore_errors=True)
    work_real, env = _prepare_omp_env(
        base_dir, llm_url, proxy.url, run_tag, extra_config_yml, shared_natives_dir=shared_natives_dir
    )
    env["TERM"] = "xterm-256color"
    cmd = _base_omp_cmd(omp_bin) + [f"{marker} Capture plan mode propose tool."]
    exec_cmd = sandbox_cmd(cmd)
    master_fd, slave_fd = pty_mod.openpty()
    proc = subprocess.Popen(
        exec_cmd,
        cwd=work_real,
        env=env,
        stdin=slave_fd,
        stdout=slave_fd,
        stderr=slave_fd,
        close_fds=True,
    )
    os.close(slave_fd)
    chunks: list[bytes] = []
    try:
        deadline = time.monotonic() + timeout_s
        while time.monotonic() < deadline:
            with llm.lock:
                if llm.captured_parent or llm.captured_child:
                    break
            if proc.poll() is not None:
                break
            r, _, _ = select.select([master_fd], [], [], 0.1)
            if r:
                try:
                    data = os.read(master_fd, 4096)
                    if data:
                        chunks.append(data)
                    else:
                        break
                except OSError:
                    break
        # Give omp a brief moment to finish reading the SSE reply, then terminate cleanly.
        time.sleep(0.2)
        if proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=3)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=2)
    finally:
        try:
            os.close(master_fd)
        except OSError:
            pass
        llm.stop()
        reap_tagged_processes(run_tag)
    return {
        "label": label,
        "rc": proc.returncode,
        "stderr": "",
        "parent": llm.captured_parent,
        "child": llm.captured_child,
    }

def _record_tools(
    out_tools: dict[str, dict[str, Any]],
    tool_list: list[dict[str, Any]],
    session_label: str,
    *,
    prefer_replace: set[str] | None = None,
) -> None:
    for t in tool_list:
        name = t["name"]
        if name not in TARGET_TOOLS:
            continue
        if name in out_tools and (not prefer_replace or name not in prefer_replace):
            continue
        out_tools[name] = {
            "source": "captured",
            "session": session_label,
            "description_len": len(t.get("description", "")),
            "parameters": t.get("parameters", {}),
        }


def main() -> int:
    parser = argparse.ArgumentParser(description="Capture omp v19.1.1 advertised tool schemas")
    parser.add_argument("--omp-bin", default=shutil.which("omp") or "omp")
    parser.add_argument("--out", default=str(SCRIPT_DIR / "captured.json"))
    parser.add_argument("--schemas-out", default="", help="Write omp_schemas.json to this path")
    parser.add_argument("--manifest-out", default="", help="Write omp_schemas.manifest.json to this path")
    parser.add_argument("--omp-tag", default="v19.1.1")
    parser.add_argument("--omp-commit", default="31ffee513f27a01bba7d4dfbe12bae239cdc646a")
    parser.add_argument("--timeout", type=int, default=45)
    args = parser.parse_args()
    work_root = Path(tempfile.mkdtemp(prefix="omp-schema-capture-", dir=str(SCRIPT_DIR)))
    check_free_disk_space(work_root)

    # Reuse host natives cache if present so omp does not spend time extracting.
    shared_natives_dir = work_root / "natives"
    shared_natives_dir.mkdir(parents=True, exist_ok=True)

    proxy = RefusingProxyServer()
    proxy.start()
    run_tag = f"schema-cap-{uuid.uuid4().hex[:10]}"

    all_top_level_yml = """
tools:
  commands: false
  intentTracing: false
astGrep:
  enabled: true
astEdit:
  enabled: true
github:
  enabled: true
debug:
  enabled: true
checkpoint:
  enabled: true
lsp:
  enabled: true
find:
  enabled: "on"
goal:
  enabled: true
ask:
  enabled: true
autolearn:
  enabled: true
"""

    yield_top_level_yml = all_top_level_yml + """
task:
  structured: true
"""

    sessions_summary: list[dict[str, Any]] = []
    captured: dict[str, dict[str, Any]] = {}

    try:
        # 1. Print session with every built-in tool enabled (default task.structured=false)
        #    and tools.commands=false.
        s1 = run_print_session(
            args.omp_bin,
            "all-top-level",
            work_root,
            proxy,
            run_tag,
            all_top_level_yml,
            shared_natives_dir=shared_natives_dir,
            timeout_s=args.timeout,
        )
        sessions_summary.append({
            "label": s1["label"],
            "rc": s1["rc"],
            "parent_turns": len(s1["parent"]),
            "parent_tools": [t["name"] for t in (s1["parent"][0] if s1["parent"] else [])],
            "stderr_tail": s1["stderr"][-500:],
        })
        if s1["parent"]:
            _record_tools(captured, s1["parent"][0], s1["label"])

        # 2. Structured subagent spawn to capture `yield` on the child's wire request.
        #    Follow task with a `wait` call so the parent waits for the background subagent.
        s_yield = run_print_session(
            args.omp_bin,
            "subagent-yield",
            work_root,
            proxy,
            run_tag,
            yield_top_level_yml,
            parent_calls=[
                [
                    {
                        "tool": "task",
                        "arguments": {
                            "context": "Capture yield schema.",
                            "tasks": [
                                {
                                    "name": "YieldChild",
                                    "agent": "task",
                                    "task": "Return ok via yield.",
                                    "solutionSpace": "single answer",
                                    "outputSchema": True,
                                }
                            ],
                        },
                    }
                ],
                [{"tool": "wait", "arguments": {}}],
            ],
            shared_natives_dir=shared_natives_dir,
            timeout_s=args.timeout,
        )
        all_child_tool_names = []
        for turn_tools in s_yield["child"]:
            names = [t["name"] for t in turn_tools]
            all_child_tool_names.append(names)
            _record_tools(captured, turn_tools, f"{s_yield['label']}:child", prefer_replace={"yield"})
        sessions_summary.append({
            "label": s_yield["label"],
            "rc": s_yield["rc"],
            "parent_turns": len(s_yield["parent"]),
            "child_turns": len(s_yield["child"]),
            "parent_tools": [t["name"] for t in (s_yield["parent"][0] if s_yield["parent"] else [])],
            "child_tools_per_turn": all_child_tool_names,
            "stderr_tail": s_yield["stderr"][-500:],
        })
        # 3. RPC-UI session: hasUI=true -> `ask` advertised, and `goal op=create` -> `goal` advertised.
        s2 = run_rpc_session(
            args.omp_bin,
            "rpc-ui-ask-goal",
            work_root,
            proxy,
            run_tag,
            all_top_level_yml,
            mode="rpc-ui",
            commands=[
                {"id": "1", "type": "set_ask_dialog", "enabled": True},
                {"id": "2", "type": "goal", "op": "create", "objective": "Capture goal tool schema"},
                {"id": "3", "type": "prompt", "message": "Capture RPC-UI tools with ask and goal."},
            ],
            shared_natives_dir=shared_natives_dir,
            timeout_s=args.timeout,
        )
        sessions_summary.append({
            "label": s2["label"],
            "rc": s2["rc"],
            "parent_turns": len(s2["parent"]),
            "responses": s2.get("responses"),
            "parent_tools": [t["name"] for t in (s2["parent"][0] if s2["parent"] else [])],
            "stderr_tail": s2["stderr"][-500:],
        })
        if s2["parent"]:
            _record_tools(captured, s2["parent"][0], s2["label"], prefer_replace={"ask", "goal"})
        if s2["child"]:
            _record_tools(captured, s2["child"][0], f"{s2['label']}:other", prefer_replace={"ask", "goal"})
        # 4. Interactive session via PTY with plan.defaultOnStartup=true so plan mode
        #    runs `#enterPlanMode` -> `setActiveToolsByName` and exposes `propose` top-level.
        plan_startup_yml = all_top_level_yml + """
plan:
  enabled: true
  defaultOnStartup: true
"""
        s3 = run_pty_plan_session(
            args.omp_bin,
            "pty-plan-mode",
            work_root,
            proxy,
            run_tag,
            plan_startup_yml,
            shared_natives_dir=shared_natives_dir,
            timeout_s=args.timeout,
        )
        sessions_summary.append({
            "label": s3["label"],
            "rc": s3["rc"],
            "parent_turns": len(s3["parent"]),
            "parent_tools": [t["name"] for t in (s3["parent"][0] if s3["parent"] else [])],
            "stderr_tail": s3["stderr"][-500:],
        })
        if s3["parent"]:
            _record_tools(captured, s3["parent"][0], s3["label"], prefer_replace={"propose"})
        if s3["child"]:
            _record_tools(captured, s3["child"][0], f"{s3['label']}:other", prefer_replace={"propose"})
    finally:
        net_attempts = list(proxy.attempts)
        proxy.stop()
        leaked = reap_tagged_processes(run_tag)
        shutil.rmtree(work_root, ignore_errors=True)

    # Check if s2's bash differed from s1's bash (e.g. pty property).
    bash_variants: dict[str, Any] = {}
    for s in (s1, s2):
        if s["parent"]:
            for t in s["parent"][0]:
                if t["name"] == "bash":
                    bash_variants[s["label"]] = t["parameters"]

    # Order captured dict by TARGET_TOOLS for determinism.
    ordered_tools: dict[str, Any] = {}
    missing: list[str] = []
    for name in TARGET_TOOLS:
        if name in captured:
            ordered_tools[name] = captured[name]
        else:
            missing.append(name)

    report = {
        "sandbox": "macos-sandbox-exec-loopback-only" if sandbox_available() else "none",
        "network_attempts": net_attempts,
        "leaked_pids": leaked,
        "missing_tools": missing,
        "sessions": sessions_summary,
        "bash_variants": bash_variants,
        "tools": ordered_tools,
    }
    out_path = Path(args.out)
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")

    if args.schemas_out:
        schemas_doc: dict[str, Any] = {}
        for name, entry in ordered_tools.items():
            schemas_doc[name] = {
                "source": entry["source"],
                "description_len": entry["description_len"],
                "parameters": entry["parameters"],
            }
        schemas_bytes = (json.dumps(schemas_doc, indent=2, ensure_ascii=False) + "\n").encode("utf-8")
        schemas_path = Path(args.schemas_out)
        schemas_path.parent.mkdir(parents=True, exist_ok=True)
        schemas_path.write_bytes(schemas_bytes)
        if args.manifest_out:
            omp_ver = ""
            try:
                sp = subprocess.run([args.omp_bin, "--version"], capture_output=True, text=True, timeout=5)
                omp_ver = sp.stdout.strip()
            except Exception:
                pass
            manifest = {
                "generator": "internal/schemaorder/gen/capture.py",
                "ompVersion": omp_ver or "omp/19.1.1",
                "ompTag": args.omp_tag,
                "ompCommit": args.omp_commit,
                "toolCount": len(schemas_doc),
                "capturedCount": sum(1 for v in schemas_doc.values() if v["source"] == "captured"),
                "derivedCount": sum(1 for v in schemas_doc.values() if v["source"] != "captured"),
                "networkAttempts": len(net_attempts),
                "leakedPids": len(leaked),
                "output": {
                    "file": schemas_path.name,
                    "bytes": len(schemas_bytes),
                    "sha256": hashlib.sha256(schemas_bytes).hexdigest(),
                },
            }
            manifest_path = Path(args.manifest_out)
            manifest_path.parent.mkdir(parents=True, exist_ok=True)
            manifest_path.write_text(json.dumps(manifest, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")

    print(f"Captured {len(ordered_tools)}/{len(TARGET_TOOLS)} tools -> {out_path}")
    print(f"Network attempts: {len(net_attempts)}, Leaked PIDs: {len(leaked)}")
    if missing:
        print(f"Missing: {missing}")
    for s in sessions_summary:
        print(f"  [{s['label']}] rc={s['rc']} parent_tools={s.get('parent_tools')} child_tools={s.get('child_tools_per_turn')}")
    return 0 if not missing and len(net_attempts) == 0 and len(leaked) == 0 else 1

if __name__ == "__main__":
    raise SystemExit(main())
