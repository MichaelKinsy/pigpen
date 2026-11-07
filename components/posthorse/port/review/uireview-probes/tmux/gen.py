#!/usr/bin/env python3
"""uireview: adversarial tmux scenarios for the card probe of port/tmux (fixtures + scenarios written next to this file).

usage: gen.py   (writes fixtures.json and scenarios/*.json)
"""
import json, os

here = os.path.dirname(os.path.abspath(__file__))
out = os.path.join(here, "scenarios")
os.makedirs(out, exist_ok=True)
for old in os.listdir(out):
    os.remove(os.path.join(out, old))


def u16(s):
    return len(s.encode("utf-16-le")) // 2


def note_read(body, total=None):
    header = "File: /work/.pi/notes/x.md\n"
    total = u16(body) if total is None else total
    return {"text": header + body, "details": {"kind": "note-read", "headerLength": u16(header), "offset": 0, "end": u16(body), "total": total}}


texts = {
    "cjk": "界".repeat if False else "界" * 30 + " 漢字かなカナ 한국어 텍스트입니다 全角スペース\u3000あり " + "中文字符" * 20 + "\n👩🏽‍💻 🇺🇦 👨‍👩‍👧‍👦 ❤️ ✋🏿 1️⃣ " * 6,
    "rtl": "مرحبا بالعالم هذا نص عربي طويل للاختبار " * 6 + "\nשלום עולם זהו טקסט בעברית " * 4 + "\nabc \u202eevil\u202c def \u200fx\u200e \u2066iso\u2069 \u061cend",
    "ansi": "\x1b[31mred\x1b[0m plain \x1b[1;4mbold\x1b[22m \x1b]8;;http://e.com\x07link\x1b]8;;\x07 \x1b[2Ahello m world \x1b[31 unterminated "
            "\x1b_apc\x1b\\ \x1bc reset \x9b31mC1 \x85nel \ufff9ann\ufffa\ufffb \x7fdel \x00nul \x07bel \x08bs\n\x1b[5;5Hjump \x1b[2Jclear\ttab end",
    "zalgo": "a" + "\u0301" * 3000 + " tail words here and more words to wrap",
    "thai": "ภาษาไทยเป็นภาษาที่ไม่มีการเว้นวรรคระหว่างคำ" * 200,
    "big": "\n".join(f"line {i} " + "word " * (i % 7) for i in range(10000)),
}
tools = {f"note-{k}": note_read(v) for k, v in texts.items()}
tools["ctx-1250"] = {"text": "report", "details": {"kind": "context", "usage": {"tokens": 200000, "contextWindow": 16000, "percent": 1250.4}, "rollover": "unsupported"}}
tools["ctx-half"] = {"text": "report", "details": {"kind": "context", "usage": {"tokens": 1, "contextWindow": 2, "percent": 0.49999999999999994}, "rollover": "disabled"}}
h1 = "2026-09-06T17:43:46Z [window w1] [e1] [user] "
h2 = "2026-09-06T17:44:46Z [window w1] [e2] [assistant] "
for k in ("cjk", "rtl", "ansi"):
    body = h1 + texts[k] + "\n" + h2 + "tail"
    tools[f"hs-{k}"] = {"text": body, "details": {"kind": "history-search", "entries": [{"headerLength": u16(h1), "length": u16(h1 + texts[k])},
                                                                                      {"headerLength": u16(h2), "length": u16(h2 + "tail")}],
                                                   "footerLength": 0, "more": False, "skipped": 0}}
messages = {f"m-{k}": {"customType": "posthorse-reminder", "content": v} for k, v in texts.items()}
messages.update({f"w-{k}": {"customType": "context-window", "content": "Context window w2 starts here.\n\nHandoff from the previous window:\n" + v} for k, v in texts.items()})
json.dump({"tools": tools, "messages": messages}, open(os.path.join(here, "fixtures.json"), "w"), ensure_ascii=False, indent=1)

READY = [{"wait": 2.5}]


def call(tool, fixture, **args):
    return {"name": tool, "arguments": {**args, "fixture": fixture}}


def cards(name, calls, width, height):
    steps = READY + [{"send": "go"}, {"key": "Enter"}, {"until": "all done" if width >= 12 else r"(?s)a\s*l\s*l\s*d\s*o\s*n\s*e", "timeout": 60}, {"snap": "collapsed", "settle": 1.5},
                     {"key": "C-o"}, {"snap": "expanded", "settle": 2.0}, {"key": "C-o"}, {"snap": "collapsed-again", "settle": 1.5}]
    s = {"name": name, "replies": [{"tool_calls": calls, "chunk": 4000}, {"text": "all done", "chunk": 4000}], "width": width, "height": height, "steps": steps}
    json.dump(s, open(os.path.join(out, name + ".json"), "w"), indent=1)


def msgs(name, names, width, height):
    steps = list(READY)
    for n in names:
        steps += [{"send": f"/pt message {n}"}, {"key": "Enter"}, {"wait": 1.5}]
    steps += [{"snap": "collapsed", "settle": 1.5}, {"key": "C-o"}, {"snap": "expanded", "settle": 2.0}, {"key": "C-o"}, {"snap": "collapsed-again", "settle": 1.5}]
    json.dump({"name": name, "replies": ["unused"], "width": width, "height": height, "steps": steps}, open(os.path.join(out, name + ".json"), "w"), indent=1)


small = [call("notes", f"note-{k}", op="read", path="x.md") for k in ("cjk", "rtl", "ansi", "zalgo")]
hist = [call("history", f"hs-{k}", op="search", query="q\tz", all=True) for k in ("cjk", "rtl", "ansi")]
ctx = [call("get_context_remaining", "ctx-1250"), call("get_context_remaining", "ctx-half")]
for w, h in ((100, 80), (40, 160), (8, 200), (4, 200), (2, 200)):
    cards(f"ur-notes-w{w}", small, w, h)
    cards(f"ur-history-w{w}", hist, w, h)
    msgs(f"ur-msgs-w{w}", ["m-cjk", "m-rtl", "m-ansi", "w-cjk", "w-rtl", "w-ansi"], w, h)
for w, h in ((100, 60), (40, 60)):
    cards(f"ur-big-w{w}", [call("notes", "note-big", op="read", path="big.md"), call("notes", "note-thai", op="read", path="t.md")], w, h)
    msgs(f"ur-bigmsg-w{w}", ["m-big", "w-big", "m-zalgo"], w, h)
cards("ur-ctx", ctx, 100, 40)
msgs("ur-msgs-w1", ["m-cjk", "m-rtl", "m-ansi", "w-cjk", "w-rtl", "w-ansi"], 1, 300)
print(len(os.listdir(out)), "scenarios")
