#!/usr/bin/env python3
"""A scripted OpenAI-compatible chat completions server for the tmux scenarios.

usage: mockllm.py PORT SCRIPT.json

SCRIPT.json is a list of replies, one per model request in order (the last one repeats). A reply is a string
(streamed in chunks of CHUNK characters, with DELAY seconds between them) or {"text": ..., "chunk": n, "delay": s}.
The request bodies are appended to PORT.requests.jsonl so a scenario can see what the model was sent.
"""
import json, sys, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

port = int(sys.argv[1])
script = json.load(open(sys.argv[2]))
requests_log = open(f"{sys.argv[2]}.{port}.requests.jsonl", "a", buffering=1)
state = {"n": 0}


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    def do_GET(self):
        body = json.dumps({"object": "list", "data": [{"id": "mock", "object": "model"}]}).encode()
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        length = int(self.headers.get("content-length") or 0)
        request = json.loads(self.rfile.read(length) or b"{}")
        requests_log.write(json.dumps(request) + "\n")
        reply = script[min(state["n"], len(script) - 1)]
        state["n"] += 1
        if isinstance(reply, str):
            reply = {"text": reply}
        text, chunk, delay = reply["text"], reply.get("chunk", 12), reply.get("delay", 0.0)
        self.send_response(200)
        self.send_header("content-type", "text/event-stream")
        self.send_header("cache-control", "no-cache")
        self.send_header("transfer-encoding", "chunked")
        self.end_headers()

        def send(obj):
            data = ("data: " + (obj if isinstance(obj, str) else json.dumps(obj)) + "\n\n").encode()
            self.wfile.write(f"{len(data):x}\r\n".encode() + data + b"\r\n")
            self.wfile.flush()

        base = {"id": "mock-1", "object": "chat.completion.chunk", "created": 0, "model": "mock"}
        send({**base, "choices": [{"index": 0, "delta": {"role": "assistant", "content": ""}, "finish_reason": None}]})
        for i in range(0, len(text), chunk):
            send({**base, "choices": [{"index": 0, "delta": {"content": text[i:i + chunk]}, "finish_reason": None}]})
            if delay:
                time.sleep(delay)
        send({**base, "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}]})
        send({**base, "choices": [], "usage": {"prompt_tokens": 10, "completion_tokens": max(1, len(text) // 4), "total_tokens": 10 + max(1, len(text) // 4)}})
        send("[DONE]")
        self.wfile.write(b"0\r\n\r\n")
        self.wfile.flush()


ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
