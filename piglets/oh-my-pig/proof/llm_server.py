#!/usr/bin/env python3
"""Scripted OpenAI-compatible chat-completions server for the oh-my-pig proof harness.

Serves deterministic SSE turns on a loopback port so both omp and oh-my-pig
receive identical assistant replies (plain Markdown, thinking + text, read,
bash, edit, and a failing tool call).

Usage:
  llm_server.py --script turns.json [--port 0] [--url-file PATH] [--log PATH]
"""

from __future__ import annotations

import argparse
import http.server
import json
import os
import re
import signal
import sys
import threading
import time
from typing import Any


def _extract_tool_schemas(body: dict[str, Any]) -> dict[str, dict[str, Any]]:
    schemas: dict[str, dict[str, Any]] = {}
    for tool in body.get("tools") or []:
        fn = tool.get("function") if isinstance(tool, dict) else None
        if isinstance(fn, dict) and isinstance(fn.get("name"), str):
            schemas[fn["name"]] = fn.get("parameters") or {}
    return schemas


def _extract_hashline_tag(messages: list[dict[str, Any]], filename: str) -> str | None:
    """Find the latest `[filename#TAG]` snapshot tag emitted by omp's read tool."""
    pat = re.compile(r"\[" + re.escape(filename) + r"#([0-9A-Fa-f]{4})\]")
    found: str | None = None
    for msg in messages:
        if msg.get("role") != "tool":
            continue
        content = msg.get("content")
        text = ""
        if isinstance(content, str):
            text = content
        elif isinstance(content, list):
            parts = []
            for item in content:
                if isinstance(item, dict) and isinstance(item.get("text"), str):
                    parts.append(item["text"])
            text = "\n".join(parts)
        for m in pat.finditer(text):
            found = m.group(1)
    return found


def _adapt_edit_args(
    args: dict[str, Any],
    edit_schema: dict[str, Any],
    messages: list[dict[str, Any]],
) -> dict[str, Any]:
    """Adapt a canonical edit spec to the caller's advertised edit tool schema.

    Canonical spec in turns.json:
      {"path": "notes.txt", "old_text": "hello world", "new_text": "hello omp", "line": 1}

    Supported target schemas:
      - replace:  {"path", "old_string", "new_string"}        (omp with PI_EDIT_VARIANT=replace)
      - edits[]:  {"path", "edits": [{"oldText", "newText"}]} (PiG built-in edit)
      - hashline: {"input": "[path#TAG]\\nPUT L.=L:\\n+new"}   (omp default hashline edit)
      - patch:    {"path", "edits": [{"op": "update", "diff": ...}]}
    """
    props = edit_schema.get("properties") if isinstance(edit_schema, dict) else None
    if not isinstance(props, dict):
        return args

    # If the script already supplied exact keys matching the schema, pass through.
    if "old_text" not in args and "new_text" not in args:
        return args

    path = str(args["path"])
    old_text = str(args["old_text"])
    new_text = str(args["new_text"])
    line_no = int(args.get("line", 1))

    if "old_string" in props and "new_string" in props:
        return {"path": path, "old_string": old_text, "new_string": new_text}

    if "edits" in props:
        items = props["edits"].get("items", {}) if isinstance(props["edits"], dict) else {}
        item_props = items.get("properties", {}) if isinstance(items, dict) else {}
        if "oldText" in item_props and "newText" in item_props:
            return {"path": path, "edits": [{"oldText": old_text, "newText": new_text}]}
        if "diff" in item_props:
            diff = f"@@\n-{old_text}\n+{new_text}\n"
            return {"path": path, "edits": [{"op": "update", "diff": diff}]}

    if "input" in props:
        tag = _extract_hashline_tag(messages, path) or "0000"
        patch_input = f"[{path}#{tag}]\nPUT {line_no}.={line_no}:\n+{new_text}\n"
        return {"input": patch_input}

    return args


