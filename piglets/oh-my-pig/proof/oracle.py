#!/usr/bin/env python3
"""Per-tool oracle comparing omp (v19.1.1) and oh-my-pig via a loopback LLM.

Runs each case in a JSON file (or directory of JSON files) in a fresh isolated
workspace + temp HOME/config directory against a loopback OpenAI-compatible
server that replays each case's scripted tool calls (single or parallel) and
captures:
  - exact tool result text (and follow-up messages added between parent turns,
    such as `<system-reminder>`, `<system-notice>` background completions, and
    attached vision images),
  - `--mode json` structured `tool_execution_end` events (`isError`, `details`),
  - advertised tool list in exact order (names, descriptions, JSON schemas with
    property order preserved) for every distinct toolset/prompt observed,
  - parent system prompt and first subagent child's system prompt & tools,
  - any non-loopback network attempt caught by the refusing proxy listener.

Never uses a real model, never hits the network (refusing proxy + macOS
`sandbox-exec` loopback-only profile where available), never touches the user's
real HOME, deletes per-case scratch immediately, shares one `PI_NATIVES_DIR`
removed on exit, and reaps every descendant process tagged with
`ORACLE_RUN_TAG` on every exit path and SIGTERM/SIGINT/SIGHUP.
"""

from __future__ import annotations

import argparse
import base64
import datetime
import difflib
import fnmatch
import http.server
import json
import os
import re
import shutil
import signal
import subprocess
import sys
import threading
import time
import uuid
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

# Avoid writing __pycache__ litter into the git worktree
sys.dont_write_bytecode = True

SCRIPT_DIR = Path(__file__).resolve().parent
if str(SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPT_DIR))

from allowlist_refs import resolve_allowlist_entries  # noqa: E402
from harness_isolation import (  # noqa: E402
    RefusingProxyServer,
    apply_proxy_env,
    augment_tool_path,
    check_free_disk_space,
    clean_base_env,
    dir_size_bytes,
    find_tagged_pids,
    find_working_tool,
    format_bytes,
    omp_offline_config_yml,
    pig_offline_config_yml,
    reap_tagged_processes,
    sandbox_available,
    sandbox_cmd,
)

# ---------------------------------------------------------------------------
# Narrow, named normalizations (every listed rule has matching code below)
# ---------------------------------------------------------------------------

NORMALIZATIONS: list[tuple[str, str]] = [
    ("temp_paths", "Replace each side's macOS `/private`-stripped alias with `<WORKSPACE>`, `<HOME>`, `<TMPDIR>` and canonical `/private/...` paths with `/private<WORKSPACE>`, `/private<HOME>`, `/private<TMPDIR>` (side-aware: a side that fails to strip `/private` where omp strips it differs)"),
    ("local_fixture_port", "Replace the ephemeral local fixture HTTP server port on 127.0.0.1/localhost with <FIXTURE_PORT>"),
    ("wall_time", "Replace the numeric duration in `Wall time: <N> seconds` with `Wall time: <DURATION> seconds` (never silently strip one-sided wall-time notices; one-sided notices classify as `timing` only within the narrow threshold band and when no details differ)"),
    ("duration_attr", "Replace XML `duration=\"<N>(ms|s|m|us|µs)\"` attributes with `duration=\"<DURATION <unit>>\"` preserving the unit"),
    ("proc_duration", "Replace `completed in <N>(ms|s)`, `failed in <N>(ms|s)`, `cancelled in <N>(ms|s)`, `running up <N>(ms|s)`, and `— up <N>(ms|s)` in proc:// / job output with `<DURATION <unit>>` preserving the unit"),
    ("service_pid", "Replace service/process status `pid=<N>` and `— pid <N>` with `<PID>`"),
    ("find_timing", "Replace `find` footer `<N>(ms|s|m) wall / <N>(ms|s|m) api` with `<DURATION <u1>> wall / <DURATION <u2>> api` preserving units"),
    ("today_date", "Replace today's calendar date (UTC and local) in system reminders/headers with <TODAY>"),
    ("iso_timestamps", "Replace ISO-8601 timestamps (with fractional seconds / zone) with <TIMESTAMP>"),
    ("task_dir_hex", "Replace `omp-task-<16-hex>` temp directories with `omp-task-<TASK_HEX>`"),
    ("daemon_service_hex", "Replace `daemon/services/<16-hex>/` temp paths with `daemon/services/<SVC_HEX>/`"),
    ("session_uuids", "Replace non-nil RFC-4122 UUIDs with <UUID> (nil UUID 00000000-0000-0000-0000-000000000000 is preserved)"),
]

# Type-aware details key normalizations (documented in README.md and report.md):
#   - Numeric-only keys: masked ONLY when value is int/float (and NOT bool), never str/None/dict/list.
#   - pids: masked ONLY when value is a list of ints (and not bools).
#   - startedAt: masked as <STARTEDAT> when int/float (epoch ms) OR normalized ISO-8601 timestamp string
#     (checked via _RE_ISO_TS), never arbitrary strings/None/bool.
#   - Trace-only keys (ns, t_ns, dur_ns, end_ns, at, seq, end_seq, event_seq, run): masked ONLY inside
#     a `trace` subtree (and numeric-only except `run` object/int inside `trace`).
#   - wallTimeNoticeEmitted is NOT masked (it is a behavioural boolean).
#   - Rate objects `{label: "lines/s"|"bytes/s", text: <numeric string>}`: masked to `<RATE>` ONLY when
#     `text` matches a finite numeric rate string (never NaN/arbitrary text).
DETAILS_NUMERIC_MASK_KEYS: frozenset[str] = frozenset({
    "durationMs",
    "totalDurationMs",
    "duration",
    "elapsedMs",
    "elapsed",
    "wallTimeMs",
    "apiMs",
    "startedAt",
    "finishedAt",
    "updatedAt",
    "createdAt",
    "exitedAt",
    "readyAt",
    "settledAtMs",
    "timestamp",
    "pid",
})

DETAILS_TRACE_NUMERIC_KEYS: frozenset[str] = frozenset({
    "ns",
    "t_ns",
    "dur_ns",
    "end_ns",
    "seq",
    "end_seq",
    "event_seq",
    "at",
})

DETAILS_ISO_OR_NUMERIC_KEYS: frozenset[str] = frozenset({
    "startedAt",
    "finishedAt",
    "updatedAt",
    "createdAt",
    "exitedAt",
    "readyAt",
    "timestamp",
})

_RE_RATE_TEXT = re.compile(r"^\d+(?:\.\d+)?[kMG]?$")

_RE_ISO_TS = re.compile(r"\b\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})\b")
_RE_ISO_TS_FULL = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})$")
_RE_UUID = re.compile(r"\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b")
_NIL_UUID = "00000000-0000-0000-0000-000000000000"
_RE_TASK_HEX = re.compile(r"\bomp-task-[0-9a-f]{16}\b")
_RE_DAEMON_SVC_HEX = re.compile(r"(daemon/services/)[0-9a-f]{16}(/)")
_RE_WALL_TIME = re.compile(r"(?m)^Wall time: (\d+(?:\.\d+)?) seconds$")
_RE_WALL_TIME_NORM = re.compile(r"(?m)^Wall time: <DURATION> seconds$")
_RE_DURATION_ATTR = re.compile(r'(duration=")(?:[0-9]+(?:\.[0-9]+)?)(ms|s|m|us|µs)(")')
_RE_PROC_DURATION = re.compile(r"\b(completed in |failed in |cancelled in |running up )(\d+(?:\.\d+)?)(ms|s)\b")
_RE_PROC_UP = re.compile(r"(— up )(\d+(?:\.\d+)?)(ms|s)\b")
_RE_SERVICE_PID_EQ = re.compile(r"\bpid=\d+\b")
_RE_PROC_PID_DASH = re.compile(r"— pid \d+\b")
_RE_FIND_TIMING = re.compile(r"\b\d+(?:\.\d+)?(ms|s|m) wall / \d+(?:\.\d+)?(ms|s|m) api\b")

WALL_TIME_NOTICE_MIN_MS = 1000.0
WALL_TIME_TIMING_MAX_S = 1.3


def _macos_unprivate_alias(path_val: str) -> str | None:
    """Match omp's standardizeMacOSPath (utils/dirs.ts:148-158): strip /private when realpaths agree."""
    if sys.platform != "darwin" or not path_val.startswith("/private/"):
        return None
    stripped = path_val[len("/private"):]
    try:
        if os.path.realpath(path_val) == os.path.realpath(stripped):
            return stripped
    except OSError:
        pass
    return None


def _replace_non_nil_uuid(m: re.Match[str]) -> str:
    return m.group(0) if m.group(0).lower() == _NIL_UUID else "<UUID>"


def normalize_text(
    text: str,
    *,
    work_dir: str,
    home_dir: str,
    tmp_dir: str,
    fixture_port: int | None = None,
) -> tuple[str, list[str]]:
    """Normalize only inherently run-specific values; return (normalized, applied_rule_names)."""
    applied: list[str] = []
    out = text

    def sub_literal(old: str, repl: str, rule: str) -> None:
        nonlocal out
        if old and old in out:
            out = out.replace(old, repl)
            if rule not in applied:
                applied.append(rule)

    # Side-aware macOS `/private` path normalization:
    # On macOS, when `path_val` is `/private/...` and its `/private`-stripped alias resolves
    # to the same realpath (matching omp's `standardizeMacOSPath`, utils/dirs.ts:148-158),
    # map the unstripped `/private/...` spelling to `/private<TOKEN>` and the stripped
    # `/tmp/...` spelling to `<TOKEN>`. Both sides emitting the stripped path match as `<TOKEN>`,
    # both emitting `/private/...` match as `/private<TOKEN>`, while one side failing to strip
    # `/private` where the other strips it visibly differs (`/private<WORKSPACE>` vs `<WORKSPACE>`).
    for label, path_val in (("<WORKSPACE>", work_dir), ("<HOME>", home_dir), ("<TMPDIR>", tmp_dir)):
        alias = _macos_unprivate_alias(path_val)
        if alias:
            sub_literal(path_val, f"/private{label}", "temp_paths")
            sub_literal(alias, label, "temp_paths")
        else:
            sub_literal(path_val, label, "temp_paths")
    if fixture_port is not None:
        sub_literal(f"127.0.0.1:{fixture_port}", "127.0.0.1:<FIXTURE_PORT>", "local_fixture_port")
        sub_literal(f"localhost:{fixture_port}", "localhost:<FIXTURE_PORT>", "local_fixture_port")

    def sub_re(pattern: re.Pattern[str], repl: Any, rule: str) -> None:
        nonlocal out
        new_out, n = pattern.subn(repl, out)
        if n > 0:
            out = new_out
            if rule not in applied:
                applied.append(rule)

    sub_re(_RE_ISO_TS, "<TIMESTAMP>", "iso_timestamps")
    # Replace only today's calendar date (never arbitrary dates in file content)
    today_local = datetime.date.today().isoformat()
    today_utc = datetime.datetime.now(datetime.timezone.utc).date().isoformat()
    for td in sorted({today_local, today_utc}):
        sub_literal(td, "<TODAY>", "today_date")

    sub_re(_RE_TASK_HEX, "omp-task-<TASK_HEX>", "task_dir_hex")
    sub_re(_RE_DAEMON_SVC_HEX, r"\1<SVC_HEX>\2", "daemon_service_hex")
    # Replace non-nil UUIDs only (preserve nil UUID 00000000-0000-0000-0000-000000000000)
    new_out = _RE_UUID.sub(_replace_non_nil_uuid, out)
    if new_out != out:
        out = new_out
        if "session_uuids" not in applied:
            applied.append("session_uuids")
    sub_re(_RE_WALL_TIME, "Wall time: <DURATION> seconds", "wall_time")
    sub_re(_RE_DURATION_ATTR, r"\1<DURATION \2>\3", "duration_attr")
    sub_re(_RE_PROC_DURATION, r"\1<DURATION \3>", "proc_duration")
    sub_re(_RE_PROC_UP, r"\1<DURATION \3>", "proc_duration")
    sub_re(_RE_SERVICE_PID_EQ, "pid=<PID>", "service_pid")
    sub_re(_RE_PROC_PID_DASH, "— pid <PID>", "service_pid")
    sub_re(_RE_FIND_TIMING, r"<DURATION \1> wall / <DURATION \2> api", "find_timing")

    return out, applied


def _is_number(v: Any) -> bool:
    return isinstance(v, (int, float)) and not isinstance(v, bool)


def normalize_obj(
    obj: Any,
    *,
    work_dir: str,
    home_dir: str,
    tmp_dir: str,
    fixture_port: int | None = None,
    _in_trace: bool = False,
) -> Any:
    """Recursively normalize strings and type-checked volatile fields inside a JSON-like structure."""
    if isinstance(obj, str):
        norm, _ = normalize_text(
            obj, work_dir=work_dir, home_dir=home_dir, tmp_dir=tmp_dir, fixture_port=fixture_port
        )
        return norm
    if isinstance(obj, list):
        return [
            normalize_obj(
                x,
                work_dir=work_dir,
                home_dir=home_dir,
                tmp_dir=tmp_dir,
                fixture_port=fixture_port,
                _in_trace=_in_trace,
            )
            for x in obj
        ]
    if isinstance(obj, dict):
        if (
            obj.get("label") in ("lines/s", "bytes/s")
            and isinstance(obj.get("text"), str)
            and _RE_RATE_TEXT.match(obj["text"].strip())
        ):
            return {**obj, "text": "<RATE>"}
        out: dict[str, Any] = {}
        for k, v in obj.items():
            child_in_trace = _in_trace or (k == "trace")
            if k == "pids" and isinstance(v, list) and all(isinstance(x, int) and not isinstance(x, bool) for x in v):
                out[k] = "<PIDS>"
            elif k in DETAILS_ISO_OR_NUMERIC_KEYS and isinstance(v, str) and _RE_ISO_TS_FULL.match(v):
                out[k] = "<TIMESTAMP>"
            elif k in DETAILS_NUMERIC_MASK_KEYS and _is_number(v):
                out[k] = f"<{k.upper()}>"
            elif _in_trace and k in DETAILS_TRACE_NUMERIC_KEYS and _is_number(v):
                out[k] = f"<{k.upper()}>"
            elif _in_trace and k == "run" and _is_number(v):
                out[k] = "<RUN>"
            else:
                out[k] = normalize_obj(
                    v,
                    work_dir=work_dir,
                    home_dir=home_dir,
                    tmp_dir=tmp_dir,
                    fixture_port=fixture_port,
                    _in_trace=child_in_trace,
                )
        return out
    return obj

# ---------------------------------------------------------------------------
# Local fixture HTTP server (for offline URL read tests)
# ---------------------------------------------------------------------------

def _build_large_html_fixture() -> bytes:
    """Build a >2.5 KB deterministic HTML article so omp's native HTML->MD converter runs."""
    paragraphs = "\n".join(
        f"<p>Section {i}: This deterministic local HTML fixture paragraph contains enough "
        f"prose for the native HTML-to-Markdown reader backend to clear its minimum content "
        f"threshold without falling back to external readers or raw HTML. Key token: FIXTURE_PARA_{i}.</p>"
        for i in range(1, 16)
    )
    html = (
        "<!doctype html>\n"
        "<html lang=\"en\">\n"
        "<head><meta charset=\"utf-8\"><title>Oracle Local HTML Fixture</title></head>\n"
        "<body>\n"
        "<article>\n"
        "<h1>Oracle Local HTML Fixture</h1>\n"
        "<p>Lead summary sentence for the offline URL reader test.</p>\n"
        "<h2>Details</h2>\n"
        f"{paragraphs}\n"
        "<ul><li>Alpha bullet item</li><li>Beta bullet item</li></ul>\n"
        "</article>\n"
        "</body>\n"
        "</html>\n"
    )
    return html.encode("utf-8")


_LARGE_HTML_BYTES = _build_large_html_fixture()


class FixtureHTTPServer:
    def __init__(self, work_dir: str) -> None:
        self.work_dir = work_dir
        self._server: http.server.ThreadingHTTPServer | None = None
        self._thread: threading.Thread | None = None
        self.port: int = 0

    def start(self) -> int:
        work_dir = self.work_dir

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, fmt: str, *args: Any) -> None:
                pass

            def do_GET(self) -> None:
                if self.path == "/hello.txt":
                    body = b"Hello from local fixture server.\nLine two.\n"
                    self.send_response(200)
                    self.send_header("Content-Type", "text/plain; charset=utf-8")
                    self.send_header("Content-Length", str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
                    return
                if self.path == "/page.html":
                    body = _LARGE_HTML_BYTES
                    self.send_response(200)
                    self.send_header("Content-Type", "text/html; charset=utf-8")
                    self.send_header("Content-Length", str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
                    return
                if self.path == "/data.json":
                    body = json.dumps({"ok": True, "items": ["alpha", "beta"]}).encode("utf-8")
                    self.send_response(200)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
                    return
                rel = self.path.lstrip("/")
                if rel and ".." not in rel:
                    fpath = os.path.join(work_dir, "http", rel)
                    if os.path.isfile(fpath):
                        with open(fpath, "rb") as fh:
                            body = fh.read()
                        self.send_response(200)
                        self.send_header("Content-Type", "text/plain; charset=utf-8")
                        self.send_header("Content-Length", str(len(body)))
                        self.end_headers()
                        self.wfile.write(body)
                        return
                self.send_response(404)
                self.send_header("Content-Type", "text/plain")
                self.end_headers()
                self.wfile.write(b"not found\n")

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
# Oracle loopback LLM server
# ---------------------------------------------------------------------------

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
                elif item.get("type") == "image_url":
                    url = str((item.get("image_url") or {}).get("url") or "")
                    if url.startswith("data:"):
                        header = url.split(",", 1)[0]
                        b64_len = len(url.split(",", 1)[1]) if "," in url else 0
                        parts.append(f"[image_url:{header},b64_len={b64_len}]")
                    else:
                        parts.append(f"[image_url:{url}]")
                elif "text" in item:
                    parts.append(str(item["text"]))
                else:
                    parts.append(json.dumps(item, ensure_ascii=False))
            else:
                parts.append(str(item))
        return "\n".join(parts)
    return str(c)


def _extract_latest_tag(messages: list[dict[str, Any]], filename: str) -> str | None:
    pat = re.compile(r"\[" + re.escape(filename) + r"#([0-9A-Fa-f]{4})\]")
    found: str | None = None
    for m in messages:
        if m.get("role") != "tool":
            continue
        txt = _msg_text(m)
        for match in pat.finditer(txt):
            found = match.group(1).upper()
    return found


def _expand_placeholders(obj: Any, messages: list[dict[str, Any]], replacements: dict[str, str]) -> Any:
    """Expand {{TAG:filename}}, {{FIXTURE_URL}}, {{WORKSPACE}}, {{HOME}} placeholders."""
    if isinstance(obj, str):
        out = obj
        for k, v in replacements.items():
            out = out.replace("{{" + k + "}}", v)

        def _repl_tag(m: re.Match[str]) -> str:
            fname = m.group(1)
            tag = _extract_latest_tag(messages, fname)
            return tag if tag else "0000"

        out = re.sub(r"\{\{TAG:([^}]+)\}\}", _repl_tag, out)
        return out
    if isinstance(obj, list):
        return [_expand_placeholders(x, messages, replacements) for x in obj]
    if isinstance(obj, dict):
        return {k: _expand_placeholders(v, messages, replacements) for k, v in obj.items()}
    return obj


def _extract_tools_ordered(body: dict[str, Any]) -> list[dict[str, Any]]:
    tools_raw = body.get("tools") or []
    parsed: list[dict[str, Any]] = []
    for t in tools_raw:
        fn = t.get("function") if isinstance(t, dict) else None
        if isinstance(fn, dict) and fn.get("name"):
            parsed.append({
                "name": fn.get("name"),
                "description": fn.get("description", ""),
                "parameters": fn.get("parameters", {}),
            })
    return parsed


def _extract_system_prompt(messages: list[dict[str, Any]]) -> str:
    sys_msgs = [m for m in messages if m.get("role") in ("system", "developer")]
    return "\n\n".join(_msg_text(m) for m in sys_msgs)


def _normalize_turn_list(case: dict[str, Any]) -> list[list[dict[str, Any]]]:
    """Normalize a case's `calls` into turns (each turn is a list of 1+ parallel tool calls).

    Supports both:
      - flat `calls: [{"tool": ..., "arguments": ...}, ...]` (one call per turn)
      - parallel turn entries `{"parallel": [{"tool": ..., "arguments": ...}, ...]}`
    """
    turns: list[list[dict[str, Any]]] = []
    for item in case.get("calls") or []:
        if isinstance(item, dict) and isinstance(item.get("parallel"), list):
            turns.append(list(item["parallel"]))
        elif isinstance(item, dict):
            turns.append([item])
    return turns


@dataclass
class OracleSessionState:
    turns: list[list[dict[str, Any]]]
    parent_marker: str
    child_reply: str
    child_delay_ms: int
    child_error: str | None
    replacements: dict[str, str]
    child_tool_calls: list[dict[str, Any]] = field(default_factory=list)
    judge_reply: str | None = None
    lock: threading.Lock = field(default_factory=threading.Lock)
    parent_turn: int = 0
    child_tool_turn: int = 0
    parent_requests: list[dict[str, Any]] = field(default_factory=list)
    child_requests: list[dict[str, Any]] = field(default_factory=list)
    dispatched_calls: list[dict[str, Any]] = field(default_factory=list)
    captured_results: dict[int, str] = field(default_factory=dict)
    # Per-request added non-assistant messages (initial user reminder, tool results,
    # follow-up user messages such as attached images or async <system-notice>).
    added_messages_per_turn: list[list[dict[str, Any]]] = field(default_factory=list)
    system_prompt: str | None = None
    advertised_tools: list[dict[str, Any]] | None = None
    child_system_prompt: str | None = None
    child_advertised_tools: list[dict[str, Any]] | None = None
    # Every distinct child subagent surface captured during the run, keyed by child agent id
    # (parsed from `Your id is \`<id>\`` in the child system prompt, falling back to `#<n>`).
    child_surfaces_by_id: dict[str, dict[str, Any]] = field(default_factory=dict)
    _flat_call_counter: int = 0
    _child_call_counter: int = 0


_RE_CHILD_SELF_ID = re.compile(r"Your id is `([^`\n]+)`")


def _extract_child_agent_id(sys_prompt: str, fallback_index: int) -> str:
    """Parse a child subagent's own agent id from its system prompt (`Your id is \\`<id>\\``)."""
    m = _RE_CHILD_SELF_ID.search(sys_prompt or "")
    if m:
        return m.group(1).strip()
    return f"child#{fallback_index}"
class OracleLLMServer:
    def __init__(self, state: OracleSessionState) -> None:
        self.state = state
        self._server: http.server.ThreadingHTTPServer | None = None
        self._thread: threading.Thread | None = None
        self.port: int = 0

    def start(self) -> int:
        state = self.state

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
                    "usage": {"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120},
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
                body = json.loads(raw.decode("utf-8"))
                model_id = str(body.get("model") or "proof-1")
                messages = body.get("messages") or []

                last_text = _msg_text(messages[-1]) if messages else ""
                if "<composer-prediction>" in last_text:
                    self._stream_sse(model_id, "aux-pred", content="")
                    return

                # Distinguish parent from subagent by checking the FIRST user message.
                first_user_text = ""
                for m in messages:
                    if m.get("role") == "user":
                        first_user_text = _msg_text(m)
                        break
                is_parent = state.parent_marker in first_user_text

                if not is_parent:
                    req_tools = _extract_tools_ordered(body)
                    req_sys = _extract_system_prompt(messages)
                    child_batch: list[tuple[str, str, str]] = []
                    is_judge_req = False
                    with state.lock:
                        cidx = len(state.child_requests)
                        state.child_requests.append(body)
                        delay_ms = state.child_delay_ms
                        child_err = state.child_error
                        child_reply = state.child_reply
                        judge_reply = state.judge_reply
                        # Detect chat TextJudge requests (no tools, or submit_judgment only).
                        tool_names = [t["name"] for t in req_tools]
                        if judge_reply is not None and (not req_tools or tool_names == ["submit_judgment"]):
                            is_judge_req = True
                        # Capture EVERY child subagent surface (skipping tool-less label requests
                        # and TextJudge requests), keyed by the child's own agent id so concurrent
                        # batch spawns pair deterministically across omp and oh-my-pig.
                        if req_tools and not is_judge_req:
                            if state.child_advertised_tools is None:
                                state.child_system_prompt = req_sys
                                state.child_advertised_tools = req_tools
                            cid = _extract_child_agent_id(req_sys, len(state.child_surfaces_by_id))
                            if cid not in state.child_surfaces_by_id:
                                state.child_surfaces_by_id[cid] = {
                                    "id": cid,
                                    "system_prompt": req_sys,
                                    "tools": req_tools,
                                }
                        if not is_judge_req and req_tools and state.child_tool_turn < len(state.child_tool_calls):
                            spec = state.child_tool_calls[state.child_tool_turn]
                            state.child_tool_turn += 1
                            ccnum = state._child_call_counter
                            state._child_call_counter += 1
                            expanded = _expand_placeholders(spec.get("arguments") or {}, messages, state.replacements)
                            child_batch.append(
                                (f"oracle_child_call_{ccnum}", str(spec["tool"]), json.dumps(expanded, ensure_ascii=False))
                            )
                    if delay_ms > 0:
                        time.sleep(delay_ms / 1000.0)
                    if child_err:
                        self._send_json(500, {"error": {"message": child_err, "type": "server_error"}})
                        return
                    if is_judge_req:
                        # Build a deterministic yes/no answer for every question ID present in the prompt.
                        q_ids = sorted(set(re.findall(r"\b(?:e\d{3}|p\d{2})\b", req_sys + "\n" + first_user_text)))
                        if len(q_ids) > 1:
                            jtext = "\n".join(f"{qid}: {judge_reply}" for qid in q_ids)
                        else:
                            jtext = judge_reply or "yes"
                        if req_tools and req_tools[0]["name"] == "submit_judgment":
                            self._stream_sse(
                                model_id,
                                f"judge-{cidx}",
                                tool_calls=[(f"judge_call_{cidx}", "submit_judgment", json.dumps({"answer": jtext}))],
                            )
                        else:
                            self._stream_sse(model_id, f"judge-{cidx}", content=jtext)
                        return
                    if child_batch:
                        self._stream_sse(model_id, f"child-{cidx}", tool_calls=child_batch)
                        return
                    self._stream_sse(model_id, f"child-{cidx}", content=child_reply)
                    return
                with state.lock:
                    idx = state.parent_turn
                    state.parent_turn += 1
                    prev_len = len(state.parent_requests[-1].get("messages") or []) if state.parent_requests else 0
                    state.parent_requests.append(body)

                    if state.system_prompt is None:
                        state.system_prompt = _extract_system_prompt(messages)
                    if state.advertised_tools is None:
                        state.advertised_tools = _extract_tools_ordered(body)

                    # Record messages newly added since the previous parent request
                    # (skip system/developer on turn 0, skip assistant echoes).
                    new_msgs: list[dict[str, Any]] = []
                    for m in messages[prev_len:]:
                        role = m.get("role")
                        if role in ("system", "developer", "assistant"):
                            continue
                        new_msgs.append({
                            "role": role,
                            "tool_call_id": m.get("tool_call_id"),
                            "text": _msg_text(m),
                        })
                    state.added_messages_per_turn.append(new_msgs)

                    # Capture tool results from any oracle_call_<N>
                    for m in messages:
                        if m.get("role") == "tool":
                            tcid = str(m.get("tool_call_id") or "")
                            if tcid.startswith("oracle_call_"):
                                try:
                                    cnum = int(tcid.split("_")[-1])
                                    state.captured_results[cnum] = _msg_text(m)
                                except ValueError:
                                    pass

                    dispatched_batch: list[tuple[str, str, str]] = []
                    delay_ms = 0
                    if idx < len(state.turns):
                        for spec in state.turns[idx]:
                            cnum = state._flat_call_counter
                            state._flat_call_counter += 1
                            tool_name = str(spec["tool"])
                            raw_args = spec.get("arguments") or {}
                            expanded_args = _expand_placeholders(raw_args, messages, state.replacements)
                            call_id = f"oracle_call_{cnum}"
                            state.dispatched_calls.append({
                                "index": cnum,
                                "turn_index": idx,
                                "tool": tool_name,
                                "arguments": expanded_args,
                                "call_id": call_id,
                            })
                            dispatched_batch.append(
                                (call_id, tool_name, json.dumps(expanded_args, ensure_ascii=False))
                            )
                            delay_ms = max(delay_ms, int(spec.get("delay_ms") or 0))

                if delay_ms > 0:
                    time.sleep(delay_ms / 1000.0)

                if dispatched_batch:
                    self._stream_sse(model_id, f"oracle-{idx}", tool_calls=dispatched_batch)
                else:
                    self._stream_sse(model_id, f"oracle-done-{idx}", content="ORACLE_DONE")

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
# Parse `--mode json` events (captures isError and Details)
# ---------------------------------------------------------------------------

def _parse_json_mode_events(stdout: str) -> dict[str, dict[str, Any]]:
    """Extract `tool_execution_start` and `tool_execution_end` events keyed by toolCallId from JSONL stdout."""
    by_call_id: dict[str, dict[str, Any]] = {}
    for line in stdout.splitlines():
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            ev = json.loads(line)
        except json.JSONDecodeError:
            continue
        if not isinstance(ev, dict):
            continue
        ev_type = ev.get("type")
        if ev_type == "tool_execution_start":
            tcid = str(ev.get("toolCallId") or ev.get("tool_call_id") or "")
            if not tcid:
                continue
            rec = by_call_id.setdefault(tcid, {})
            rec["startArgs"] = ev.get("args")
            if "intent" in ev:
                rec["startIntent"] = ev.get("intent")
        elif ev_type == "tool_execution_end":
            tcid = str(ev.get("toolCallId") or ev.get("tool_call_id") or "")
            if not tcid:
                continue
            res = ev.get("result") if isinstance(ev.get("result"), dict) else {}
            # Extract text content as fallback when context was rewound (e.g. rewind tool)
            text_parts: list[str] = []
            for item in res.get("content") or []:
                if isinstance(item, dict) and item.get("type") == "text":
                    text_parts.append(str(item.get("text", "")))
            rec = by_call_id.setdefault(tcid, {})
            rec.update({
                "toolName": ev.get("toolName") or ev.get("tool_name"),
                "isError": bool(ev.get("isError", False)),
                "details": res.get("details"),
                "text": "\n".join(text_parts) if text_parts else None,
            })
    return by_call_id

# ---------------------------------------------------------------------------
# Side runners (omp & oh-my-pig)
# ---------------------------------------------------------------------------

def run_side(
    side: str,
    binary: str,
    case: dict[str, Any],
    work_root: Path,
    timeout_s: int,
    *,
    shared_natives_dir: Path | None,
    proxy: RefusingProxyServer,
    run_tag: str,
) -> dict[str, Any]:
    """Run one case on one side (`omp` or `pig`) in a fresh realpath-resolved temp dir."""
    base = work_root / side
    shutil.rmtree(base, ignore_errors=True)
    work_dir = base / "work"
    home_dir = base / "home"
    tmp_dir = base / "tmp"
    for d in (
        work_dir,
        home_dir / ".config",
        home_dir / ".local" / "share",
        home_dir / ".local" / "state",
        home_dir / ".cache",
        tmp_dir,
    ):
        d.mkdir(parents=True, exist_ok=True)

    # Resolve realpaths once so work/home/tmp have one canonical representation.
    work_real = os.path.realpath(str(work_dir))
    home_real = os.path.realpath(str(home_dir))
    tmp_real = os.path.realpath(str(tmp_dir))

    fixture = FixtureHTTPServer(work_real)
    fixture_port = fixture.start()
    fixture_url = f"http://127.0.0.1:{fixture_port}"

    setup_cmd = case.get("setup") or ""
    setup_rc = 0
    setup_stderr = ""
    setup_timed_out = False
    # Prepare PATH with optional extra tool dirs (ORACLE_TOOL_DIRS) and precheck required external tools.
    base_env = clean_base_env()
    augment_tool_path(base_env)
    extra_tool_dirs: list[str] = []
    missing_tools: list[str] = []
    for req_tool in case.get("requires_tools") or []:
        resolved_tool = find_working_tool(str(req_tool), path_env=base_env.get("PATH"))
        if not resolved_tool:
            missing_tools.append(str(req_tool))
        else:
            # If resolved outside current PATH (e.g. broken mise shim fallback), place a symlink
            # in a case-local bin dir so both omp and oh-my-pig resolve the exact same binary
            # without altering the rest of PATH (e.g. keeping the active Go toolchain intact).
            which_cur = shutil.which(str(req_tool), path=base_env.get("PATH"))
            if which_cur != resolved_tool:
                tool_shim_dir = base / "toolbin"
                tool_shim_dir.mkdir(parents=True, exist_ok=True)
                link_path = tool_shim_dir / str(req_tool)
                if link_path.exists() or link_path.is_symlink():
                    link_path.unlink()
                link_path.symlink_to(resolved_tool)
                d_real = os.path.realpath(str(tool_shim_dir))
                if d_real not in extra_tool_dirs:
                    extra_tool_dirs.append(d_real)
    if extra_tool_dirs:
        augment_tool_path(base_env, tuple(extra_tool_dirs))

    if missing_tools:
        fixture.stop()
        return {
            "side": side,
            "binary": binary,
            "missing_tools": missing_tools,
            "setup_rc": 0,
            "setup_stderr": "",
            "setup_timed_out": False,
            "rc": -1,
            "timed_out": False,
            "elapsed_ms": 0,
            "stdout_bytes": 0,
            "stderr": f"missing tool: {', '.join(missing_tools)} not found on PATH",
            "parent_request_count": 0,
            "child_request_count": 0,
            "calls": [],
            "added_messages_normalized": [],
            "system_prompt_raw": None,
            "system_prompt_normalized": None,
            "system_prompt_normalizations": [],
            "tools": [],
            "child_system_prompt_normalized": None,
            "child_tools": [],
            "child_surfaces": {},
            "network_attempts": [],
            "leaked_pids": [],
        }

    if setup_cmd:
        setup_env = dict(base_env)
        setup_env.update({
            "HOME": home_real,
            "TMPDIR": tmp_real,
            "FIXTURE_URL": fixture_url,
            "ORACLE_SIDE": side,
            "ORACLE_RUN_TAG": run_tag,
        })
        apply_proxy_env(setup_env, proxy.url)
        try:
            sp = subprocess.run(
                ["/bin/sh", "-e", "-c", setup_cmd],
                cwd=work_real,
                env=setup_env,
                capture_output=True,
                text=True,
                timeout=30,
            )
            setup_rc = sp.returncode
            setup_stderr = sp.stderr
        except subprocess.TimeoutExpired as exc:
            setup_rc = 124
            setup_timed_out = True
            setup_stderr = (exc.stderr or "") if isinstance(exc.stderr, str) else ""
            if not setup_stderr:
                setup_stderr = "setup command timed out after 30s"

    turns = _normalize_turn_list(case)
    flat_calls = [c for batch in turns for c in batch]
    parent_marker = f"[oracle-case:{case.get('name', 'unnamed')}]"
    child_reply = str(case.get("child_reply") or "ORACLE_CHILD_OK")
    child_delay_ms = int(case.get("child_delay_ms") or 0)
    child_error = str(case["child_error"]) if case.get("child_error") else None
    child_tool_calls = list(case.get("child_tool_calls") or [])
    judge_reply = str(case["judge_reply"]) if case.get("judge_reply") is not None else None
    replacements = {
        "FIXTURE_URL": fixture_url,
        "WORKSPACE": work_real,
        "HOME": home_real,
    }
    state = OracleSessionState(
        turns=turns,
        parent_marker=parent_marker,
        child_reply=child_reply,
        child_delay_ms=child_delay_ms,
        child_error=child_error,
        replacements=replacements,
        child_tool_calls=child_tool_calls,
        judge_reply=judge_reply,
    )
    llm = OracleLLMServer(state)
    llm_port = llm.start()
    llm_url = f"http://127.0.0.1:{llm_port}/v1"

    model_inputs = ["text", "image"] if case.get("vision") else ["text"]

    env = dict(base_env)
    apply_proxy_env(env, proxy.url)
    env["ORACLE_RUN_TAG"] = run_tag
    if side == "omp":
        agent_dir = Path(home_real) / ".omp" / "agent"
        agent_dir.mkdir(parents=True, exist_ok=True)
        inputs_yaml = "[" + ", ".join(f'"{x}"' for x in model_inputs) + "]"
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
        input: {inputs_yaml}
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
        # Disable update checks, auto-updates, keyless third-party services,
        # external HTML reader installs/fallbacks (use in-process native only),
        # and composer predictions.
        extra_cfg = str(case.get("config_yml") or "")
        if case.get("omp_config_yml"):
            extra_cfg = (extra_cfg + "\n" + str(case["omp_config_yml"])).strip()
        config_yml = omp_offline_config_yml(extra_cfg)
        (agent_dir / "config.yml").write_text(config_yml, encoding="utf-8")
        env.update({
            "HOME": home_real,
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
            "PI_NATIVES_DIR": str(shared_natives_dir) if shared_natives_dir is not None else str(Path(home_real) / ".omp" / "natives"),
            "GIT_TERMINAL_PROMPT": "0",
        })
        cmd = [
            binary,
            "-p",
            "--mode", "json",
            "--model", "proof/proof-1",
            "--no-session",
            "--offline",
            "--no-extensions",
            "--no-context-files",
            "--no-title",
        ]
        if not case.get("enable_rules"):
            cmd.append("--no-rules")
        if not case.get("no_auto_approve"):
            cmd.append("--auto-approve")
        if not case.get("enable_skills"):
            cmd.append("--no-skills")
        if case.get("wait_async"):
            cmd.append("--wait-async")
        if case.get("omp_args"):
            cmd.extend(case["omp_args"])
        cmd.append(f"{parent_marker} Execute scripted calls.")
    else:
        pighome = Path(home_real) / ".pig"
        agent_dir = pighome / "agent"
        pighome.mkdir(parents=True, exist_ok=True)
        agent_dir.mkdir(parents=True, exist_ok=True)
        (agent_dir / "models.json").write_text(
            json.dumps(
                {
                    "providers": {
                        "proof": {
                            "baseUrl": llm_url,
                            "apiKey": "proof-key",
                            "api": "openai-completions",
                            "models": [
                                {
                                    "id": "proof-1",
                                    "name": "Proof 1",
                                    "reasoning": False,
                                    "input": model_inputs,
                                    "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
                                    "contextWindow": 128000,
                                    "maxTokens": 4096,
                                }
                            ],
                        }
                    }
                },
                indent=2,
            ),
            encoding="utf-8",
        )
        (agent_dir / "settings.json").write_text(
            json.dumps({"lastChangelogVersion": "1.5.2"}), encoding="utf-8"
        )
        extra_pig_cfg = str(case.get("config_yml") or "")
        if case.get("pig_config_yml"):
            extra_pig_cfg = (extra_pig_cfg + "\n" + str(case["pig_config_yml"])).strip()
        (agent_dir / "config.yml").write_text(
            pig_offline_config_yml(extra_pig_cfg), encoding="utf-8"
        )
        (agent_dir / "trust.json").write_text(
            json.dumps({work_real: True}), encoding="utf-8"
        )
        env.update({
            "HOME": home_real,
            "USERPROFILE": home_real,
            "PIG_HOME": str(pighome),
            "PI_HOME": str(pighome),
            "PIG_CODING_AGENT_DIR": str(agent_dir),
            "PI_CODING_AGENT_DIR": str(agent_dir),
            "PIG_CODING_AGENT_SESSION_DIR": str(agent_dir / "sessions"),
            "PI_CODING_AGENT_SESSION_DIR": str(agent_dir / "sessions"),
            "PIG_OFFLINE": "1",
            "PI_OFFLINE": "1",
            "XDG_CONFIG_HOME": str(Path(home_real) / ".config"),
            "XDG_DATA_HOME": str(Path(home_real) / ".local" / "share"),
            "XDG_STATE_HOME": str(Path(home_real) / ".local" / "state"),
            "XDG_CACHE_HOME": str(Path(home_real) / ".cache"),
            "TMPDIR": tmp_real,
            "GIT_TERMINAL_PROMPT": "0",
        })
        cmd = [
            binary,
            "-p",
            "--mode", "json",
            "--model", "proof/proof-1",
            "--no-session",
            "--approve",
            "--no-extensions",
            "--no-context-files",
        ]
        if not case.get("enable_skills"):
            cmd.append("--no-skills")
        if case.get("pig_args"):
            cmd.extend(case["pig_args"])
        cmd.append(f"{parent_marker} Execute scripted calls.")
        if case.get("pig_env"):
            env.update({str(k): str(v) for k, v in case["pig_env"].items()})
    if case.get("env"):
        env.update({str(k): str(v) for k, v in case["env"].items()})
    if side == "omp" and case.get("omp_env"):
        env.update({str(k): str(v) for k, v in case["omp_env"].items()})
    wrapped_cmd = sandbox_cmd(cmd)
    proxy_start_idx = len(proxy.snapshot())

    rc = -1
    stdout = ""
    stderr = ""
    timed_out = False
    leaked_pids: list[int] = []
    t0 = time.monotonic()
    proc: subprocess.Popen[str] | None = None

    if setup_rc != 0:
        llm.stop()
        fixture.stop()
        return {
            "side": side,
            "binary": binary,
            "setup_rc": setup_rc,
            "setup_stderr": setup_stderr,
            "setup_timed_out": setup_timed_out,
            "rc": -1,
            "timed_out": False,
            "elapsed_ms": 0,
            "stdout": "",
            "stderr": f"setup failed (rc={setup_rc}): {setup_stderr}",
            "parent_request_count": 0,
            "child_request_count": 0,
            "calls": [],
            "added_messages_normalized": [],
            "system_prompt_raw": None,
            "system_prompt_normalized": None,
            "system_prompt_normalizations": [],
            "tools": [],
            "child_system_prompt_normalized": None,
            "child_tools": [],
            "child_surfaces": {},
            "network_attempts": [],
            "leaked_pids": [],
        }

    try:
        proc = subprocess.Popen(
            wrapped_cmd,
            cwd=work_real,
            env=env,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            start_new_session=True,
        )
        try:
            stdout, stderr = proc.communicate(timeout=timeout_s)
            rc = proc.returncode
        except subprocess.TimeoutExpired:
            timed_out = True
            try:
                os.killpg(proc.pid, signal.SIGTERM)
            except OSError:
                try:
                    proc.terminate()
                except OSError:
                    pass
            try:
                stdout, stderr = proc.communicate(timeout=3)
            except subprocess.TimeoutExpired:
                try:
                    os.killpg(proc.pid, signal.SIGKILL)
                except OSError:
                    try:
                        proc.kill()
                    except OSError:
                        pass
                stdout, stderr = proc.communicate()
            rc = proc.returncode
    finally:
        if proc is not None and proc.poll() is None:
            try:
                os.killpg(proc.pid, signal.SIGKILL)
            except OSError:
                try:
                    proc.kill()
                except OSError:
                    pass
            try:
                proc.wait(timeout=2)
            except subprocess.TimeoutExpired:
                pass
        # Always sweep by ORACLE_RUN_TAG even when the leader exited 0
        # (catches __omp_worker_daemon, background bash services, kernels).
        leaked_pids = reap_tagged_processes(run_tag)
        llm.stop()
        fixture.stop()

    elapsed_ms = int((time.monotonic() - t0) * 1000)
    net_attempts = proxy.snapshot()[proxy_start_idx:]
    json_events = _parse_json_mode_events(stdout)

    call_records: list[dict[str, Any]] = []
    for idx, spec in enumerate(flat_calls):
        dispatched = next((d for d in state.dispatched_calls if d["index"] == idx), None)
        call_id = dispatched["call_id"] if dispatched else f"oracle_call_{idx}"
        ev = json_events.get(call_id)
        raw_res = state.captured_results.get(idx)
        if raw_res is None and ev is not None and ev.get("text") is not None:
            # Fallback for tools that replace/rewind the transcript before the next LLM call (e.g. rewind).
            raw_res = ev["text"]
        if raw_res is not None:
            norm_res, applied = normalize_text(
                raw_res,
                work_dir=work_real,
                home_dir=home_real,
                tmp_dir=tmp_real,
                fixture_port=fixture_port,
            )
        else:
            norm_res, applied = None, []

        raw_details = ev.get("details") if ev is not None else None
        raw_wall_ms = (
            float(raw_details["wallTimeMs"])
            if isinstance(raw_details, dict)
            and isinstance(raw_details.get("wallTimeMs"), (int, float))
            and not isinstance(raw_details.get("wallTimeMs"), bool)
            else None
        )
        norm_details = (
            normalize_obj(
                raw_details,
                work_dir=work_real,
                home_dir=home_real,
                tmp_dir=tmp_real,
                fixture_port=fixture_port,
            )
            if raw_details is not None
            else None
        )
        norm_start_args = (
            normalize_obj(
                ev["startArgs"],
                work_dir=work_real,
                home_dir=home_real,
                tmp_dir=tmp_real,
                fixture_port=fixture_port,
            )
            if ev is not None and "startArgs" in ev
            else None
        )
        call_records.append({
            "index": idx,
            "turn_index": dispatched["turn_index"] if dispatched else idx,
            "tool": spec["tool"],
            "arguments": dispatched["arguments"] if dispatched else spec.get("arguments", {}),
            "captured": raw_res is not None,
            "raw_result": raw_res,
            "normalized_result": norm_res,
            "normalizations_applied": applied,
            "is_error": ev["isError"] if ev is not None and "isError" in ev else None,
            "details_normalized": norm_details,
            "raw_wall_time_ms": raw_wall_ms,
            "recorded_args": norm_start_args,
            "recorded_intent": ev.get("startIntent") if ev is not None else None,
        })
    # Normalize per-turn added messages (user reminders, async notices, vision images)
    added_norm: list[list[dict[str, Any]]] = []
    for batch in state.added_messages_per_turn:
        norm_batch: list[dict[str, Any]] = []
        for m in batch:
            nt, _ = normalize_text(
                m["text"],
                work_dir=work_real,
                home_dir=home_real,
                tmp_dir=tmp_real,
                fixture_port=fixture_port,
            )
            norm_batch.append({"role": m["role"], "tool_call_id": m.get("tool_call_id"), "text": nt})
        added_norm.append(norm_batch)

    norm_sys = None
    sys_norms: list[str] = []
    if state.system_prompt is not None:
        norm_sys, sys_norms = normalize_text(
            state.system_prompt,
            work_dir=work_real,
            home_dir=home_real,
            tmp_dir=tmp_real,
            fixture_port=fixture_port,
        )

    norm_child_sys = None
    if state.child_system_prompt is not None:
        norm_child_sys, _ = normalize_text(
            state.child_system_prompt,
            work_dir=work_real,
            home_dir=home_real,
            tmp_dir=tmp_real,
            fixture_port=fixture_port,
        )

    norm_child_surfaces: dict[str, dict[str, Any]] = {}
    for cid, csurf in state.child_surfaces_by_id.items():
        csys_raw = csurf.get("system_prompt")
        csys_norm = None
        if isinstance(csys_raw, str):
            csys_norm, _ = normalize_text(
                csys_raw,
                work_dir=work_real,
                home_dir=home_real,
                tmp_dir=tmp_real,
                fixture_port=fixture_port,
            )
        norm_child_surfaces[cid] = {
            "id": cid,
            "system_prompt_normalized": csys_norm,
            "tools": csurf.get("tools") or [],
        }

    return {
        "side": side,
        "binary": binary,
        "setup_rc": setup_rc,
        "setup_stderr": setup_stderr,
        "setup_timed_out": setup_timed_out,
        "rc": rc,
        "timed_out": timed_out,
        "elapsed_ms": elapsed_ms,
        "stdout_bytes": len(stdout),
        "stderr": stderr,
        "parent_request_count": len(state.parent_requests),
        "child_request_count": len(state.child_requests),
        "calls": call_records,
        "added_messages_normalized": added_norm,
        "system_prompt_raw": state.system_prompt,
        "system_prompt_normalized": norm_sys,
        "system_prompt_normalizations": sys_norms,
        "tools": state.advertised_tools or [],
        "child_system_prompt_normalized": norm_child_sys,
        "child_tools": state.child_advertised_tools or [],
        "child_surfaces": norm_child_surfaces,
        "network_attempts": net_attempts,
        "leaked_pids": leaked_pids,
    }


# ---------------------------------------------------------------------------
# Comparison & report generation
# ---------------------------------------------------------------------------

def unified_diff(a: str, b: str, label_a: str = "omp", label_b: str = "oh-my-pig") -> str:
    """Unified diff that explicitly surfaces trailing-newline differences."""
    a_lines = a.splitlines(keepends=True)
    b_lines = b.splitlines(keepends=True)
    raw_lines = list(difflib.unified_diff(a_lines, b_lines, fromfile=label_a, tofile=label_b))
    out: list[str] = []
    for line in raw_lines:
        if line.endswith("\n"):
            out.append(line)
        else:
            out.append(line + "\n\\ No newline at end of file\n")
    if not out and a != b:
        out.append(f"--- {label_a}\n+++ {label_b}\n@@ trailing newline differs @@\n")
    return "".join(out)


def compare_tools(omp_tools: list[dict[str, Any]], pig_tools: list[dict[str, Any]]) -> dict[str, Any]:
    """Compare tool lists preserving advertised tool order AND JSON schema property order."""
    omp_names = [t["name"] for t in omp_tools]
    pig_names = [t["name"] for t in pig_tools]
    omp_map = {t["name"]: t for t in omp_tools}
    pig_map = {t["name"]: t for t in pig_tools}

    # Preserve omp order first, then any pig-only names in pig order.
    ordered_names = list(omp_names) + [n for n in pig_names if n not in omp_map]
    only_omp = [n for n in omp_names if n not in pig_map]
    only_pig = [n for n in pig_names if n not in omp_map]
    order_identical = omp_names == pig_names

    per_tool: list[dict[str, Any]] = []
    for name in ordered_names:
        ot = omp_map.get(name)
        pt = pig_map.get(name)
        if ot and pt:
            # Do NOT sort_keys: preserve property order as advertised on the wire.
            o_schema = json.dumps(ot.get("parameters", {}), sort_keys=False, indent=2)
            p_schema = json.dumps(pt.get("parameters", {}), sort_keys=False, indent=2)
            schema_same = o_schema == p_schema
            desc_same = (ot.get("description") or "") == (pt.get("description") or "")
            per_tool.append({
                "name": name,
                "status": "identical" if (schema_same and desc_same) else "differs",
                "schema_identical": schema_same,
                "description_identical": desc_same,
                "schema_diff": "" if schema_same else unified_diff(o_schema, p_schema, f"omp:{name}.parameters", f"oh-my-pig:{name}.parameters"),
                "description_diff": "" if desc_same else unified_diff(ot.get("description") or "", pt.get("description") or "", f"omp:{name}.description", f"oh-my-pig:{name}.description"),
            })
        elif ot:
            per_tool.append({"name": name, "status": "only_omp"})
        else:
            per_tool.append({"name": name, "status": "only_pig"})
    return {
        "omp_names": omp_names,
        "pig_names": pig_names,
        "order_identical": order_identical,
        "only_omp": only_omp,
        "only_pig": only_pig,
        "per_tool": per_tool,
    }


def _strip_heavy(res: dict[str, Any] | None) -> dict[str, Any] | None:
    if res is None:
        return None
    drop = {
        "system_prompt_raw",
        "system_prompt_normalized",
        "tools",
        "child_system_prompt_normalized",
        "child_tools",
        "child_surfaces",
    }
    return {k: v for k, v in res.items() if k not in drop}

# ---------------------------------------------------------------------------
# Reviewable allowlists for documented details & text differences
# ---------------------------------------------------------------------------

DEFAULT_DETAILS_ALLOWLIST_PATH = SCRIPT_DIR / "details_allowlist.json"
DEFAULT_TEXT_ALLOWLIST_PATH = SCRIPT_DIR / "text_allowlist.json"
DEFAULT_RECORDED_ALLOWLIST_PATH = SCRIPT_DIR / "recorded_allowlist.json"
DEFAULT_SURFACE_ALLOWLIST_PATH = SCRIPT_DIR / "surface_allowlist.json"

VALID_DETAILS_CLASSES = frozenset({"LISTED", "HOST", "PORT_EXTRA", "NO_CONSUMER_AT_TAG", "ON_PURPOSE"})
VALID_TEXT_CLASSES = frozenset({"LISTED", "HOST", "ON_PURPOSE"})
VALID_RECORDED_CLASSES = frozenset({"LISTED", "HOST", "ON_PURPOSE"})
VALID_SURFACE_CLASSES = frozenset({"LISTED", "HOST", "ON_PURPOSE"})


def _details_entry_subject(raw: dict[str, Any]) -> list[str] | None:
    """Extract the differing field name(s) that the cited row must mention."""
    if raw.get("match_kind") == "agent_loop_error":
        return ["errorToolResult", "isError", "error"]
    path = str(raw.get("path") or "").strip()
    if not path:
        return None
    leaf = path.split(".")[-1].rstrip("[]")
    candidates = [path, leaf]
    if path.startswith("proc.daemons[]."):
        candidates.append(path[len("proc.daemons[]."):])
    elif path.startswith("proc.daemon."):
        candidates.append(path[len("proc.daemon."):])
    return [c for c in candidates if c]


def _validate_anchored_regex(pattern: str, ctx: str) -> None:
    """Require text/surface allowlist regexes to be anchored at both ends and reject open `.*$` tails."""
    s = pattern.strip()
    if not s:
        raise ValueError(f"{ctx}: empty regex")
    # Accept either `^...$` or `(?:^...$|^...$)` where every branch is anchored at both ends.
    if not (s.startswith("^") or s.startswith("(?:^")):
        raise ValueError(f"{ctx}: regex must be anchored at start (^): {pattern!r}")
    if not (s.endswith("$") or s.endswith("$)")):
        raise ValueError(f"{ctx}: regex must be anchored at end ($): {pattern!r}")
    if re.search(r"\.\*\$(?:\)|$)", s):
        raise ValueError(f"{ctx}: open-tailed '.*$' regex is not allowed; anchor exact text: {pattern!r}")
    re.compile(pattern)


def load_details_allowlist(path: Path | None = None) -> list[dict[str, Any]]:
    p = path or DEFAULT_DETAILS_ALLOWLIST_PATH
    if not p.is_file():
        return []
    data = json.loads(p.read_text(encoding="utf-8"))
    if not isinstance(data, list):
        raise ValueError(f"details_allowlist must be a JSON array: {p}")
    resolved = resolve_allowlist_entries(
        data, source_name=p.name, subject_extractor=_details_entry_subject
    )
    for i, e in enumerate(resolved):
        cls = e.get("class")
        if cls not in VALID_DETAILS_CLASSES:
            raise ValueError(
                f"{p.name}[{i}]: invalid class {cls!r}; expected one of {sorted(VALID_DETAILS_CLASSES)}"
            )
    return resolved


def load_text_allowlist(path: Path | None = None) -> list[dict[str, Any]]:
    p = path or DEFAULT_TEXT_ALLOWLIST_PATH
    if not p.is_file():
        return []
    data = json.loads(p.read_text(encoding="utf-8"))
    if not isinstance(data, list):
        raise ValueError(f"text_allowlist must be a JSON array: {p}")
    resolved = resolve_allowlist_entries(data, source_name=p.name)
    for i, e in enumerate(resolved):
        cls = e.get("class")
        if cls not in VALID_TEXT_CLASSES:
            raise ValueError(
                f"{p.name}[{i}]: invalid class {cls!r}; expected one of {sorted(VALID_TEXT_CLASSES)}"
            )
        _validate_anchored_regex(str(e.get("regex", "")), f"{p.name}[{i}]")
    return resolved


def load_recorded_allowlist(path: Path | None = None) -> list[dict[str, Any]]:
    p = path or DEFAULT_RECORDED_ALLOWLIST_PATH
    if not p.is_file():
        return []
    data = json.loads(p.read_text(encoding="utf-8"))
    if not isinstance(data, list):
        raise ValueError(f"recorded_allowlist must be a JSON array: {p}")
    resolved = resolve_allowlist_entries(data, source_name=p.name)
    for i, e in enumerate(resolved):
        cls = e.get("class")
        if cls not in VALID_RECORDED_CLASSES:
            raise ValueError(
                f"{p.name}[{i}]: invalid class {cls!r}; expected one of {sorted(VALID_RECORDED_CLASSES)}"
            )
    return resolved


def load_surface_allowlist(path: Path | None = None) -> list[dict[str, Any]]:
    p = path or DEFAULT_SURFACE_ALLOWLIST_PATH
    if not p.is_file():
        return []
    data = json.loads(p.read_text(encoding="utf-8"))
    if not isinstance(data, list):
        raise ValueError(f"surface_allowlist must be a JSON array: {p}")
    resolved = resolve_allowlist_entries(data, source_name=p.name)
    for i, e in enumerate(resolved):
        cls = e.get("class")
        if cls not in VALID_SURFACE_CLASSES:
            raise ValueError(
                f"{p.name}[{i}]: invalid class {cls!r}; expected one of {sorted(VALID_SURFACE_CLASSES)}"
            )
        if e.get("regex") is not None:
            _validate_anchored_regex(str(e["regex"]), f"{p.name}[{i}]")
    return resolved


def _match_details_path(pattern: str, concrete_path: str) -> bool:
    """Match a dot-separated JSON path pattern against a concrete path without swallowing siblings.

    Each `.`-separated segment is matched independently with fnmatchcase, so
    `proc.daemon.*` matches `proc.daemon.id` but NOT `proc.daemon.policy.hold`
    or `proc.daemon_other.id`, and `meta.pagedSource` matches only `meta.pagedSource`
    (never sibling `meta.source` or child `meta.pagedSource.x`).
    """
    pat_segs = pattern.split(".")
    val_segs = concrete_path.split(".")
    if len(pat_segs) != len(val_segs):
        return False
    return all(fnmatch.fnmatchcase(v, p) for p, v in zip(pat_segs, val_segs))


def _walk_details_diffs(
    o_val: Any,
    p_val: Any,
    path: str = "",
    *,
    _ctx_stack: tuple[Any, ...] = (),
) -> list[dict[str, Any]]:
    """Collect all leaf/structural differences between normalized omp and port Details objects.

    Treats `None` vs `{}` as a genuine difference (never silently equal):
    - `{}` vs `None` reports `<root>` (or the field path) as `side="value"`.
    - Non-empty dict vs `None` emits a `<root>` / field `None`-vs-dict difference unless
      both are dicts (e.g. `{}` vs `{"a": 1}` emits `a` as `port-only`).

    Attaches `_ancestors` (enclosing omp dict objects from root down to parent) so
    narrow `when` predicates can inspect enclosing items (e.g. a piped bash job's
    `label` and stat item's `label: "lines"`) without matching unrelated list elements.
    """
    diffs: list[dict[str, Any]] = []
    if type(o_val) is not type(p_val):
        diffs.append({"path": path or "<root>", "side": "value", "omp": o_val, "pig": p_val, "_ancestors": _ctx_stack})
        return diffs
    if isinstance(o_val, dict):
        next_stack = _ctx_stack + (o_val,)
        for k in sorted(set(o_val) | set(p_val)):
            child_path = f"{path}.{k}" if path else k
            if k not in p_val:
                diffs.append({"path": child_path, "side": "omp-only", "omp": o_val[k], "pig": None, "_ancestors": next_stack})
            elif k not in o_val:
                diffs.append({"path": child_path, "side": "port-only", "omp": None, "pig": p_val[k], "_ancestors": next_stack})
            else:
                diffs.extend(_walk_details_diffs(o_val[k], p_val[k], child_path, _ctx_stack=next_stack))
        return diffs
    if isinstance(o_val, list):
        if len(o_val) != len(p_val):
            diffs.append({"path": path or "<root>", "side": "value", "omp": o_val, "pig": p_val, "_ancestors": _ctx_stack})
            return diffs
        elem_path = f"{path}[]" if path else "[]"
        for ox, px in zip(o_val, p_val):
            diffs.extend(_walk_details_diffs(ox, px, elem_path, _ctx_stack=_ctx_stack))
        return diffs
    if o_val != p_val:
        diffs.append({"path": path or "<root>", "side": "value", "omp": o_val, "pig": p_val, "_ancestors": _ctx_stack})
    return diffs

def _get_nested_value(obj: Any, dotted_path: str) -> tuple[bool, Any]:
    cur = obj
    for seg in dotted_path.split("."):
        if not isinstance(cur, dict) or seg not in cur:
            return False, None
        cur = cur[seg]
    return True, cur


def _matches_when_condition(
    when: dict[str, Any],
    *,
    diff: dict[str, Any],
    case_name: str,
    call_index: int | None,
    omp_details: Any,
    pig_details: Any,
    omp_is_error: bool | None,
    pig_is_error: bool | None,
    omp_text: str | None,
    arguments: dict[str, Any] | None,
) -> bool:
    """Evaluate optional narrowing conditions on a details allowlist entry."""
    if "case" in when and when["case"] != case_name:
        return False
    if "call_index" in when and (call_index is None or int(when["call_index"]) != call_index):
        return False
    if "omp_is_error" in when and (omp_is_error is None or bool(omp_is_error) != bool(when["omp_is_error"])):
        return False
    if "pig_is_error" in when and (pig_is_error is None or bool(pig_is_error) != bool(when["pig_is_error"])):
        return False
    if "omp_value" in when and diff.get("omp") != when["omp_value"]:
        return False
    if "pig_value" in when and diff.get("pig") != when["pig_value"]:
        return False
    if "omp_value_regex" in when:
        if not isinstance(diff.get("omp"), str) or not re.search(str(when["omp_value_regex"]), diff["omp"]):
            return False
    if "pig_value_regex" in when:
        if not isinstance(diff.get("pig"), str) or not re.search(str(when["pig_value_regex"]), diff["pig"]):
            return False
    if "omp_details_keys_exact" in when:
        if not isinstance(omp_details, dict) or sorted(omp_details.keys()) != sorted(when["omp_details_keys_exact"]):
            return False
    if "pig_details_exact" in when:
        if pig_details != when["pig_details_exact"]:
            return False
    if "omp_details_has" in when:
        for k, v in when["omp_details_has"].items():
            ok, got = _get_nested_value(omp_details, k)
            if not ok or got != v:
                return False
    if "pig_details_has" in when:
        for k, v in when["pig_details_has"].items():
            ok, got = _get_nested_value(pig_details, k)
            if not ok or got != v:
                return False
    if "omp_details_missing" in when:
        for k in when["omp_details_missing"]:
            ok, _ = _get_nested_value(omp_details, k)
            if ok:
                return False
    if "pig_details_missing" in when:
        for k in when["pig_details_missing"]:
            ok, _ = _get_nested_value(pig_details, k)
            if ok:
                return False
    if "pig_source_prefix" in when:
        ok, val = _get_nested_value(pig_details, "meta.source.value")
        if not ok or not isinstance(val, str) or not val.startswith(str(when["pig_source_prefix"])):
            return False
    if "omp_source_prefix" in when:
        ok, val = _get_nested_value(omp_details, "meta.source.value")
        if not ok or not isinstance(val, str) or not val.startswith(str(when["omp_source_prefix"])):
            return False
    if "arg_path_regex" in when:
        p = (arguments or {}).get("path")
        if not isinstance(p, str) or not re.search(str(when["arg_path_regex"]), p):
            return False
    if "omp_only_keys_subset" in when:
        if not isinstance(diff.get("omp"), dict) or not set(diff["omp"].keys()).issubset(set(when["omp_only_keys_subset"])):
            return False
    if "pig_only_keys_subset" in when:
        if not isinstance(diff.get("pig"), dict) or not set(diff["pig"].keys()).issubset(set(when["pig_only_keys_subset"])):
            return False
    if "omp_widget_states" in when:
        val = diff.get("omp")
        if not isinstance(val, dict) or val.get("state") not in when["omp_widget_states"]:
            return False
    if "omp_text_prefix" in when:
        if not isinstance(omp_text, str) or not omp_text.startswith(str(when["omp_text_prefix"])):
            return False
    if "enclosing_item_label" in when:
        anc = diff.get("_ancestors") or ()
        parent = anc[-1] if anc else None
        if not isinstance(parent, dict) or parent.get("label") != when["enclosing_item_label"]:
            return False
    if "enclosing_job_label_contains" in when:
        anc = diff.get("_ancestors") or ()
        needle = str(when["enclosing_job_label_contains"])
        job_dict = next(
            (a for a in reversed(anc) if isinstance(a, dict) and a.get("type") == "bash" and isinstance(a.get("label"), str)),
            None,
        )
        if job_dict is None or needle not in job_dict["label"]:
            return False
    if "enclosing_job_id" in when:
        anc = diff.get("_ancestors") or ()
        job_dict = next(
            (a for a in reversed(anc) if isinstance(a, dict) and a.get("type") == "bash" and "id" in a),
            None,
        )
        if job_dict is None or job_dict.get("id") != when["enclosing_job_id"]:
            return False
    return True

def _is_agent_loop_error_shape(
    omp_details: Any,
    pig_details: Any,
    omp_is_error: bool | None,
    pig_is_error: bool | None,
    omp_text: str | None,
) -> bool:
    """Match ONLY omp's agent-loop prepare-phase error shape vs PiG's errorToolResult({}).

    omp sets `details: {isError: true, error: <result text>}` (agent-loop.ts:3643-3650)
    and PiG's `errorToolResult` returns `{}` (agent/tool_execution.go:325-327). Both
    sides must have `isError == True`.
    """
    if omp_is_error is not True or pig_is_error is not True:
        return False
    if pig_details != {}:
        return False
    if not isinstance(omp_details, dict) or set(omp_details.keys()) != {"isError", "error"}:
        return False
    if omp_details.get("isError") is not True or not isinstance(omp_details.get("error"), str):
        return False
    if omp_text is not None and omp_details.get("error") != omp_text:
        return False
    return True


def classify_details_diffs(
    tool: str,
    omp_details: Any,
    pig_details: Any,
    allowlist: list[dict[str, Any]],
    *,
    case_name: str = "",
    call_index: int | None = None,
    omp_is_error: bool | None = None,
    pig_is_error: bool | None = None,
    omp_text: str | None = None,
    arguments: dict[str, Any] | None = None,
    implied_paths: list[str] | None = None,
) -> tuple[list[dict[str, Any]], list[dict[str, Any]], list[int]]:
    """Split details differences into (matched_allowlisted, unexplained, matched_entry_indices).

    Supports:
    - structural `match_kind: "agent_loop_error"` (exact `{isError: true, error: <omp_text>}` vs `{}` with both isError=True),
    - per-entry `when` conditions (case, call_index, isError, omp/pig values, source prefix, details keys/values, widget state),
    - `implied_paths`: details paths explicitly declared by a matched `text_allowlist.json` entry as consequences of the listed text diff.
    """
    raw_diffs = _walk_details_diffs(omp_details, pig_details)
    if not raw_diffs:
        return [], [], []

    # Check structural whole-call matchers first (e.g., agent_loop_error)
    for idx, rule in enumerate(allowlist):
        if rule.get("match_kind") == "agent_loop_error":
            r_tool = rule.get("tool", "*")
            if r_tool != "*" and r_tool != tool:
                continue
            if _is_agent_loop_error_shape(omp_details, pig_details, omp_is_error, pig_is_error, omp_text):
                matched = [
                    {
                        **{k: v for k, v in d.items() if k != "_ancestors"},
                        "allowlist_index": idx,
                        "class": rule.get("class"),
                        "ref": rule.get("ref"),
                    }
                    for d in raw_diffs
                ]
                return matched, [], [idx]

    implied = list(implied_paths or [])
    matched: list[dict[str, Any]] = []
    unexplained: list[dict[str, Any]] = []
    matched_indices: set[int] = set()
    for d in raw_diffs:
        hit_idx: int | None = None
        for idx, rule in enumerate(allowlist):
            if rule.get("match_kind"):
                continue
            r_tool = rule.get("tool", "")
            if r_tool != "*" and r_tool != tool:
                continue
            if rule.get("side") != d["side"]:
                continue
            if not _match_details_path(str(rule.get("path", "")), d["path"]):
                continue
            when = rule.get("when")
            if isinstance(when, dict) and not _matches_when_condition(
                when,
                diff=d,
                case_name=case_name,
                call_index=call_index,
                omp_details=omp_details,
                pig_details=pig_details,
                omp_is_error=omp_is_error,
                pig_is_error=pig_is_error,
                omp_text=omp_text,
                arguments=arguments,
            ):
                continue
            hit_idx = idx
            break
        clean_d = {k: v for k, v in d.items() if k != "_ancestors"}
        if hit_idx is not None:
            matched_indices.add(hit_idx)
            rule = allowlist[hit_idx]
            matched.append({
                **clean_d,
                "allowlist_index": hit_idx,
                "class": rule.get("class"),
                "ref": rule.get("ref"),
            })
        elif any(_match_details_path(pat, d["path"]) for pat in implied):
            matched.append({
                **clean_d,
                "allowlist_index": None,
                "class": "IMPLIED_BY_TEXT",
                "ref": "text_allowlist",
            })
        else:
            unexplained.append(clean_d)
    return matched, unexplained, sorted(matched_indices)

def _strip_wall_time_notice_for_timing_check(text: str) -> str:
    """Remove a single omp-formatted `Wall time: <DURATION> seconds` notice line (with its join separator)
    ONLY to test whether two texts differ solely due to a one-sided wall-time threshold crossing.

    Matches all notice shapes emitted by bash.ts:1306-1320 (`outputLines.push("", ...notices)` joined by `\\n`):
    - Followed by another notice on the next line (e.g. pty fallback): `\\nWall time: <DURATION> seconds\\n<next>` -> `\\n<next>`
    - Preceded by blank separator and followed by blank+footer/end/fence (e.g. `[Some lines truncated...]`, EOF, ```` ``` ````):
      `\\n\\nWall time: <DURATION> seconds` -> ``
    """
    # Case A: another notice line follows immediately (`notty\n\n\nWall time: <DURATION> seconds\npty ...`)
    out, n = re.subn(r"\nWall time: <DURATION> seconds(?=\n(?!```)(?!\n)(?!$))", "", text, count=1)
    if n > 0:
        return out
    # Case B: sole notice preceded by blank separator line (at end, before closing fence ```, or before `\n\n[...]` footer)
    out, n = re.subn(r"\n\nWall time: <DURATION> seconds(?=$|\n```|\n\n)", "", text, count=1)
    if n > 0:
        return out
    # Case C: bare wall-time notice at start of text
    out, n = re.subn(r"^Wall time: <DURATION> seconds(?:\n|$)", "", text, count=1)
    return out


def _extract_raw_wall_time_seconds(raw_text: str | None) -> float | None:
    if not isinstance(raw_text, str):
        return None
    m = _RE_WALL_TIME.search(raw_text)
    if not m:
        return None
    try:
        return float(m.group(1))
    except ValueError:
        return None


def _is_one_sided_wall_time_diff(
    omp_norm: str,
    pig_norm: str,
    *,
    omp_raw: str | None = None,
    pig_raw: str | None = None,
    omp_wall_ms: float | None = None,
    pig_wall_ms: float | None = None,
    unexplained_details: list[dict[str, Any]] | None = None,
) -> bool:
    """Return True iff exactly ONE side emitted `Wall time: <DURATION> seconds` within a narrow
    threshold band (`[1.0s, 1.3s]`), the other side's `wallTimeMs` (when known) is below 1000ms,
    removing the notice equalizes the texts, and no details differ aside from `wallTimeNoticeEmitted`.
    """
    o_has = bool(_RE_WALL_TIME_NORM.search(omp_norm))
    p_has = bool(_RE_WALL_TIME_NORM.search(pig_norm))
    if o_has == p_has:
        return False
    # Any unexplained details difference other than wallTimeNoticeEmitted disqualifies `timing`.
    for d in unexplained_details or []:
        if d.get("path") != "wallTimeNoticeEmitted":
            return False
    if o_has:
        if _strip_wall_time_notice_for_timing_check(omp_norm) != pig_norm:
            return False
        printing_s = _extract_raw_wall_time_seconds(omp_raw)
        if printing_s is None and omp_wall_ms is not None:
            printing_s = omp_wall_ms / 1000.0
        other_ms = pig_wall_ms
    else:
        if omp_norm != _strip_wall_time_notice_for_timing_check(pig_norm):
            return False
        printing_s = _extract_raw_wall_time_seconds(pig_raw)
        if printing_s is None and pig_wall_ms is not None:
            printing_s = pig_wall_ms / 1000.0
        other_ms = omp_wall_ms
    # Require a known raw duration in the narrow band above omp's 1000ms threshold.
    if printing_s is None or printing_s < (WALL_TIME_NOTICE_MIN_MS / 1000.0) or printing_s > WALL_TIME_TIMING_MAX_S:
        return False
    if other_ms is not None and other_ms >= WALL_TIME_NOTICE_MIN_MS:
        return False
    return True


def _parse_unified_diff_changed_lines(diff_text: str) -> tuple[list[tuple[str, str]], bool]:
    """Parse a unified diff by header position so content lines starting with `--` or `++` are never skipped.

    Returns `(changed_lines, saw_is_error_line)` where each item in `changed_lines` is
    `(sign, content)` with `sign` in `("-", "+")`.
    """
    changed: list[tuple[str, str]] = []
    saw_is_error_line = False
    raw_lines = diff_text.splitlines()
    i = 0
    in_hunk = False
    # Skip the two file-header lines (`--- <from>`, `+++ <to>`) by position only.
    if len(raw_lines) >= 2 and raw_lines[0].startswith("--- ") and raw_lines[1].startswith("+++ "):
        i = 2
    while i < len(raw_lines):
        raw_line = raw_lines[i]
        i += 1
        if raw_line.startswith("@@ ") and raw_line.endswith(" @@"):
            in_hunk = True
            continue
        if raw_line.startswith("[isError differs:"):
            saw_is_error_line = True
            continue
        if raw_line.startswith("\\ "):
            continue
        if not in_hunk and not raw_line:
            continue
        if raw_line.startswith("-"):
            changed.append(("-", raw_line[1:]))
        elif raw_line.startswith("+"):
            changed.append(("+", raw_line[1:]))
    return changed, saw_is_error_line


def classify_text_diff(
    case_name: str,
    call_index: int,
    tool: str,
    diff_text: str,
    allowlist: list[dict[str, Any]],
    *,
    is_error_differs: bool = False,
    omp_is_error: bool | None = None,
    pig_is_error: bool | None = None,
) -> tuple[bool, list[int]]:
    """Check whether every differing +/- line in diff_text AND any isError mismatch is explicitly allowlisted.

    An isError mismatch is never ignored and never matched implicitly: the matching allowlist
    entry must explicitly declare `"is_error_mismatch": [omp_is_error, pig_is_error]` (or
    `"allow_is_error_mismatch": true`). Returns (all_listed, matched_entry_indices).
    """
    candidates: list[tuple[int, dict[str, Any], re.Pattern[str]]] = []
    for idx, rule in enumerate(allowlist):
        r_case = rule.get("case", "*")
        if r_case != "*" and r_case != case_name:
            continue
        r_idx = rule.get("call_index")
        if r_idx is not None and r_idx != "*" and int(r_idx) != call_index:
            continue
        r_tool = rule.get("tool", "*")
        if r_tool != "*" and r_tool != tool:
            continue
        candidates.append((idx, rule, re.compile(str(rule.get("regex", "")))))

    changed_lines, saw_is_error_line = _parse_unified_diff_changed_lines(diff_text)
    err_mismatch = is_error_differs or saw_is_error_line
    if not changed_lines and not err_mismatch:
        return False, []

    matched_indices: set[int] = set()
    for sign, line in changed_lines:
        hit: int | None = None
        for idx, rule, pat in candidates:
            r_side = rule.get("side")
            if r_side == "omp" and sign != "-":
                continue
            if r_side == "pig" and sign != "+":
                continue
            if pat.search(line):
                hit = idx
                break
        if hit is None:
            return False, []
        matched_indices.add(hit)

    if err_mismatch:
        err_hit: int | None = None
        for idx, rule, _pat in candidates:
            spec = rule.get("is_error_mismatch")
            if isinstance(spec, list) and len(spec) == 2:
                if omp_is_error is None or pig_is_error is None or (bool(spec[0]) == bool(omp_is_error) and bool(spec[1]) == bool(pig_is_error)):
                    err_hit = idx
                    break
            elif rule.get("allow_is_error_mismatch") is True:
                err_hit = idx
                break
        if err_hit is None:
            return False, []
        matched_indices.add(err_hit)

    return True, sorted(matched_indices)


_RE_PEER_BULLET_ANY = re.compile(r"^- `([^`\n]+)` — .+ \((?:main|sub), (running|idle)\)(?:: .*)?$")
_RE_PEER_SIBLING_TASK = re.compile(r"^- `([^`\n]+)` — task \(sub, (idle|running)\)$")


def _match_peers_sibling_idle_vs_running_lines(
    diff_text: str,
    *,
    self_child_id: str | None,
    sibling_child_ids: set[str],
) -> set[int]:
    r"""Return indices (into `_parse_unified_diff_changed_lines(diff_text)[0]`) matched by the narrow
    `peers_sibling_idle_vs_running` rule.

    Matches ONLY inside a `# Peers` hunk where EVERY changed line in the peer list is a strict
    1-to-1 pair for the same `task (sub, ...)` sibling id of the same case:
      - omp (`-`): `- `<sibling>` — task (sub, idle)`
      - port (`+`): `- `<sibling>` — task (sub, running)`
    Never matches `Main`, `self_child_id`, unknown/different peer ids, reversed statuses,
    non-`task (sub, ...)` peers, or reordered rosters (running must precede idle on both sides
    and no other peer may move).
    """
    if not sibling_child_ids:
        return set()
    raw_lines = diff_text.splitlines()
    i = 0
    if len(raw_lines) >= 2 and raw_lines[0].startswith("--- ") and raw_lines[1].startswith("+++ "):
        i = 2

    # Split into hunks while tracking the exact flat index of each changed line to stay in lockstep
    # with `_parse_unified_diff_changed_lines`.
    hunks: list[list[tuple[str, str, int | None]]] = []
    cur_hunk: list[tuple[str, str, int | None]] | None = None
    change_idx = 0
    while i < len(raw_lines):
        raw_line = raw_lines[i]
        i += 1
        if raw_line.startswith("@@ ") and raw_line.endswith(" @@"):
            cur_hunk = []
            hunks.append(cur_hunk)
            continue
        if raw_line.startswith("[isError differs:") or raw_line.startswith("\\ "):
            continue
        if cur_hunk is None and not raw_line:
            continue
        if cur_hunk is None:
            cur_hunk = []
            hunks.append(cur_hunk)
        if raw_line.startswith("-"):
            cur_hunk.append(("-", raw_line[1:], change_idx))
            change_idx += 1
        elif raw_line.startswith("+"):
            cur_hunk.append(("+", raw_line[1:], change_idx))
            change_idx += 1
        elif raw_line.startswith(" "):
            cur_hunk.append((" ", raw_line[1:], None))
        else:
            cur_hunk.append((" ", raw_line, None))

    matched_change_indices: set[int] = set()
    for hunk in hunks:
        # Verify this hunk is inside the `# Peers` section (header line must precede any changed line).
        saw_peers_header = False
        peers_list_started = False
        omp_peer_statuses: list[str] = []
        pig_peer_statuses: list[str] = []
        omp_removed: dict[str, int] = {}
        pig_added: dict[str, int] = {}
        hunk_change_indices: list[int] = []
        valid_hunk = True

        for sign, content, cidx in hunk:
            if sign == " ":
                if content == "# Peers" or "Peers when this list was written:" in content:
                    saw_peers_header = True
                    if "Peers when this list was written:" in content:
                        peers_list_started = True
                    continue
                if peers_list_started:
                    m_any = _RE_PEER_BULLET_ANY.match(content)
                    if m_any:
                        st = m_any.group(2)
                        omp_peer_statuses.append(st)
                        pig_peer_statuses.append(st)
                    elif content == "":
                        peers_list_started = False
                continue

            # Changed line (`-` or `+`)
            if not (saw_peers_header and peers_list_started):
                valid_hunk = False
                break
            m_sib = _RE_PEER_SIBLING_TASK.match(content)
            if not m_sib:
                valid_hunk = False
                break
            pid, status = m_sib.group(1), m_sib.group(2)
            if pid == "Main" or pid == self_child_id or pid not in sibling_child_ids:
                valid_hunk = False
                break
            assert cidx is not None
            hunk_change_indices.append(cidx)
            if sign == "-":
                if status != "idle" or pid in omp_removed:
                    valid_hunk = False
                    break
                omp_removed[pid] = cidx
                omp_peer_statuses.append("idle")
            else:
                if status != "running" or pid in pig_added:
                    valid_hunk = False
                    break
                pig_added[pid] = cidx
                pig_peer_statuses.append("running")

        if not valid_hunk or not omp_removed:
            continue
        # Strict 1-to-1 pairing on the exact same sibling id(s)
        if set(omp_removed.keys()) != set(pig_added.keys()):
            continue
        # Enforce status bucket order (`running` before `idle`) on both sides so a reordered
        # roster (e.g. an `idle` peer placed before a `running` peer) is rejected.
        def _buckets_ordered(statuses: list[str]) -> bool:
            seen_idle = False
            for st in statuses:
                if st == "idle":
                    seen_idle = True
                elif seen_idle:
                    return False
            return True

        if not _buckets_ordered(omp_peer_statuses) or not _buckets_ordered(pig_peer_statuses):
            continue
        matched_change_indices.update(hunk_change_indices)

    return matched_change_indices


def classify_surface_diff(
    kind: str | tuple[str, ...],
    diff_text: str,
    allowlist: list[dict[str, Any]],
    *,
    target: str = "",
    case_name: str = "",
    self_child_id: str | None = None,
    sibling_child_ids: list[str] | set[str] | None = None,
) -> tuple[bool, list[int], list[str]]:
    """Classify a surface unified diff (system_prompt, tool_description, child_tool_description,
    tool_schema, child_system_prompt, followup) against `surface_allowlist.json`.

    Every changed `+/-` line (with diff headers parsed by position) must match a candidate rule.
    Returns `(all_listed, matched_entry_indices, unexplained_lines)`.
    """
    changed_lines, _ = _parse_unified_diff_changed_lines(diff_text)
    if not changed_lines:
        return True, [], []

    kinds = (kind,) if isinstance(kind, str) else tuple(kind)
    candidates: list[tuple[int, dict[str, Any], re.Pattern[str]]] = []
    structural_matched: dict[int, int] = {}  # change_idx -> allowlist_rule_idx
    sib_set = set(sibling_child_ids or ())
    for idx, rule in enumerate(allowlist):
        if rule.get("kind") not in kinds:
            continue
        r_target = rule.get("target", "*")
        if r_target != "*" and r_target != target:
            continue
        r_case = rule.get("case", "*")
        if r_case != "*" and r_case != case_name:
            continue
        if rule.get("match_kind") == "peers_sibling_idle_vs_running":
            for cidx in _match_peers_sibling_idle_vs_running_lines(
                diff_text, self_child_id=self_child_id, sibling_child_ids=sib_set
            ):
                structural_matched.setdefault(cidx, idx)
            continue
        if rule.get("regex") is None:
            continue
        candidates.append((idx, rule, re.compile(str(rule["regex"]))))
    matched_indices: set[int] = set()
    unexplained: list[str] = []
    for cidx, (sign, line) in enumerate(changed_lines):
        if cidx in structural_matched:
            matched_indices.add(structural_matched[cidx])
            continue
        hit: int | None = None
        for idx, rule, pat in candidates:
            r_side = rule.get("side")
            if r_side == "omp" and sign != "-":
                continue
            if r_side == "pig" and sign != "+":
                continue
            if pat.search(line):
                hit = idx
                break
        if hit is None:
            unexplained.append(f"{sign}{line}")
        else:
            matched_indices.add(hit)
    return (len(unexplained) == 0), sorted(matched_indices), unexplained

def classify_tool_list_membership(
    kind: str,
    only_omp: list[str],
    only_pig: list[str],
    allowlist: list[dict[str, Any]],
    *,
    case_name: str = "",
) -> tuple[bool, list[int], list[str]]:
    """Classify tools present only on one side (e.g. child `only_omp: ['task']`) against `surface_allowlist.json`."""
    matched: set[int] = set()
    unexplained: list[str] = []
    for side_tag, names in (("omp-only", only_omp), ("port-only", only_pig)):
        for name in names:
            hit: int | None = None
            for idx, rule in enumerate(allowlist):
                if rule.get("kind") != kind:
                    continue
                if rule.get("side") != side_tag:
                    continue
                if rule.get("target") != name:
                    continue
                r_case = rule.get("case", "*")
                if r_case != "*" and r_case != case_name:
                    continue
                hit = idx
                break
            if hit is None:
                unexplained.append(f"{side_tag}:{name}")
            else:
                matched.add(hit)
    return (len(unexplained) == 0), sorted(matched), unexplained


def classify_tools_comparison(
    tc: dict[str, Any],
    allowlist: list[dict[str, Any]],
    *,
    is_child: bool = False,
    case_name: str = "",
) -> dict[str, Any]:
    """Annotate a `compare_tools()` dict with surface allowlist verdicts and hits."""
    out = dict(tc)
    mem_kind = "child_tool_membership" if is_child else "tool_membership"
    desc_kind: str | tuple[str, ...] = ("child_tool_description", "tool_description") if is_child else "tool_description"
    schema_kind: str | tuple[str, ...] = ("child_tool_schema", "tool_schema") if is_child else "tool_schema"
    hits: set[int] = set()
    unexplained_items: list[str] = []

    mem_ok, mem_hits, mem_unexp = classify_tool_list_membership(
        mem_kind, tc.get("only_omp") or [], tc.get("only_pig") or [], allowlist, case_name=case_name
    )
    hits.update(mem_hits)
    unexplained_items.extend(mem_unexp)
    if not tc.get("order_identical") and not (tc.get("only_omp") or tc.get("only_pig")):
        unexplained_items.append("tool-order-differs")

    new_per_tool: list[dict[str, Any]] = []
    any_diff = bool(tc.get("only_omp") or tc.get("only_pig") or not tc.get("order_identical"))
    for row in tc.get("per_tool") or []:
        r = dict(row)
        if r.get("status") == "differs":
            any_diff = True
            t_ok = True
            t_hits: list[int] = []
            if not r.get("schema_identical"):
                s_ok, s_hits, s_unexp = classify_surface_diff(
                    schema_kind, r.get("schema_diff") or "", allowlist, target=r["name"], case_name=case_name
                )
                hits.update(s_hits)
                t_hits.extend(s_hits)
                if not s_ok:
                    t_ok = False
                    unexplained_items.append(f"schema:{r['name']} ({len(s_unexp)} lines)")
                r["schema_verdict"] = "listed" if s_ok else "differs"
                r["schema_unexplained"] = s_unexp
            else:
                r["schema_verdict"] = "identical"
                r["schema_unexplained"] = []
            if not r.get("description_identical"):
                d_ok, d_hits, d_unexp = classify_surface_diff(
                    desc_kind, r.get("description_diff") or "", allowlist, target=r["name"], case_name=case_name
                )
                hits.update(d_hits)
                t_hits.extend(d_hits)
                if not d_ok:
                    t_ok = False
                    unexplained_items.append(f"description:{r['name']} ({len(d_unexp)} lines)")
                r["description_verdict"] = "listed" if d_ok else "differs"
                r["description_unexplained"] = d_unexp
            else:
                r["description_verdict"] = "identical"
                r["description_unexplained"] = []
            r["surface_allowlist_hits"] = sorted(set(t_hits))
            r["verdict"] = "listed" if t_ok else "differs"
        elif r.get("status") in ("only_omp", "only_pig"):
            side_tag = "omp-only" if r["status"] == "only_omp" else "port-only"
            r["verdict"] = "differs" if f"{side_tag}:{r['name']}" in mem_unexp else "listed"
        else:
            r["verdict"] = "identical"
        new_per_tool.append(r)

    out["per_tool"] = new_per_tool
    out["surface_allowlist_hits"] = sorted(hits)
    out["unexplained"] = unexplained_items
    if not any_diff:
        out["verdict"] = "identical"
    elif not unexplained_items:
        out["verdict"] = "listed"
    else:
        out["verdict"] = "differs"
    return out


def classify_surface_snapshot(
    snap: dict[str, Any],
    allowlist: list[dict[str, Any]],
) -> dict[str, Any]:
    """Classify a parent surface snapshot (system prompt + tools comparison) against `surface_allowlist.json`."""
    out = dict(snap)
    hits: set[int] = set()
    unexplained: list[str] = []
    any_diff = False

    sp = dict(snap.get("system_prompt") or {})
    if sp.get("diff"):
        any_diff = True
        sp_ok, sp_hits, sp_unexp = classify_surface_diff(
            "system_prompt", sp["diff"], allowlist, target=str(snap.get("label", "")), case_name=str(snap.get("case", ""))
        )
        sp["verdict"] = "listed" if sp_ok else "differs"
        sp["surface_allowlist_hits"] = sp_hits
        sp["unexplained"] = sp_unexp
        hits.update(sp_hits)
        if not sp_ok:
            unexplained.append(f"system_prompt ({len(sp_unexp)} lines)")
    out["system_prompt"] = sp

    if snap.get("tools_comparison"):
        tc = classify_tools_comparison(
            snap["tools_comparison"], allowlist, is_child=False, case_name=str(snap.get("case", ""))
        )
        out["tools_comparison"] = tc
        hits.update(tc.get("surface_allowlist_hits") or [])
        if tc.get("verdict") != "identical":
            any_diff = True
        unexplained.extend(tc.get("unexplained") or [])

    out["surface_allowlist_hits"] = sorted(hits)
    out["unexplained"] = unexplained
    if not any_diff:
        out["verdict"] = "identical"
    elif not unexplained:
        out["verdict"] = "listed"
    else:
        out["verdict"] = "differs"
    return out


def classify_followup_messages(
    case_name: str,
    o_added: list[list[dict[str, Any]]] | None,
    p_added: list[list[dict[str, Any]]] | None,
    call_diffs: list[dict[str, Any]],
    surface_allowlist: list[dict[str, Any]],
) -> dict[str, Any]:
    """Compare and classify added per-turn follow-up messages.

    Tool-result echoes (`role == 'tool'` with `tool_call_id == 'oracle_call_<N>'`) whose call
    text diff was already explicitly listed (`verdict == 'differs-listed'`) inherit that listing;
    any non-tool follow-up message (e.g., vision image attachment, `<system-reminder>`,
    `<system-notice>`) or unlisted tool-result diff must be explicitly matched by `surface_allowlist.json`
    or the case's followups verdict is `differs`.
    """
    if o_added is None or p_added is None:
        return {}
    o_txt = json.dumps(o_added, indent=2, ensure_ascii=False)
    p_txt = json.dumps(p_added, indent=2, ensure_ascii=False)
    if o_txt == p_txt:
        return {"verdict": "identical", "diff": "", "surface_allowlist_hits": [], "unexplained": []}

    diff_str = unified_diff(o_txt, p_txt, "omp:messages", "oh-my-pig:messages")
    listed_tool_call_ids = {
        f"oracle_call_{c['index']}"
        for c in call_diffs
        if c.get("verdict") == "differs-listed"
    }
    hits: set[int] = set()
    unexplained: list[str] = []

    if len(o_added) != len(p_added):
        unexplained.append(f"turn-batch-count:{len(o_added)} vs {len(p_added)}")
    else:
        for ti, (ob, pb) in enumerate(zip(o_added, p_added)):
            if len(ob) != len(pb):
                unexplained.append(f"turn[{ti}]-msg-count:{len(ob)} vs {len(pb)}")
                continue
            for mi, (om, pm) in enumerate(zip(ob, pb)):
                if om == pm:
                    continue
                if (
                    om.get("role") == "tool"
                    and pm.get("role") == "tool"
                    and om.get("tool_call_id") == pm.get("tool_call_id")
                    and om.get("tool_call_id") in listed_tool_call_ids
                ):
                    continue
                mdiff = unified_diff(
                    json.dumps(om, indent=2, ensure_ascii=False),
                    json.dumps(pm, indent=2, ensure_ascii=False),
                    f"omp:followup[{ti}][{mi}]",
                    f"oh-my-pig:followup[{ti}][{mi}]",
                )
                f_ok, f_hits, f_unexp = classify_surface_diff(
                    "followup",
                    mdiff,
                    surface_allowlist,
                    target=str(om.get("role") or ""),
                    case_name=case_name,
                )
                hits.update(f_hits)
                if not f_ok:
                    unexplained.append(f"turn[{ti}][{mi}] ({om.get('role')}/{om.get('tool_call_id')}): {len(f_unexp)} lines")

    return {
        "verdict": "listed" if not unexplained else "differs",
        "diff": diff_str,
        "surface_allowlist_hits": sorted(hits),
        "unexplained": unexplained,
    }


def classify_recorded_call_diff(
    tool: str,
    omp_args: Any,
    omp_intent: Any,
    pig_args: Any,
    pig_intent: Any,
    allowlist: list[dict[str, Any]],
) -> tuple[bool, list[int]]:
    """Check whether a recorded tool_execution_start diff is covered by recorded_allowlist.

    Supports `kind: "intent-in-args"`: matches ONLY when pig_intent is None, omp_intent is a
    non-empty string, pig_args equals omp_args plus `"i": omp_intent`, and no other argument
    key differs. Returns (is_listed, matched_entry_indices).
    """
    for idx, rule in enumerate(allowlist):
        r_tool = rule.get("tool", "*")
        if r_tool != "*" and r_tool != tool:
            continue
        kind = rule.get("kind")
        if kind == "intent-in-args":
            if (
                isinstance(omp_intent, str)
                and omp_intent != ""
                and pig_intent is None
                and isinstance(omp_args, dict)
                and isinstance(pig_args, dict)
                and "i" not in omp_args
                and pig_args.get("i") == omp_intent
            ):
                pig_without_i = {k: v for k, v in pig_args.items() if k != "i"}
                if json.dumps(omp_args, sort_keys=True) == json.dumps(pig_without_i, sort_keys=True):
                    return True, [idx]
    return False, []


def _evaluate_call_verdict(
    *,
    case_name: str,
    call_index: int,
    tool: str,
    arguments: dict[str, Any] | None,
    o_norm: str | None,
    p_norm: str | None,
    o_raw: str | None,
    p_raw: str | None,
    o_wall_ms: float | None,
    p_wall_ms: float | None,
    o_err: bool | None,
    p_err: bool | None,
    o_det: Any,
    p_det: Any,
    details_allowlist: list[dict[str, Any]],
    text_allowlist: list[dict[str, Any]],
) -> dict[str, Any]:
    """Compute unified per-call text + details verdict.

    Invariant (Audit Finding 1): a call with ANY unexplained details difference MUST be
    `differs` (or `details-only` when text & isError match), NEVER `differs-listed` or `timing`.
    Matched `text_allowlist.json` entries may declare `details_paths: [...]` listing the exact
    details paths implied by that listed text difference.
    """
    err_differs = o_err is not None and p_err is not None and o_err != p_err
    o_det_json = json.dumps(o_det, sort_keys=True, indent=2, ensure_ascii=False)
    p_det_json = json.dumps(p_det, sort_keys=True, indent=2, ensure_ascii=False)
    details_same = o_det_json == p_det_json
    details_diff = "" if details_same else unified_diff(o_det_json, p_det_json, "omp:details", "oh-my-pig:details")

    text_allow_idxs: list[int] = []
    implied_det_paths: list[str] = []
    diff_str = ""
    text_listed = False
    is_timing_text = False

    if o_norm is None and p_norm is None:
        return {
            "verdict": "missing_both",
            "diff": "",
            "details_verdict": "identical" if details_same else "differs",
            "details_diff": details_diff,
            "details_unexplained": [],
            "details_allowlist_hits": [],
            "text_allowlist_hits": [],
        }
    if o_norm is None:
        return {
            "verdict": "missing_omp",
            "diff": "",
            "details_verdict": "identical" if details_same else "differs",
            "details_diff": details_diff,
            "details_unexplained": [],
            "details_allowlist_hits": [],
            "text_allowlist_hits": [],
        }
    if p_norm is None:
        return {
            "verdict": "missing_pig",
            "diff": "",
            "details_verdict": "identical" if details_same else "differs",
            "details_diff": details_diff,
            "details_unexplained": [],
            "details_allowlist_hits": [],
            "text_allowlist_hits": [],
        }

    text_or_err_differs = (o_norm != p_norm) or err_differs
    if text_or_err_differs:
        diff_str = unified_diff(o_norm, p_norm) if o_norm != p_norm else ""
        if err_differs:
            diff_str += f"\n[isError differs: omp={o_err} vs oh-my-pig={p_err}]\n"
        text_listed, text_allow_idxs = classify_text_diff(
            case_name,
            call_index,
            tool,
            diff_str,
            text_allowlist,
            is_error_differs=err_differs,
            omp_is_error=o_err,
            pig_is_error=p_err,
        )
        if text_listed:
            for tidx in text_allow_idxs:
                for dp in text_allowlist[tidx].get("details_paths") or []:
                    implied_det_paths.append(str(dp))

    _, unexplained_det, det_allow_idxs = classify_details_diffs(
        tool,
        o_det,
        p_det,
        details_allowlist,
        case_name=case_name,
        call_index=call_index,
        omp_is_error=o_err,
        pig_is_error=p_err,
        omp_text=o_norm,
        arguments=arguments,
        implied_paths=implied_det_paths,
    )

    if text_or_err_differs and not err_differs:
        is_timing_text = _is_one_sided_wall_time_diff(
            o_norm,
            p_norm,
            omp_raw=o_raw,
            pig_raw=p_raw,
            omp_wall_ms=o_wall_ms,
            pig_wall_ms=p_wall_ms,
            unexplained_details=unexplained_det,
        )
        if is_timing_text:
            # Only wallTimeNoticeEmitted may differ for a valid timing crossing.
            unexplained_det = [d for d in unexplained_det if d.get("path") != "wallTimeNoticeEmitted"]

    if details_same:
        details_verdict = "identical"
    elif not unexplained_det:
        details_verdict = "listed"
    else:
        details_verdict = "differs"

    if not text_or_err_differs:
        if details_same:
            verdict = "identical"
        elif not unexplained_det:
            verdict = "details-listed"
        else:
            verdict = "details-only"
    elif is_timing_text and not unexplained_det:
        verdict = "timing"
    elif text_listed and not unexplained_det:
        verdict = "differs-listed"
    else:
        verdict = "differs"

    return {
        "verdict": verdict,
        "diff": diff_str,
        "details_verdict": details_verdict,
        "details_diff": details_diff,
        "details_unexplained": unexplained_det,
        "details_allowlist_hits": det_allow_idxs,
        "text_allowlist_hits": text_allow_idxs,
    }


def compare_case(
    case: dict[str, Any],
    omp_res: dict[str, Any] | None,
    pig_res: dict[str, Any] | None,
    *,
    details_allowlist: list[dict[str, Any]] | None = None,
    text_allowlist: list[dict[str, Any]] | None = None,
    recorded_allowlist: list[dict[str, Any]] | None = None,
    surface_allowlist: list[dict[str, Any]] | None = None,
) -> dict[str, Any]:
    d_allow = details_allowlist if details_allowlist is not None else []
    t_allow = text_allowlist if text_allowlist is not None else []
    r_allow = recorded_allowlist if recorded_allowlist is not None else []
    s_allow = surface_allowlist if surface_allowlist is not None else []
    case_name = str(case.get("name", "unnamed"))
    turns = _normalize_turn_list(case)
    flat_calls = [c for batch in turns for c in batch]
    call_diffs: list[dict[str, Any]] = []
    for i, spec in enumerate(flat_calls):
        oc = omp_res["calls"][i] if omp_res and i < len(omp_res["calls"]) else None
        pc = pig_res["calls"][i] if pig_res and i < len(pig_res["calls"]) else None
        # Report the actual expanded arguments dispatched (falling back to spec)
        shown_args = (
            oc["arguments"] if oc is not None else (pc["arguments"] if pc is not None else spec.get("arguments", {}))
        )
        entry: dict[str, Any] = {
            "index": i,
            "turn_index": oc["turn_index"] if oc else (pc["turn_index"] if pc else i),
            "tool": spec["tool"],
            "arguments": shown_args,
            "omp_raw": oc["raw_result"] if oc else None,
            "omp_normalized": oc["normalized_result"] if oc else None,
            "omp_normalizations": oc["normalizations_applied"] if oc else [],
            "omp_is_error": oc["is_error"] if oc else None,
            "omp_details": oc["details_normalized"] if oc else None,
            "omp_raw_wall_time_ms": oc.get("raw_wall_time_ms") if oc else None,
            "omp_recorded_args": oc.get("recorded_args") if oc else None,
            "omp_recorded_intent": oc.get("recorded_intent") if oc else None,
            "pig_raw": pc["raw_result"] if pc else None,
            "pig_normalized": pc["normalized_result"] if pc else None,
            "pig_normalizations": pc["normalizations_applied"] if pc else [],
            "pig_is_error": pc["is_error"] if pc else None,
            "pig_details": pc["details_normalized"] if pc else None,
            "pig_raw_wall_time_ms": pc.get("raw_wall_time_ms") if pc else None,
            "pig_recorded_args": pc.get("recorded_args") if pc else None,
            "pig_recorded_intent": pc.get("recorded_intent") if pc else None,
        }
        if omp_res and pig_res:
            entry["recorded_allowlist_hits"] = []
            # Compare recorded tool_execution_start args/intent (catches whether `i` was
            # stripped into `intent` vs left in `args` when intentTracing is on/off).
            o_rec = {"args": entry["omp_recorded_args"], "intent": entry["omp_recorded_intent"]}
            p_rec = {"args": entry["pig_recorded_args"], "intent": entry["pig_recorded_intent"]}
            o_rec_json = json.dumps(o_rec, sort_keys=True, indent=2, ensure_ascii=False)
            p_rec_json = json.dumps(p_rec, sort_keys=True, indent=2, ensure_ascii=False)
            if o_rec_json == p_rec_json:
                entry["recorded_call_verdict"] = "identical"
                entry["recorded_call_diff"] = ""
            else:
                rec_listed, rec_hits = classify_recorded_call_diff(
                    spec["tool"],
                    entry["omp_recorded_args"],
                    entry["omp_recorded_intent"],
                    entry["pig_recorded_args"],
                    entry["pig_recorded_intent"],
                    r_allow,
                )
                entry["recorded_allowlist_hits"] = rec_hits
                entry["recorded_call_verdict"] = "recorded-listed" if rec_listed else "recorded-differs"
                entry["recorded_call_diff"] = unified_diff(
                    o_rec_json, p_rec_json, "omp:recorded_call", "oh-my-pig:recorded_call"
                )
            o_missing_tools = omp_res.get("missing_tools") or []
            p_missing_tools = pig_res.get("missing_tools") or []
            if o_missing_tools or p_missing_tools:
                entry["verdict"] = "missing_tool"
                entry["diff"] = ""
                entry["details_verdict"] = "missing_tool"
                entry["details_diff"] = ""
                entry["details_unexplained"] = []
                entry["details_allowlist_hits"] = []
                entry["text_allowlist_hits"] = []
            else:
                ev_res = _evaluate_call_verdict(
                    case_name=case_name,
                    call_index=i,
                    tool=spec["tool"],
                    arguments=shown_args,
                    o_norm=oc["normalized_result"] if oc else None,
                    p_norm=pc["normalized_result"] if pc else None,
                    o_raw=oc["raw_result"] if oc else None,
                    p_raw=pc["raw_result"] if pc else None,
                    o_wall_ms=oc.get("raw_wall_time_ms") if oc else None,
                    p_wall_ms=pc.get("raw_wall_time_ms") if pc else None,
                    o_err=oc["is_error"] if oc else None,
                    p_err=pc["is_error"] if pc else None,
                    o_det=oc["details_normalized"] if oc else None,
                    p_det=pc["details_normalized"] if pc else None,
                    details_allowlist=d_allow,
                    text_allowlist=t_allow,
                )
                entry.update(ev_res)
        elif omp_res:
            if omp_res.get("missing_tools"):
                entry["verdict"] = "missing_tool"
            else:
                entry["verdict"] = "omp_only" if (oc and oc["captured"]) else "missing_omp"
            entry["details_verdict"] = "omp_only"
            entry["recorded_call_verdict"] = "omp_only"
            entry["recorded_call_diff"] = ""
            entry["details_diff"] = ""
            entry["details_unexplained"] = []
            entry["details_allowlist_hits"] = []
            entry["text_allowlist_hits"] = []
            entry["recorded_allowlist_hits"] = []
            entry["diff"] = ""
        else:
            if pig_res and pig_res.get("missing_tools"):
                entry["verdict"] = "missing_tool"
            else:
                entry["verdict"] = "pig_only" if (pc and pc["captured"]) else "missing_pig"
            entry["details_verdict"] = "pig_only"
            entry["recorded_call_verdict"] = "pig_only"
            entry["recorded_call_diff"] = ""
            entry["details_diff"] = ""
            entry["details_unexplained"] = []
            entry["details_allowlist_hits"] = []
            entry["text_allowlist_hits"] = []
            entry["recorded_allowlist_hits"] = []
            entry["diff"] = ""
        call_diffs.append(entry)

    # Compare and classify non-tool follow-up / reminder messages added between consecutive parent requests
    o_added = omp_res["added_messages_normalized"] if omp_res else None
    p_added = pig_res["added_messages_normalized"] if pig_res else None
    followup_diff = classify_followup_messages(case_name, o_added, p_added, call_diffs, s_allow)

    # Compare and classify EVERY child subagent surface, paired deterministically by child agent id.
    o_csurfs_raw = omp_res.get("child_surfaces") if omp_res else None
    p_csurfs_raw = pig_res.get("child_surfaces") if pig_res else None
    # Backward-compatible fallback for synthetic/legacy callers providing only single child_* fields.
    if not o_csurfs_raw and omp_res and (omp_res.get("child_system_prompt_normalized") is not None or omp_res.get("child_tools")):
        o_sys_legacy = omp_res.get("child_system_prompt_normalized")
        o_cid = _extract_child_agent_id(o_sys_legacy or "", 0)
        o_csurfs_raw = {o_cid: {"id": o_cid, "system_prompt_normalized": o_sys_legacy, "tools": omp_res.get("child_tools") or []}}
    if not p_csurfs_raw and pig_res and (pig_res.get("child_system_prompt_normalized") is not None or pig_res.get("child_tools")):
        p_sys_legacy = pig_res.get("child_system_prompt_normalized")
        p_cid = _extract_child_agent_id(p_sys_legacy or "", 0)
        p_csurfs_raw = {p_cid: {"id": p_cid, "system_prompt_normalized": p_sys_legacy, "tools": pig_res.get("child_tools") or []}}
    o_csurfs: dict[str, dict[str, Any]] = dict(o_csurfs_raw or {})
    p_csurfs: dict[str, dict[str, Any]] = dict(p_csurfs_raw or {})

    child_surfaces_by_id: dict[str, dict[str, Any]] = {}
    child_surface: dict[str, Any] = {}
    all_child_ids = sorted(set(o_csurfs) | set(p_csurfs))
    if all_child_ids:
        agg_hits: set[int] = set()
        agg_unexp: list[str] = []
        agg_any_diff = False
        agg_any_missing = False
        for cid in all_child_ids:
            oc_s = o_csurfs.get(cid)
            pc_s = p_csurfs.get(cid)
            o_csys = oc_s.get("system_prompt_normalized") if oc_s else None
            p_csys = pc_s.get("system_prompt_normalized") if pc_s else None
            o_ctools = (oc_s.get("tools") or []) if oc_s else []
            p_ctools = (pc_s.get("tools") or []) if pc_s else []
            cs_entry: dict[str, Any] = {
                "id": cid,
                "omp_child_sys_len": len(o_csys) if o_csys is not None else None,
                "pig_child_sys_len": len(p_csys) if p_csys is not None else None,
                "omp_child_tools": [t["name"] for t in o_ctools],
                "pig_child_tools": [t["name"] for t in p_ctools],
            }
            if omp_res and pig_res:
                if oc_s is None or pc_s is None:
                    missing_side = "omp" if oc_s is None else "pig"
                    cs_entry["verdict"] = "missing"
                    cs_entry["missing_side"] = missing_side
                    cs_entry["sys_verdict"] = "missing"
                    cs_entry["sys_diff"] = ""
                    cs_entry["sys_unexplained"] = []
                    cs_entry["surface_allowlist_hits"] = []
                    cs_entry["unexplained"] = [f"missing_on_{missing_side}:{cid}"]
                    agg_any_missing = True
                    agg_unexp.append(f"{cid}: missing on {missing_side}")
                else:
                    c_hits: set[int] = set()
                    c_unexp: list[str] = []
                    c_any_diff = False
                    if o_csys is not None and p_csys is not None:
                        if o_csys == p_csys:
                            cs_entry["sys_verdict"] = "identical"
                            cs_entry["sys_diff"] = ""
                            cs_entry["sys_unexplained"] = []
                        else:
                            c_any_diff = True
                            sdiff = unified_diff(o_csys, p_csys, f"omp:child_sys:{cid}", f"oh-my-pig:child_sys:{cid}")
                            sib_ids = [other for other in all_child_ids if other != cid]
                            cs_ok, cs_hits, cs_unexp = classify_surface_diff(
                                "child_system_prompt",
                                sdiff,
                                s_allow,
                                target="child",
                                case_name=case_name,
                                self_child_id=cid,
                                sibling_child_ids=sib_ids,
                            )
                            cs_entry["sys_verdict"] = "listed" if cs_ok else "differs"
                            cs_entry["sys_diff"] = sdiff
                            cs_entry["sys_unexplained"] = cs_unexp
                            c_hits.update(cs_hits)
                            if not cs_ok:
                                c_unexp.append(f"child_sys ({len(cs_unexp)} lines)")
                    elif o_csys != p_csys:
                        c_any_diff = True
                        cs_entry["sys_verdict"] = "missing"
                        c_unexp.append(f"child_sys missing on {'omp' if o_csys is None else 'pig'}")
                    if o_ctools and p_ctools:
                        ctc = classify_tools_comparison(
                            compare_tools(o_ctools, p_ctools), s_allow, is_child=True, case_name=case_name
                        )
                        cs_entry["tools_comparison"] = ctc
                        c_hits.update(ctc.get("surface_allowlist_hits") or [])
                        if ctc.get("verdict") != "identical":
                            c_any_diff = True
                        c_unexp.extend(ctc.get("unexplained") or [])
                    elif bool(o_ctools) != bool(p_ctools):
                        c_any_diff = True
                        c_unexp.append(f"child_tools missing on {'omp' if not o_ctools else 'pig'}")
                    cs_entry["surface_allowlist_hits"] = sorted(c_hits)
                    cs_entry["unexplained"] = c_unexp
                    cs_entry["verdict"] = "identical" if not c_any_diff else ("listed" if not c_unexp else "differs")
                    agg_hits.update(c_hits)
                    if c_any_diff:
                        agg_any_diff = True
                    for u in c_unexp:
                        agg_unexp.append(f"{cid}: {u}")
            child_surfaces_by_id[cid] = cs_entry

        # Populate top-level summary `child_surface` (keeping single-child fields from first child id for compatibility)
        first_cs = child_surfaces_by_id[all_child_ids[0]]
        child_surface = {
            "ids": all_child_ids,
            "by_id": child_surfaces_by_id,
            "omp_child_sys_len": first_cs.get("omp_child_sys_len"),
            "pig_child_sys_len": first_cs.get("pig_child_sys_len"),
            "omp_child_tools": first_cs.get("omp_child_tools"),
            "pig_child_tools": first_cs.get("pig_child_tools"),
            "sys_verdict": first_cs.get("sys_verdict"),
            "sys_diff": first_cs.get("sys_diff", ""),
            "sys_unexplained": first_cs.get("sys_unexplained", []),
        }
        if "tools_comparison" in first_cs:
            child_surface["tools_comparison"] = first_cs["tools_comparison"]
        if omp_res and pig_res:
            child_surface["surface_allowlist_hits"] = sorted(agg_hits)
            child_surface["unexplained"] = agg_unexp
            if agg_any_missing:
                child_surface["verdict"] = "missing"
            elif not agg_any_diff:
                child_surface["verdict"] = "identical"
            elif not agg_unexp:
                child_surface["verdict"] = "listed"
            else:
                child_surface["verdict"] = "differs"

    return {
        "name": case.get("name", "unnamed"),
        "description": case.get("description", ""),
        "omp": _strip_heavy(omp_res),
        "pig": _strip_heavy(pig_res),
        "calls": call_diffs,
        "followups": followup_diff,
        "child_surface": child_surface,
    }
def _case_failed(report_entry: dict[str, Any]) -> bool:
    for side_key in ("omp", "pig"):
        s = report_entry.get(side_key)
        if s is not None:
            if (
                bool(s.get("missing_tools"))
                or s.get("rc") != 0
                or s.get("timed_out")
                or s.get("setup_rc") != 0
                or bool(s.get("network_attempts"))
                or bool(s.get("leaked_pids"))
            ):
                return True
    for call in report_entry.get("calls") or []:
        if call.get("verdict") in ("missing_both", "missing_omp", "missing_pig", "missing_tool"):
            return True
    return False

def _stderr_tail(text: str, max_lines: int = 12) -> str:
    lines = [l for l in (text or "").splitlines() if l.strip()]
    return "\n".join(lines[-max_lines:])


def render_markdown(report: dict[str, Any]) -> str:
    lines: list[str] = []
    lines.append("# oh-my-pig vs omp — Per-Tool Oracle Report")
    lines.append("")
    t = report["totals"]
    lines.append(f"- Mode: `{report['mode']}`")
    lines.append(f"- Sandbox: `{report['sandbox']}`")
    lines.append(f"- Input files: {', '.join(f'`{f}`' for f in report['input_files'])}")
    lines.append(
        f"- Totals: {t['cases']} cases ({t['cases_failed']} failed), "
        f"{t['calls']} calls ({t['calls_identical']} identical, {t['calls_details_listed']} details-listed, "
        f"{t['calls_details_only']} details-only, {t['calls_differs_listed']} differs-listed, "
        f"{t.get('calls_timing', 0)} timing, {t['calls_differs']} differs, "
        f"{t['calls_missing']} missing, {t['calls_single']} single-side), "
        f"recorded=(identical={t.get('recorded_identical', 0)}, recorded-listed={t.get('recorded_listed', 0)}, "
        f"recorded-differs={t.get('recorded_differs', 0)}), "
        f"surfaces=(snapshots_listed={t.get('surfaces_listed', 0)}, snapshots_differs={t.get('surfaces_differs', 0)}, "
        f"child_listed={t.get('child_surfaces_listed', 0)}, child_differs={t.get('child_surfaces_differs', 0)}, "
        f"child_missing={t.get('child_surfaces_missing', 0)}, "
        f"followups_listed={t.get('followups_listed', 0)}, followups_differs={t.get('followups_differs', 0)}), "
        f"network_attempts={t['network_attempts']}, leaked_pids={t['leaked_pids']}"
    )
    lines.append("")
    timing_calls = [
        (c["name"], x)
        for c in report["cases"]
        for x in c["calls"]
        if x.get("verdict") == "timing"
    ]
    if timing_calls:
        lines.append("## Timing-Threshold Calls (`timing`)")
        lines.append("")
        for cname, x in timing_calls:
            side_with = "omp" if _RE_WALL_TIME_NORM.search(x.get("omp_normalized") or "") else "oh-my-pig"
            lines.append(
                f"- `{cname}#{x['index']}` (`{x['tool']}`): wall-time notice emitted on `{side_with}` only (within 1.0–1.3s threshold band)."
            )
        lines.append("")
    if report["network_attempts"]:
        lines.append("## BLOCKING: Network Attempts Recorded by Refusing Proxy")
        lines.append("")
        for a in report["network_attempts"]:
            lines.append(f"- `{a['case']}` ({a['side']}): `{a['method']} {a['target']}` (Host: `{a['host']}`)")
        lines.append("")

    if report["leaked_pids"]:
        lines.append("## BLOCKING: Processes Left Behind")
        lines.append("")
        for lp in report["leaked_pids"]:
            lines.append(f"- `{lp['case']}` ({lp['side']}): PIDs `{lp['pids']}`")
        lines.append("")

    lines.append("## Normalizations applied before diffing")
    lines.append("")
    for name, desc in report["normalizations"]:
        lines.append(f"- `{name}`: {desc}")
    lines.append("")
    for section_title, key in (
        ("Details Allowlist Usage", "details_allowlist_usage"),
        ("Text Allowlist Usage", "text_allowlist_usage"),
        ("Recorded-Call Allowlist Usage", "recorded_allowlist_usage"),
        ("Surface Allowlist Usage", "surface_allowlist_usage"),
    ):
        usage = report.get(key) or []
        if not usage:
            continue
        lines.append(f"## {section_title}")
        lines.append("")
        if key == "details_allowlist_usage":
            lines.append("| # | Tool | Path | Side | Class | Ref | Hits | Status |")
            lines.append("|---|---|---|---|---|---|---|---|")
            for row in usage:
                status = "real-model-only" if (row["hits"] == 0 and row.get("real_model_only")) else ("STALE" if row["stale"] else "used")
                p_label = row.get("path") or row.get("match_kind") or ""
                s_label = row.get("side") or row.get("match_kind") or ""
                lines.append(
                    f"| {row['index']} | `{row['tool']}` | `{p_label}` | `{s_label}` | "
                    f"`{row['class']}` | `{row['ref']}` | {row['hits']} | `{status}` |"
                )
        elif key == "text_allowlist_usage":
            lines.append("| # | Case | Call | Tool | Class | Ref | Hits | Status |")
            lines.append("|---|---|---|---|---|---|---|---|")
            for row in usage:
                status = "STALE" if row["hits"] == 0 else "used"
                lines.append(
                    f"| {row['index']} | `{row['case']}` | `{row['call_index']}` | `{row['tool']}` | "
                    f"`{row['class']}` | `{row['ref']}` | {row['hits']} | `{status}` |"
                )
        elif key == "surface_allowlist_usage":
            lines.append("| # | Kind | Target | Case | Class | Ref | Hits | Status |")
            lines.append("|---|---|---|---|---|---|---|---|")
            for row in usage:
                status = "STALE" if row["hits"] == 0 else "used"
                lines.append(
                    f"| {row['index']} | `{row['kind']}` | `{row.get('target', '*')}` | `{row.get('case', '*')}` | "
                    f"`{row['class']}` | `{row['ref']}` | {row['hits']} | `{status}` |"
                )
        else:
            lines.append("| # | Tool | Kind | Class | Ref | Hits | Status |")
            lines.append("|---|---|---|---|---|---|---|")
            for row in usage:
                status = "STALE" if row["hits"] == 0 else "used"
                lines.append(
                    f"| {row['index']} | `{row['tool']}` | `{row['kind']}` | "
                    f"`{row['class']}` | `{row['ref']}` | {row['hits']} | `{status}` |"
                )
        lines.append("")
    # Surface snapshots (every distinct prompt/toolset seen across cases, e.g. default vs skills-enabled)
    lines.append("## Advertised Tools, Schemas & System Prompts")
    lines.append("")
    for snap in report.get("surface_snapshots") or []:
        sv = f" — `{snap['verdict']}`" if snap.get("verdict") else ""
        lines.append(f"### Surface `{snap['label']}` (from case `{snap['case']}`){sv}")
        lines.append("")
        if snap.get("unexplained"):
            lines.append(f"- Unexplained surface diffs: {', '.join(f'`{u}`' for u in snap['unexplained'])}")
        if snap.get("tools_comparison"):
            tc = snap["tools_comparison"]
            lines.append(f"- omp tools ({len(tc['omp_names'])}): `{', '.join(tc['omp_names'])}`")
            lines.append(f"- oh-my-pig tools ({len(tc['pig_names'])}): `{', '.join(tc['pig_names'])}`")
            lines.append(f"- Tool order identical: `{tc['order_identical']}`")
            if tc["only_omp"]:
                lines.append(f"- Only in omp: `{', '.join(tc['only_omp'])}`")
            if tc["only_pig"]:
                lines.append(f"- Only in oh-my-pig: `{', '.join(tc['only_pig'])}`")
            lines.append("")
            lines.append("| Tool | Status | Verdict | Schema (order-preserving) | Description |")
            lines.append("|---|---|---|---|---|")
            for row in tc["per_tool"]:
                if row["status"] in ("only_omp", "only_pig"):
                    lines.append(f"| `{row['name']}` | `{row['status']}` | `{row.get('verdict', row['status'])}` | — | — |")
                else:
                    lines.append(
                        f"| `{row['name']}` | `{row['status']}` | `{row.get('verdict', row['status'])}` | "
                        f"{row.get('schema_verdict', 'identical' if row['schema_identical'] else 'differs')} | "
                        f"{row.get('description_verdict', 'identical' if row['description_identical'] else 'differs')} |"
                    )
            lines.append("")
            for row in tc["per_tool"]:
                if row.get("schema_diff"):
                    lines.append(f"#### Schema diff: `{row['name']}`")
                    lines.append("```diff")
                    lines.append(row["schema_diff"].rstrip("\n"))
                    lines.append("```")
                    lines.append("")
                if row.get("description_diff"):
                    lines.append(f"#### Description diff: `{row['name']}`")
                    lines.append("```diff")
                    lines.append(row["description_diff"].rstrip("\n"))
                    lines.append("```")
                    lines.append("")
        elif snap.get("omp_tool_names"):
            lines.append(f"- omp tools ({len(snap['omp_tool_names'])}): `{', '.join(snap['omp_tool_names'])}`")
        elif snap.get("pig_tool_names"):
            lines.append(f"- oh-my-pig tools ({len(snap['pig_tool_names'])}): `{', '.join(snap['pig_tool_names'])}`")

        sp = snap.get("system_prompt") or {}
        if sp.get("verdict") == "identical":
            lines.append("- System prompt: `identical` (after normalization).")
        elif sp.get("verdict") in ("differs", "listed"):
            lines.append(f"- System prompt: `{sp['verdict']}` (omp: {sp.get('omp_len', 0)} chars, oh-my-pig: {sp.get('pig_len', 0)} chars).")
            lines.append("<details><summary>System prompt unified diff</summary>\n\n```diff\n" + (sp.get("diff") or "").rstrip("\n") + "\n```\n</details>")
        elif sp.get("omp_len") is not None:
            lines.append(f"- System prompt (omp-only): {sp['omp_len']} chars.")
        elif sp.get("pig_len") is not None:
            lines.append(f"- System prompt (oh-my-pig-only): {sp['pig_len']} chars.")
        lines.append("")

    # Per-case summary table
    lines.append("## Case Summary")
    lines.append("| Case | Call | Tool | isError (omp/pig) | Verdict | Details | Normalizations |")
    lines.append("|---|---|---|---|---|---|---|")
    for c in report["cases"]:
        for call in c["calls"]:
            norms = sorted(set(call.get("omp_normalizations", [])) | set(call.get("pig_normalizations", [])))
            norm_s = ", ".join(norms) if norms else "none"
            ie = f"{call['omp_is_error']}/{call['pig_is_error']}"
            dv = call.get("details_verdict", "—")
            lines.append(f"| `{c['name']}` | {call['index']} | `{call['tool']}` | `{ie}` | `{call['verdict']}` | `{dv}` | {norm_s} |")

    # Detailed per-case results
    lines.append("## Per-Call Details")
    lines.append("")
    for c in report["cases"]:
        lines.append(f"### Case: `{c['name']}`")
        if c.get("description"):
            lines.append(c["description"])
        for side_key, label in (("omp", "omp"), ("pig", "oh-my-pig")):
            s = c.get(side_key)
            if not s:
                continue
            mt = s.get("missing_tools") or []
            mt_s = f" missing_tools={mt}" if mt else ""
            lines.append(
                f"- {label}: rc={s['rc']} setup_rc={s['setup_rc']} timed_out={s['timed_out']}{mt_s} "
                f"reqs={s['parent_request_count']} child_reqs={s['child_request_count']} "
                f"net_attempts={len(s.get('network_attempts') or [])} leaked_pids={s.get('leaked_pids') or []} "
                f"({s['elapsed_ms']}ms)"
            )
            if mt or s["rc"] != 0 or s["setup_rc"] != 0 or s["timed_out"]:
                tail = _stderr_tail(s.get("stderr", ""))
                if tail:
                    lines.append(f"  {label} stderr tail:")
                    lines.append("  ```text")
                    for tl in tail.splitlines():
                        lines.append(f"  {tl}")
                    lines.append("  ```")
        if c.get("child_surface"):
            cs = c["child_surface"]
            by_id = cs.get("by_id")
            if isinstance(by_id, dict) and by_id:
                lines.append(
                    f"- subagent child surfaces ({len(by_id)}): ids={cs.get('ids')} verdict={cs.get('verdict')}"
                )
                for cid, entry_cs in by_id.items():
                    lines.append(
                        f"  - child `{cid}`: verdict={entry_cs.get('verdict')} sys_verdict={entry_cs.get('sys_verdict')} "
                        f"omp_tools={entry_cs.get('omp_child_tools')} pig_tools={entry_cs.get('pig_child_tools')} "
                        f"omp_sys_len={entry_cs.get('omp_child_sys_len')} pig_sys_len={entry_cs.get('pig_child_sys_len')}"
                    )
            else:
                lines.append(
                    f"- subagent child surface: omp_tools={cs.get('omp_child_tools')} "
                    f"pig_tools={cs.get('pig_child_tools')} omp_sys_len={cs.get('omp_child_sys_len')} "
                    f"pig_sys_len={cs.get('pig_child_sys_len')} sys_verdict={cs.get('sys_verdict')}"
                )
        lines.append("")
        for call in c["calls"]:
            rc_note = (
                f", recorded_call={call['recorded_call_verdict']}"
                if call.get("recorded_call_verdict") in ("recorded-differs", "recorded-listed", "differs")
                else ""
            )
            lines.append(
                f"#### Call {call['index']}: `{call['tool']}` — `{call['verdict']}` "
                f"(isError: omp={call['omp_is_error']}, pig={call['pig_is_error']}, details={call.get('details_verdict', '—')}{rc_note})"
            )
            lines.append("Arguments:")
            lines.append("```json")
            lines.append(json.dumps(call["arguments"], indent=2, ensure_ascii=False))
            lines.append("```")
            if call["omp_normalized"] is not None:
                lines.append("omp result (normalized):")
                lines.append("```text")
                lines.append(call["omp_normalized"])
                lines.append("```")
            if call["pig_normalized"] is not None:
                lines.append("oh-my-pig result (normalized):")
                lines.append("```text")
                lines.append(call["pig_normalized"])
                lines.append("```")
            if call.get("diff"):
                lines.append("Diff (`omp` vs `oh-my-pig`):")
                lines.append("```diff")
                lines.append(call["diff"].rstrip("\n"))
                lines.append("```")
            if call.get("details_diff"):
                lines.append("<details><summary>Details diff (`omp` vs `oh-my-pig`)</summary>")
                lines.append("")
                lines.append("```diff")
                lines.append(call["details_diff"].rstrip("\n"))
                lines.append("```")
                lines.append("</details>")
            if call.get("recorded_call_diff"):
                lines.append("<details><summary>Recorded tool_execution_start diff (`omp` vs `oh-my-pig`)</summary>")
                lines.append("")
                lines.append("```diff")
                lines.append(call["recorded_call_diff"].rstrip("\n"))
                lines.append("```")
                lines.append("</details>")
            lines.append("")

    return "\n".join(lines) + "\n"


def load_cases(paths: list[str]) -> tuple[list[str], list[dict[str, Any]]]:
    files: list[str] = []
    cases: list[dict[str, Any]] = []
    for p in paths:
        path = Path(p)
        if path.is_dir():
            for child in sorted(path.glob("*.json")):
                files.append(str(child))
        else:
            files.append(str(path))
    for f in files:
        with open(f, "r", encoding="utf-8") as fh:
            data = json.load(fh)
        if isinstance(data, list):
            cases.extend(data)
        elif isinstance(data, dict) and isinstance(data.get("cases"), list):
            cases.extend(data["cases"])
        elif isinstance(data, dict):
            cases.append(data)
        else:
            raise ValueError(f"Unsupported case file format: {f}")
    return files, cases

def _compute_totals_and_usage(
    *,
    case_reports: list[dict[str, Any]],
    surface_snapshots: list[dict[str, Any]],
    details_allowlist: list[dict[str, Any]],
    text_allowlist: list[dict[str, Any]],
    recorded_allowlist: list[dict[str, Any]],
    surface_allowlist: list[dict[str, Any]],
    network_attempts: list[dict[str, Any]],
    leaked_pids: list[dict[str, Any]],
) -> tuple[dict[str, Any], list[dict[str, Any]], list[dict[str, Any]], list[dict[str, Any]], list[dict[str, Any]]]:
    details_hits = [0] * len(details_allowlist)
    text_hits = [0] * len(text_allowlist)
    recorded_hits = [0] * len(recorded_allowlist)
    surface_hits = [0] * len(surface_allowlist)

    for c in case_reports:
        for x in c.get("calls") or []:
            for idx in x.get("details_allowlist_hits") or []:
                if 0 <= idx < len(details_hits):
                    details_hits[idx] += 1
            for idx in x.get("text_allowlist_hits") or []:
                if 0 <= idx < len(text_hits):
                    text_hits[idx] += 1
            for idx in x.get("recorded_allowlist_hits") or []:
                if 0 <= idx < len(recorded_hits):
                    recorded_hits[idx] += 1
        fu = c.get("followups") or {}
        for idx in fu.get("surface_allowlist_hits") or []:
            if 0 <= idx < len(surface_hits):
                surface_hits[idx] += 1
        cs = c.get("child_surface") or {}
        for idx in cs.get("surface_allowlist_hits") or []:
            if 0 <= idx < len(surface_hits):
                surface_hits[idx] += 1
    for snap in surface_snapshots:
        for idx in snap.get("surface_allowlist_hits") or []:
            if 0 <= idx < len(surface_hits):
                surface_hits[idx] += 1

    details_usage = [
        {
            "index": i,
            "tool": r.get("tool"),
            "path": r.get("path"),
            "match_kind": r.get("match_kind"),
            "side": r.get("side"),
            "class": r.get("class"),
            "ref": r.get("ref"),
            "hits": details_hits[i],
            "real_model_only": bool(r.get("real_model_only")),
            "stale": (details_hits[i] == 0) and not bool(r.get("real_model_only")),
        }
        for i, r in enumerate(details_allowlist)
    ]
    text_usage = [
        {
            "index": i,
            "case": r.get("case"),
            "call_index": r.get("call_index"),
            "tool": r.get("tool"),
            "class": r.get("class"),
            "ref": r.get("ref"),
            "hits": text_hits[i],
            "stale": text_hits[i] == 0,
        }
        for i, r in enumerate(text_allowlist)
    ]
    recorded_usage = [
        {
            "index": i,
            "tool": r.get("tool"),
            "kind": r.get("kind"),
            "class": r.get("class"),
            "ref": r.get("ref"),
            "hits": recorded_hits[i],
            "stale": recorded_hits[i] == 0,
        }
        for i, r in enumerate(recorded_allowlist)
    ]
    surface_usage = [
        {
            "index": i,
            "kind": r.get("kind"),
            "target": r.get("target", "*"),
            "case": r.get("case", "*"),
            "class": r.get("class"),
            "ref": r.get("ref"),
            "hits": surface_hits[i],
            "stale": surface_hits[i] == 0,
        }
        for i, r in enumerate(surface_allowlist)
    ]

    surfaces_identical = sum(1 for s in surface_snapshots if s.get("verdict") == "identical")
    surfaces_listed = sum(1 for s in surface_snapshots if s.get("verdict") == "listed")
    surfaces_differs = sum(1 for s in surface_snapshots if s.get("verdict") == "differs")
    child_identical = 0
    child_listed = 0
    child_differs = 0
    child_missing = 0
    for c in case_reports:
        cs = c.get("child_surface") or {}
        by_id = cs.get("by_id")
        if isinstance(by_id, dict) and by_id:
            for entry_cs in by_id.values():
                v = entry_cs.get("verdict")
                if v == "identical":
                    child_identical += 1
                elif v == "listed":
                    child_listed += 1
                elif v == "missing":
                    child_missing += 1
                elif v == "differs":
                    child_differs += 1
        else:
            v = cs.get("verdict")
            if v == "identical":
                child_identical += 1
            elif v == "listed":
                child_listed += 1
            elif v == "missing":
                child_missing += 1
            elif v == "differs":
                child_differs += 1
    followups_identical = sum(1 for c in case_reports if (c.get("followups") or {}).get("verdict") == "identical")
    followups_listed = sum(1 for c in case_reports if (c.get("followups") or {}).get("verdict") == "listed")
    followups_differs = sum(1 for c in case_reports if (c.get("followups") or {}).get("verdict") == "differs")

    totals = {
        "cases": len(case_reports),
        "cases_failed": sum(1 for c in case_reports if _case_failed(c)),
        "calls": sum(len(c.get("calls") or []) for c in case_reports),
        "calls_identical": sum(1 for c in case_reports for x in c.get("calls") or [] if x["verdict"] == "identical"),
        "calls_details_listed": sum(1 for c in case_reports for x in c.get("calls") or [] if x["verdict"] == "details-listed"),
        "calls_details_only": sum(1 for c in case_reports for x in c.get("calls") or [] if x["verdict"] == "details-only"),
        "calls_differs_listed": sum(1 for c in case_reports for x in c.get("calls") or [] if x["verdict"] == "differs-listed"),
        "calls_timing": sum(1 for c in case_reports for x in c.get("calls") or [] if x["verdict"] == "timing"),
        "calls_differs": sum(1 for c in case_reports for x in c.get("calls") or [] if x["verdict"] == "differs"),
        "calls_missing": sum(1 for c in case_reports for x in c.get("calls") or [] if x["verdict"].startswith("missing")),
        "calls_single": sum(1 for c in case_reports for x in c.get("calls") or [] if x["verdict"] in ("omp_only", "pig_only")),
        "recorded_identical": sum(1 for c in case_reports for x in c.get("calls") or [] if x.get("recorded_call_verdict") == "identical"),
        "recorded_listed": sum(1 for c in case_reports for x in c.get("calls") or [] if x.get("recorded_call_verdict") == "recorded-listed"),
        "recorded_differs": sum(1 for c in case_reports for x in c.get("calls") or [] if x.get("recorded_call_verdict") == "recorded-differs"),
        "surfaces_identical": surfaces_identical,
        "surfaces_listed": surfaces_listed,
        "surfaces_differs": surfaces_differs,
        "child_surfaces_identical": child_identical,
        "child_surfaces_listed": child_listed,
        "child_surfaces_differs": child_differs,
        "child_surfaces_missing": child_missing,
        "followups_identical": followups_identical,
        "followups_listed": followups_listed,
        "followups_differs": followups_differs,
        "details_allowlist_stale": sum(1 for r in details_usage if r["stale"]),
        "text_allowlist_stale": sum(1 for r in text_usage if r["stale"]),
        "recorded_allowlist_stale": sum(1 for r in recorded_usage if r["stale"]),
        "surface_allowlist_stale": sum(1 for r in surface_usage if r["stale"]),
        "network_attempts": len(network_attempts),
        "leaked_pids": sum(len(x.get("pids") or []) for x in leaked_pids),
    }
    return totals, details_usage, text_usage, recorded_usage, surface_usage


def oracle_exit_code(
    totals: dict[str, Any],
    *,
    mode: str,
    allow_diffs: bool = False,
    allow_timing: bool = False,
) -> int:
    """Compute the oracle process exit code from totals.

    Fails non-zero on:
      - any failed case, network attempt, or leaked PID (always, even with --allow-diffs)
      - in `both` mode (unless --allow-diffs): any unexplained call diff, details-only diff,
        recorded-call diff, surface/child-surface/followup diff or missing child surface,
        or `timing` call (unless --allow-timing).
    """
    if totals.get("cases_failed", 0) > 0 or totals.get("network_attempts", 0) > 0 or totals.get("leaked_pids", 0) > 0:
        return 1
    if mode != "both" or allow_diffs:
        return 0
    if (
        totals.get("calls_differs", 0) > 0
        or totals.get("calls_details_only", 0) > 0
        or totals.get("recorded_differs", 0) > 0
        or totals.get("surfaces_differs", 0) > 0
        or totals.get("child_surfaces_differs", 0) > 0
        or totals.get("child_surfaces_missing", 0) > 0
        or totals.get("followups_differs", 0) > 0
    ):
        return 1
    if totals.get("calls_timing", 0) > 0 and not allow_timing:
        return 1
    return 0


def reclassify_report(
    saved_report: dict[str, Any],
    *,
    details_allowlist: list[dict[str, Any]],
    text_allowlist: list[dict[str, Any]],
    recorded_allowlist: list[dict[str, Any]],
    surface_allowlist: list[dict[str, Any]] | None = None,
) -> dict[str, Any]:
    """Re-apply details, text, recorded, and surface allowlists to an existing saved report.json without re-running binaries."""
    s_allow = surface_allowlist if surface_allowlist is not None else []
    out = json.loads(json.dumps(saved_report))
    case_reports = out.get("cases") or []
    for c in case_reports:
        cname = str(c.get("name", "unnamed"))
        for call in c.get("calls") or []:
            if out.get("mode") != "both":
                continue
            tool = str(call.get("tool", ""))
            idx = int(call.get("index", 0))
            o_norm = call.get("omp_normalized")
            p_norm = call.get("pig_normalized")
            o_err = call.get("omp_is_error")
            p_err = call.get("pig_is_error")
            o_det = call.get("omp_details")
            p_det = call.get("pig_details")

            call["recorded_allowlist_hits"] = []
            o_rec = {"args": call.get("omp_recorded_args"), "intent": call.get("omp_recorded_intent")}
            p_rec = {"args": call.get("pig_recorded_args"), "intent": call.get("pig_recorded_intent")}
            o_rec_json = json.dumps(o_rec, sort_keys=True, indent=2, ensure_ascii=False)
            p_rec_json = json.dumps(p_rec, sort_keys=True, indent=2, ensure_ascii=False)
            if o_rec_json == p_rec_json:
                call["recorded_call_verdict"] = "identical"
                call["recorded_call_diff"] = ""
            else:
                rec_listed, rec_hits = classify_recorded_call_diff(
                    tool,
                    call.get("omp_recorded_args"),
                    call.get("omp_recorded_intent"),
                    call.get("pig_recorded_args"),
                    call.get("pig_recorded_intent"),
                    recorded_allowlist,
                )
                call["recorded_allowlist_hits"] = rec_hits
                call["recorded_call_verdict"] = "recorded-listed" if rec_listed else "recorded-differs"
                call["recorded_call_diff"] = unified_diff(o_rec_json, p_rec_json, "omp:recorded_call", "oh-my-pig:recorded_call")

            if call.get("verdict") in ("missing_tool", "missing_both", "missing_omp", "missing_pig"):
                continue
            ev_res = _evaluate_call_verdict(
                case_name=cname,
                call_index=idx,
                tool=tool,
                arguments=call.get("arguments") if isinstance(call.get("arguments"), dict) else None,
                o_norm=o_norm,
                p_norm=p_norm,
                o_raw=call.get("omp_raw"),
                p_raw=call.get("pig_raw"),
                o_wall_ms=call.get("omp_raw_wall_time_ms"),
                p_wall_ms=call.get("pig_raw_wall_time_ms"),
                o_err=o_err,
                p_err=p_err,
                o_det=o_det,
                p_det=p_det,
                details_allowlist=details_allowlist,
                text_allowlist=text_allowlist,
            )
            call.update(ev_res)

        # Reclassify followups if added_messages_normalized is present on both sides
        omp_side = c.get("omp") or {}
        pig_side = c.get("pig") or {}
        if out.get("mode") == "both" and "added_messages_normalized" in omp_side and "added_messages_normalized" in pig_side:
            c["followups"] = classify_followup_messages(
                cname,
                omp_side.get("added_messages_normalized"),
                pig_side.get("added_messages_normalized"),
                c.get("calls") or [],
                s_allow,
            )

        # Reclassify child surface(s)
        cs = c.get("child_surface")
        if out.get("mode") == "both" and isinstance(cs, dict) and cs:
            by_id = cs.get("by_id")
            if isinstance(by_id, dict) and by_id:
                agg_hits: set[int] = set()
                agg_unexp: list[str] = []
                agg_any_diff = False
                agg_any_missing = False
                for cid, entry_cs in by_id.items():
                    if entry_cs.get("verdict") == "missing" or entry_cs.get("missing_side"):
                        agg_any_missing = True
                        agg_unexp.append(f"{cid}: missing on {entry_cs.get('missing_side', 'one side')}")
                        continue
                    c_hits: set[int] = set()
                    c_unexp: list[str] = []
                    c_any_diff = False
                    if entry_cs.get("sys_diff"):
                        c_any_diff = True
                        sib_ids = [other for other in by_id.keys() if other != cid]
                        cs_ok, hits_s, unexp_s = classify_surface_diff(
                            "child_system_prompt",
                            entry_cs["sys_diff"],
                            s_allow,
                            target="child",
                            case_name=cname,
                            self_child_id=cid,
                            sibling_child_ids=sib_ids,
                        )
                        entry_cs["sys_verdict"] = "listed" if cs_ok else "differs"
                        entry_cs["sys_unexplained"] = unexp_s
                        c_hits.update(hits_s)
                        if not cs_ok:
                            c_unexp.append(f"child_sys ({len(unexp_s)} lines)")
                    if entry_cs.get("tools_comparison"):
                        ctc = classify_tools_comparison(entry_cs["tools_comparison"], s_allow, is_child=True, case_name=cname)
                        entry_cs["tools_comparison"] = ctc
                        c_hits.update(ctc.get("surface_allowlist_hits") or [])
                        if ctc.get("verdict") != "identical":
                            c_any_diff = True
                        c_unexp.extend(ctc.get("unexplained") or [])
                    entry_cs["surface_allowlist_hits"] = sorted(c_hits)
                    entry_cs["unexplained"] = c_unexp
                    entry_cs["verdict"] = "identical" if not c_any_diff else ("listed" if not c_unexp else "differs")
                    agg_hits.update(c_hits)
                    if c_any_diff:
                        agg_any_diff = True
                    for u in c_unexp:
                        agg_unexp.append(f"{cid}: {u}")
                cs["surface_allowlist_hits"] = sorted(agg_hits)
                cs["unexplained"] = agg_unexp
                if agg_any_missing:
                    cs["verdict"] = "missing"
                elif not agg_any_diff:
                    cs["verdict"] = "identical"
                elif not agg_unexp:
                    cs["verdict"] = "listed"
                else:
                    cs["verdict"] = "differs"
            else:
                cs_hits = set()
                cs_unexp = []
                cs_any_diff = False
                if cs.get("sys_diff"):
                    cs_any_diff = True
                    cs_ok, hits_s, unexp_s = classify_surface_diff(
                        "child_system_prompt", cs["sys_diff"], s_allow, target="child", case_name=cname
                    )
                    cs["sys_verdict"] = "listed" if cs_ok else "differs"
                    cs["sys_unexplained"] = unexp_s
                    cs_hits.update(hits_s)
                    if not cs_ok:
                        cs_unexp.append(f"child_sys ({len(unexp_s)} lines)")
                if cs.get("tools_comparison"):
                    ctc = classify_tools_comparison(cs["tools_comparison"], s_allow, is_child=True, case_name=cname)
                    cs["tools_comparison"] = ctc
                    cs_hits.update(ctc.get("surface_allowlist_hits") or [])
                    if ctc.get("verdict") != "identical":
                        cs_any_diff = True
                    cs_unexp.extend(ctc.get("unexplained") or [])
                cs["surface_allowlist_hits"] = sorted(cs_hits)
                cs["unexplained"] = cs_unexp
                cs["verdict"] = "identical" if not cs_any_diff else ("listed" if not cs_unexp else "differs")
    new_snaps: list[dict[str, Any]] = []
    for snap in out.get("surface_snapshots") or []:
        if out.get("mode") == "both":
            new_snaps.append(classify_surface_snapshot(snap, s_allow))
        else:
            new_snaps.append(snap)
    out["surface_snapshots"] = new_snaps

    totals, details_usage, text_usage, recorded_usage, surface_usage = _compute_totals_and_usage(
        case_reports=case_reports,
        surface_snapshots=new_snaps,
        details_allowlist=details_allowlist,
        text_allowlist=text_allowlist,
        recorded_allowlist=recorded_allowlist,
        surface_allowlist=s_allow,
        network_attempts=out.get("network_attempts") or [],
        leaked_pids=out.get("leaked_pids") or [],
    )
    out["details_allowlist_usage"] = details_usage
    out["text_allowlist_usage"] = text_usage
    out["recorded_allowlist_usage"] = recorded_usage
    out["surface_allowlist_usage"] = surface_usage
    out["totals"] = totals
    return out


def main() -> int:
    parser = argparse.ArgumentParser(description="Per-tool oracle comparing omp and oh-my-pig")
    parser.add_argument("case_files", nargs="*", help="JSON case file(s) or directory of case files")
    parser.add_argument("--omp-bin", default=shutil.which("omp") or "", help="Path to omp binary")
    parser.add_argument("--pig-bin", default=os.environ.get("OH_MY_PIG_BIN", ""), help="Path to oh-my-pig binary")
    parser.add_argument("--mode", choices=("both", "omp-only", "pig-only"), default="both")
    parser.add_argument("--out-dir", default="/tmp/omp-proof2/oracle", help="Output directory for report.md, report.json")
    parser.add_argument("--timeout", type=int, default=45, help="Per-case subprocess timeout in seconds")
    parser.add_argument(
        "--keep-failing",
        action="store_true",
        default=os.environ.get("ORACLE_KEEP", "") == "1",
        help="Keep per-case scratch for failing cases (also enabled via ORACLE_KEEP=1)",
    )
    parser.add_argument(
        "--allow-diffs",
        action="store_true",
        default=os.environ.get("ORACLE_ALLOW_DIFFS", "") == "1",
        help="In both-mode, exit 0 even when tool outputs or surfaces differ (still exits non-zero on crashes/network/leaked PIDs)",
    )
    parser.add_argument(
        "--allow-timing",
        action="store_true",
        default=os.environ.get("ORACLE_ALLOW_TIMING", "") == "1",
        help="In both-mode, allow `timing` wall-time threshold crossings without exiting non-zero",
    )
    parser.add_argument(
        "--details-allowlist",
        default=str(DEFAULT_DETAILS_ALLOWLIST_PATH),
        help="JSON allowlist of documented details differences",
    )
    parser.add_argument(
        "--text-allowlist",
        default=str(DEFAULT_TEXT_ALLOWLIST_PATH),
        help="JSON allowlist of documented text differences",
    )
    parser.add_argument(
        "--recorded-allowlist",
        default=str(DEFAULT_RECORDED_ALLOWLIST_PATH),
        help="JSON allowlist of documented tool_execution_start recorded-call differences",
    )
    parser.add_argument(
        "--surface-allowlist",
        default=str(DEFAULT_SURFACE_ALLOWLIST_PATH),
        help="JSON allowlist of documented system prompt, tool description/schema, child surface, and follow-up differences",
    )
    parser.add_argument(
        "--reclassify",
        default="",
        help="Reclassify an existing saved report.json against the current allowlists (no binaries executed)",
    )
    args = parser.parse_args()

    details_allowlist = load_details_allowlist(Path(args.details_allowlist))
    text_allowlist = load_text_allowlist(Path(args.text_allowlist))
    recorded_allowlist = load_recorded_allowlist(Path(args.recorded_allowlist))
    surface_allowlist = load_surface_allowlist(Path(args.surface_allowlist))

    if args.reclassify:
        saved = json.loads(Path(args.reclassify).read_text(encoding="utf-8"))
        reclassified = reclassify_report(
            saved,
            details_allowlist=details_allowlist,
            text_allowlist=text_allowlist,
            recorded_allowlist=recorded_allowlist,
            surface_allowlist=surface_allowlist,
        )
        t = reclassified["totals"]
        print(
            f"Oracle reclassified: {t['cases']} case(s) ({t['cases_failed']} failed), "
            f"{t['calls']} call(s) (identical={t['calls_identical']}, details-listed={t['calls_details_listed']}, "
            f"details-only={t['calls_details_only']}, differs-listed={t['calls_differs_listed']}, "
            f"timing={t['calls_timing']}, differs={t['calls_differs']}, "
            f"missing={t['calls_missing']}, single={t['calls_single']}), "
            f"recorded=(identical={t['recorded_identical']}, recorded-listed={t['recorded_listed']}, "
            f"recorded-differs={t['recorded_differs']}), "
            f"surfaces=(listed={t['surfaces_listed']}, differs={t['surfaces_differs']}, "
            f"child_listed={t['child_surfaces_listed']}, child_differs={t['child_surfaces_differs']}, "
            f"child_missing={t.get('child_surfaces_missing', 0)}, "
            f"followups_listed={t['followups_listed']}, followups_differs={t['followups_differs']}), "
            f"allowlist_stale=(details={t['details_allowlist_stale']}, text={t['text_allowlist_stale']}, "
            f"recorded={t['recorded_allowlist_stale']}, surface={t['surface_allowlist_stale']})"
        )
        if args.out_dir and args.out_dir != "/tmp/omp-proof2/oracle":
            out_dir = Path(os.path.realpath(args.out_dir))
            out_dir.mkdir(parents=True, exist_ok=True)
            (out_dir / "report.json").write_text(json.dumps(reclassified, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
            (out_dir / "report.md").write_text(render_markdown(reclassified), encoding="utf-8")
        return oracle_exit_code(
            t,
            mode=str(reclassified.get("mode", "both")),
            allow_diffs=args.allow_diffs,
            allow_timing=args.allow_timing,
        )

    if not args.case_files:
        sys.stderr.write("error: provide at least one case file or --reclassify <report.json>\n")
        return 2

    run_omp = args.mode in ("both", "omp-only")
    run_pig = args.mode in ("both", "pig-only")
    if run_omp and (not args.omp_bin or not os.path.isfile(args.omp_bin)):
        sys.stderr.write(f"error: omp binary not found: {args.omp_bin!r}\n")
        return 2
    if run_pig and (not args.pig_bin or not os.path.isfile(args.pig_bin)):
        sys.stderr.write(f"error: oh-my-pig binary not found: {args.pig_bin!r} (set OH_MY_PIG_BIN or pass --mode omp-only)\n")
        return 2

    files, cases = load_cases(args.case_files)
    out_dir = Path(os.path.realpath(args.out_dir))
    check_free_disk_space(out_dir)
    out_dir.mkdir(parents=True, exist_ok=True)

    scratch = out_dir / "scratch"
    shared_natives = out_dir / ".natives-cache"
    shutil.rmtree(scratch, ignore_errors=True)
    shutil.rmtree(shared_natives, ignore_errors=True)
    scratch.mkdir(parents=True, exist_ok=True)
    shared_natives.mkdir(parents=True, exist_ok=True)

    run_tag = f"oracle-{os.getpid()}-{uuid.uuid4().hex[:12]}"
    proxy = RefusingProxyServer()
    proxy.start()

    # Install SIGTERM/SIGINT/SIGHUP handlers so finally blocks run and tagged
    # children are never orphaned.
    def _handle_signal(signum: int, _frame: Any) -> None:
        reap_tagged_processes(run_tag)
        raise SystemExit(128 + signum)

    signal.signal(signal.SIGTERM, _handle_signal)
    signal.signal(signal.SIGINT, _handle_signal)
    signal.signal(signal.SIGHUP, _handle_signal)

    case_reports: list[dict[str, Any]] = []
    # Track distinct surfaces (e.g. default vs enable_skills / custom config)
    seen_surface_keys: set[str] = set()
    surface_snapshots: list[dict[str, Any]] = []
    first_omp_sys: str | None = None
    first_pig_sys: str | None = None
    kept_failing_dirs: list[Path] = []
    all_net_attempts: list[dict[str, Any]] = []
    all_leaked_pids: list[dict[str, Any]] = []

    try:
        for idx, case in enumerate(cases):
            cname = case.get("name") or f"case-{idx}"
            safe_name = re.sub(r"[^A-Za-z0-9._-]+", "_", cname)
            case_dir = scratch / f"{idx:03d}_{safe_name}"
            case_timeout = int(case.get("timeout") or args.timeout)

            omp_res = None
            pig_res = None
            entry: dict[str, Any] | None = None
            try:
                if run_omp:
                    case_tag = f"{run_tag}-c{idx}-omp"
                    omp_res = run_side(
                        "omp",
                        args.omp_bin,
                        case,
                        case_dir,
                        case_timeout,
                        shared_natives_dir=shared_natives,
                        proxy=proxy,
                        run_tag=case_tag,
                    )
                    if first_omp_sys is None and omp_res["system_prompt_normalized"] is not None:
                        first_omp_sys = omp_res["system_prompt_normalized"]
                    for a in omp_res.get("network_attempts") or []:
                        all_net_attempts.append({"case": cname, "side": "omp", **a})
                    if omp_res.get("leaked_pids"):
                        all_leaked_pids.append({"case": cname, "side": "omp", "pids": omp_res["leaked_pids"]})

                if run_pig:
                    case_tag = f"{run_tag}-c{idx}-pig"
                    pig_res = run_side(
                        "pig",
                        args.pig_bin,
                        case,
                        case_dir,
                        case_timeout,
                        shared_natives_dir=None,
                        proxy=proxy,
                        run_tag=case_tag,
                    )
                    if first_pig_sys is None and pig_res["system_prompt_normalized"] is not None:
                        first_pig_sys = pig_res["system_prompt_normalized"]
                    for a in pig_res.get("network_attempts") or []:
                        all_net_attempts.append({"case": cname, "side": "pig", **a})
                    if pig_res.get("leaked_pids"):
                        all_leaked_pids.append({"case": cname, "side": "pig", "pids": pig_res["leaked_pids"]})

                # Record surface snapshot whenever tool names or system prompt changes
                o_tools = omp_res["tools"] if omp_res else []
                p_tools = pig_res["tools"] if pig_res else []
                o_sys = omp_res["system_prompt_normalized"] if omp_res else None
                p_sys = pig_res["system_prompt_normalized"] if pig_res else None
                surf_sig = json.dumps(
                    {
                        "ot": [t["name"] for t in o_tools],
                        "pt": [t["name"] for t in p_tools],
                        "os": o_sys,
                        "ps": p_sys,
                    },
                    sort_keys=True,
                )
                if (o_tools or p_tools or o_sys or p_sys) and surf_sig not in seen_surface_keys:
                    seen_surface_keys.add(surf_sig)
                    label = "skills" if case.get("enable_skills") else ("vision" if case.get("vision") else f"surface-{len(surface_snapshots)}")
                    sp_info: dict[str, Any] = {}
                    if o_sys is not None:
                        sp_info["omp_len"] = len(o_sys)
                    if p_sys is not None:
                        sp_info["pig_len"] = len(p_sys)
                    if o_sys is not None and p_sys is not None:
                        sp_info["verdict"] = "identical" if o_sys == p_sys else "differs"
                        sp_info["diff"] = "" if o_sys == p_sys else unified_diff(o_sys, p_sys, "omp:system_prompt", "oh-my-pig:system_prompt")
                    snap: dict[str, Any] = {
                        "label": label,
                        "case": cname,
                        "omp_tool_names": [t["name"] for t in o_tools],
                        "pig_tool_names": [t["name"] for t in p_tools],
                        "omp_tools": o_tools,
                        "pig_tools": p_tools,
                        "system_prompt": sp_info,
                    }
                    if o_tools and p_tools:
                        snap["tools_comparison"] = compare_tools(o_tools, p_tools)
                    if args.mode == "both":
                        snap = classify_surface_snapshot(snap, surface_allowlist)
                    surface_snapshots.append(snap)

                entry = compare_case(
                    case,
                    omp_res,
                    pig_res,
                    details_allowlist=details_allowlist,
                    text_allowlist=text_allowlist,
                    recorded_allowlist=recorded_allowlist,
                    surface_allowlist=surface_allowlist,
                )
                case_reports.append(entry)
            finally:
                keep = args.keep_failing and (entry is None or _case_failed(entry))
                if keep:
                    kept_failing_dirs.append(case_dir)
                else:
                    shutil.rmtree(case_dir, ignore_errors=True)
    finally:
        proxy.stop()
        # Final sweep across the whole run tag prefix
        still_alive = reap_tagged_processes(run_tag)
        if still_alive:
            all_leaked_pids.append({"case": "<run>", "side": "any", "pids": still_alive})
        shutil.rmtree(shared_natives, ignore_errors=True)
        if not kept_failing_dirs:
            shutil.rmtree(scratch, ignore_errors=True)

    totals, details_allowlist_usage, text_allowlist_usage, recorded_allowlist_usage, surface_allowlist_usage = _compute_totals_and_usage(
        case_reports=case_reports,
        surface_snapshots=surface_snapshots,
        details_allowlist=details_allowlist,
        text_allowlist=text_allowlist,
        recorded_allowlist=recorded_allowlist,
        surface_allowlist=surface_allowlist,
        network_attempts=all_net_attempts,
        leaked_pids=all_leaked_pids,
    )
    report = {
        "mode": args.mode,
        "sandbox": "macos-sandbox-exec-loopback-only" if (sandbox_available() and os.environ.get("ORACLE_NO_SANDBOX") != "1") else "refusing-proxy-only",
        "input_files": files,
        "normalizations": NORMALIZATIONS,
        "totals": totals,
        "details_allowlist_usage": details_allowlist_usage,
        "text_allowlist_usage": text_allowlist_usage,
        "recorded_allowlist_usage": recorded_allowlist_usage,
        "surface_allowlist_usage": surface_allowlist_usage,
        "network_attempts": all_net_attempts,
        "leaked_pids": all_leaked_pids,
        "surface_snapshots": surface_snapshots,
        "cases": case_reports,
    }

    json_out = out_dir / "report.json"
    md_out = out_dir / "report.md"
    json_out.write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    md_out.write_text(render_markdown(report), encoding="utf-8")

    if first_omp_sys is not None:
        (out_dir / "omp-system-prompt.txt").write_text(first_omp_sys, encoding="utf-8")
    if first_pig_sys is not None:
        (out_dir / "pig-system-prompt.txt").write_text(first_pig_sys, encoding="utf-8")

    final_bytes = dir_size_bytes(out_dir)
    print(
        f"Oracle complete: {totals['cases']} case(s) ({totals['cases_failed']} failed), "
        f"{totals['calls']} call(s) (identical={totals['calls_identical']}, details-listed={totals['calls_details_listed']}, "
        f"details-only={totals['calls_details_only']}, differs-listed={totals['calls_differs_listed']}, "
        f"timing={totals['calls_timing']}, differs={totals['calls_differs']}, "
        f"missing={totals['calls_missing']}, single={totals['calls_single']}), "
        f"recorded=(identical={totals['recorded_identical']}, recorded-listed={totals['recorded_listed']}, "
        f"recorded-differs={totals['recorded_differs']}), "
        f"surfaces=(listed={totals['surfaces_listed']}, differs={totals['surfaces_differs']}, "
        f"child_listed={totals['child_surfaces_listed']}, child_differs={totals['child_surfaces_differs']}, "
        f"child_missing={totals.get('child_surfaces_missing', 0)}, "
        f"followups_listed={totals['followups_listed']}, followups_differs={totals['followups_differs']}), "
        f"allowlist_stale=(details={totals['details_allowlist_stale']}, text={totals['text_allowlist_stale']}, "
        f"recorded={totals['recorded_allowlist_stale']}, surface={totals['surface_allowlist_stale']}), "
        f"network_attempts={totals['network_attempts']}, leaked_pids={totals['leaked_pids']}, "
        f"output_size={format_bytes(final_bytes)}"
    )
    print(f"Reports: {md_out} and {json_out}")

    # Report per-failed-case diagnostics loudly on stderr
    failed_cases = [c for c in case_reports if _case_failed(c)]
    for fc in failed_cases:
        sys.stderr.write(f"FAILED case `{fc['name']}`:\n")
        for sk in ("omp", "pig"):
            s = fc.get(sk)
            if not s:
                continue
            mt = s.get("missing_tools") or []
            mt_s = f" missing_tools={mt}" if mt else ""
            sys.stderr.write(
                f"  {sk}: rc={s['rc']} setup_rc={s['setup_rc']} timed_out={s['timed_out']}{mt_s} "
                f"net_attempts={len(s.get('network_attempts') or [])} leaked_pids={s.get('leaked_pids') or []}\n"
            )
            tail = _stderr_tail(s.get("stderr", ""), max_lines=6)
            if tail:
                for tl in tail.splitlines():
                    sys.stderr.write(f"    | {tl}\n")

    if all_net_attempts:
        sys.stderr.write(f"ERROR: {len(all_net_attempts)} non-loopback network attempt(s) recorded by refusing proxy!\n")
        for a in all_net_attempts[:20]:
            sys.stderr.write(f"  - [{a['case']}/{a['side']}] {a['method']} {a['target']} (Host: {a['host']})\n")
    if all_leaked_pids:
        sys.stderr.write(f"ERROR: processes left behind after sweep: {all_leaked_pids}\n")

    return oracle_exit_code(
        totals,
        mode=args.mode,
        allow_diffs=args.allow_diffs,
        allow_timing=args.allow_timing,
    )

if __name__ == "__main__":
    raise SystemExit(main())