def _adapt_tool_call(
    call: dict[str, Any],
    schemas: dict[str, dict[str, Any]],
    messages: list[dict[str, Any]],
) -> tuple[str, dict[str, Any]]:
    name = str(call["name"])
    args = dict(call.get("arguments") or {})
    if name == "edit" and name in schemas:
        args = _adapt_edit_args(args, schemas[name], messages)
    return name, args


class ScriptedLLMServer:
    def __init__(self, turns: list[dict[str, Any]], log_path: str | None) -> None:
        self.turns = turns
        self.log_path = log_path
        self.lock = threading.Lock()
        self.next_index = 0

    def reset(self) -> None:
        with self.lock:
            self.next_index = 0

    def log_event(self, event: dict[str, Any]) -> None:
        if not self.log_path:
            return
        with self.lock:
            with open(self.log_path, "a", encoding="utf-8") as fh:
                fh.write(json.dumps(event, ensure_ascii=False) + "\n")

    def pop_turn(self) -> tuple[int, dict[str, Any] | None]:
        with self.lock:
            idx = self.next_index
            if idx >= len(self.turns):
                return idx, None
            self.next_index += 1
            return idx, self.turns[idx]


def make_handler(state: ScriptedLLMServer) -> type[http.server.BaseHTTPRequestHandler]:
    class Handler(http.server.BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, fmt: str, *args: Any) -> None:
            pass

        def _send_json(self, status: int, payload: Any) -> None:
            raw = json.dumps(payload).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

        def do_GET(self) -> None:
            if self.path.rstrip("/") in ("/v1/models", "/models"):
                self._send_json(
                    200,
                    {
                        "object": "list",
                        "data": [
                            {
                                "id": "proof-1",
                                "object": "model",
                                "created": 1,
                                "owned_by": "proof",
                            }
                        ],
                    },
                )
                return
            if self.path == "/health":
                self._send_json(200, {"ok": True, "next": state.next_index})
                return
            self._send_json(404, {"error": "not found"})

        def do_POST(self) -> None:
            if self.path == "/reset":
                state.reset()
                self._send_json(200, {"ok": True})
                return

            if not self.path.endswith("/chat/completions"):
                self._send_json(404, {"error": f"unsupported path {self.path}"})
                return

            length = int(self.headers.get("Content-Length", "0"))
            raw_body = self.rfile.read(length) if length > 0 else b"{}"
            try:
                body = json.loads(raw_body.decode("utf-8"))
            except json.JSONDecodeError as exc:
                self._send_json(400, {"error": str(exc)})
                return

            messages = body.get("messages") or []
            schemas = _extract_tool_schemas(body)

            last_msg = messages[-1] if messages else {}
            last_content = last_msg.get("content", "")
            if isinstance(last_content, list):
                last_content = " ".join(
                    p.get("text", "") for p in last_content if isinstance(p, dict)
                )
            last_text = str(last_content)

            # Ignore auxiliary non-conversation requests (e.g. omp's
            # `<composer-prediction>` ghost-text query) so they do not advance
            # the scripted turn cursor.
            if "<composer-prediction>" in last_text:
                state.log_event(
                    {
                        "turn_index": None,
                        "auxiliary": "composer-prediction",
                        "path": self.path,
                        "model": body.get("model"),
                    }
                )
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.send_header("Cache-Control", "no-cache")
                self.send_header("Connection", "close")
                self.end_headers()
                chunk = {
                    "id": "aux-pred",
                    "object": "chat.completion.chunk",
                    "created": 1,
                    "model": str(body.get("model") or "proof-1"),
                    "choices": [{"index": 0, "delta": {"role": "assistant", "content": ""}, "finish_reason": "stop"}],
                }
                self.wfile.write(f"data: {json.dumps(chunk)}\n\ndata: [DONE]\n\n".encode("utf-8"))
                self.wfile.flush()
                return

            idx, turn = state.pop_turn()
            state.log_event(
                {
                    "turn_index": idx,
                    "path": self.path,
                    "model": body.get("model"),
                    "message_count": len(messages),
                    "last_role": last_msg.get("role"),
                    "last_preview": last_text[:120],
                    "tool_names": sorted(schemas.keys()),
                    "has_turn": turn is not None,
                }
            )

            if turn is None:
                self._send_json(
                    500,
                    {"error": {"message": f"script exhausted at request {idx + 1}"}},
                )
                return
            delay_ms = int(turn.get("delay_ms") or 0)
            model_id = str(body.get("model") or "proof-1")
            chunk_id = f"proof-{idx}"

            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache")
            self.send_header("Connection", "close")
            self.end_headers()

            def emit(delta: dict[str, Any], finish: str | None) -> None:
                chunk = {
                    "id": chunk_id,
                    "object": "chat.completion.chunk",
                    "created": 1,
                    "model": model_id,
                    "choices": [{"index": 0, "delta": delta, "finish_reason": finish}],
                }
                self.wfile.write(f"data: {json.dumps(chunk)}\n\n".encode("utf-8"))
                self.wfile.flush()

            emit({"role": "assistant", "content": ""}, None)

            if delay_ms > 0:
                time.sleep(delay_ms / 1000.0)

            thinking = turn.get("thinking")
            if isinstance(thinking, str) and thinking:
                # Stream in two pieces so live thinking renders before finishing.
                mid = max(1, len(thinking) // 2)
                emit({"reasoning_content": thinking[:mid]}, None)
                if len(thinking) > mid:
                    emit({"reasoning_content": thinking[mid:]}, None)

            text = turn.get("text")
            if isinstance(text, str) and text:
                emit({"content": text}, None)

            tool_calls = turn.get("tool_calls") or []
            finish_reason = "stop"
            for j, call in enumerate(tool_calls):
                name, args = _adapt_tool_call(call, schemas, messages)
                args_str = json.dumps(args, ensure_ascii=False)
                emit(
                    {
                        "tool_calls": [
                            {
                                "index": j,
                                "id": f"call_{idx}_{j}",
                                "type": "function",
                                "function": {"name": name, "arguments": args_str},
                            }
                        ]
                    },
                    None,
                )
                finish_reason = "tool_calls"

            emit({}, finish_reason)

            usage_chunk = {
                "id": chunk_id,
                "object": "chat.completion.chunk",
                "created": 1,
                "model": model_id,
                "choices": [],
                "usage": {
                    "prompt_tokens": 120 + idx * 40,
                    "completion_tokens": 35,
                    "total_tokens": 155 + idx * 40,
                },
            }
            self.wfile.write(f"data: {json.dumps(usage_chunk)}\n\n".encode("utf-8"))
            self.wfile.write(b"data: [DONE]\n\n")
            self.wfile.flush()

    return Handler


def main() -> int:
    parser = argparse.ArgumentParser(description="Scripted OpenAI-compatible LLM server")
    parser.add_argument("--script", required=True, help="Path to turns.json")
    parser.add_argument("--port", type=int, default=0, help="Port on 127.0.0.1 (default: ephemeral)")
    parser.add_argument("--url-file", help="Write base URL (http://127.0.0.1:<port>/v1) to this file")
    parser.add_argument("--log", help="Append request log JSONL to this path")
    args = parser.parse_args()

    with open(args.script, "r", encoding="utf-8") as fh:
        turns = json.load(fh)
    if not isinstance(turns, list):
        sys.stderr.write("script must be a JSON array of turns\n")
        return 2

    state = ScriptedLLMServer(turns, args.log)
    server = http.server.ThreadingHTTPServer(("127.0.0.1", args.port), make_handler(state))
    port = server.server_address[1]
    base_url = f"http://127.0.0.1:{port}/v1"

    if args.url_file:
        tmp = f"{args.url_file}.tmp.{os.getpid()}"
        with open(tmp, "w", encoding="utf-8") as fh:
            fh.write(base_url + "\n")
        os.replace(tmp, args.url_file)

    sys.stdout.write(base_url + "\n")
    sys.stdout.flush()

    def _stop(_signum: int, _frame: Any) -> None:
        threading.Thread(target=server.shutdown, daemon=True).start()

    signal.signal(signal.SIGTERM, _stop)
    signal.signal(signal.SIGINT, _stop)

    try:
        server.serve_forever()
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
