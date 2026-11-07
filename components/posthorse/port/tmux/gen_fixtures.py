#!/usr/bin/env python3
"""Writes fixtures.json: the canned tool results and messages of the card probe (probe/probe.ts and probe/probe.go.tmpl).

The `details` follow src/ui/display.ts exactly, with offsets in UTF-16 units as the original counts them.
usage: gen_fixtures.py > fixtures.json
"""
import json


def u16(s):
    return len(s.encode("utf-16-le")) // 2


def note_read(path, body, *, offset=0, end=None, total=None):
    header = f"File: /work/.pi/notes/{path}\n"
    total = u16(body) if total is None else total
    end = u16(body) if end is None else end
    more = f"\n[chars {offset}-{end} of {total}; continue with offset {end}]" if end < total else ""
    return {"text": header + body + more, "details": {"kind": "note-read", "headerLength": u16(header), "offset": offset, "end": end, "total": total}}


def span_entries(entries):
    """history search: entries are (header, body); the text is `header body` joined by newlines, spans in UTF-16 units."""
    parts, spans = [], []
    for header, body in entries:
        part = header + body
        parts.append(part)
        spans.append({"headerLength": u16(header), "length": u16(part)})
    return "\n".join(parts), spans


wide = "界 👩🏽‍💻 é "
long_body = "START " + wide * 40 + " END"
long_page = long_body[:600]
hist_entries = [(f"2026-09-06T17:43:46Z [window w1] [entry-{i}] [user] ", f"Relay station {i}: route details " * 3) for i in range(6)]
hist_text, hist_spans = span_entries(hist_entries)
footer = '\n[More results; continue with cursor "abc123"]'
hist_text_more = hist_text + footer

tools = {
    "note-short": note_read("plan.md", "A readable note\nsecond line"),
    "note-long": note_read("ledger.md", long_page, end=u16(long_page), total=u16(long_body)),
    "note-empty": {"text": "", "details": {"kind": "note-read", "headerLength": 0, "offset": 0, "end": 0, "total": 0}},
    "note-write": {"text": "Wrote .pi/notes/draft.md", "details": {"kind": "note-write"}},
    "note-append": {"text": "Appended to .pi/notes/draft.md", "details": {"kind": "note-append"}},
    "notes-list": {"text": "Notes directory: /work/.pi/notes\na.md  12B  2026-09-06T17:43Z\nb.md  40B  2026-09-06T17:40Z\nc.md  7B  2026-09-06T17:30Z",
                   "details": {"kind": "notes-list", "headerLength": u16("Notes directory: /work/.pi/notes\n"), "count": 3, "page": {"offset": 0, "end": 100, "total": 100}}},
    "notes-search-more": {"text": "a.md:1: needle 1\nb.md:2: needle 2\n[chars 0-34; more matches follow; continue with offset 34]",
                          "details": {"kind": "notes-search", "headerLength": 0, "count": 2, "page": {"offset": 0, "end": 34, "total": 400}, "more": True}},
    "notes-none": {"text": "(no notes yet)", "details": {"kind": "notes-list", "headerLength": 0, "count": 0, "page": {"offset": 0, "end": 14, "total": 14}}},
    "plain-long": {"text": "\n".join(f"line {i} " + "word " * 12 for i in range(30))},
    "error-missing": {"error": "No note at missing.md. Use list."},
    "history-search": {"text": hist_text_more, "details": {"kind": "history-search", "entries": hist_spans, "footerLength": u16(footer), "more": True, "skipped": 2}},
    "history-empty": {"text": 'No history matches "absent".', "details": {"kind": "history-search", "entries": [], "footerLength": 0, "more": False, "skipped": 0}},
    "history-read-images": {"text": "2026-09-06T17:43:46Z [window w1] [pic] [chars 0-40 of 9000] " + "route " * 6 + "\nMore remains; call history read with id \"pic\" and offset 40 and imageOffset 1.",
                            "images": 2,
                            "details": {"kind": "history-read", "headerLength": u16("2026-09-06T17:43:46Z [window w1] [pic] [chars 0-40 of 9000] "), "offset": 0, "end": 40, "total": 9000, "imageOffset": 0, "imageEnd": 1, "imageTotal": 2}},
    "history-legacy": {"text": "archive.jsonl 2026-09-06T17:43:46Z [window old] [owner-1] [user] Relay station is ready.\nSECOND LINE"},
    "context-enabled": {"text": "≈90,000 tokens until automatic rollover (line at 91,000); ≈99,000 tokens until the configured context limit (1,000/100,000 used, 1%). Best available native estimate.",
                        "details": {"kind": "context", "usage": {"tokens": 1000, "contextWindow": 100000, "percent": 1}, "rollover": "enabled", "rolloverAt": 91000}},
    "context-unsupported": {"text": "Automatic rollover is unsupported for this window. ≈7,000 tokens until the configured context limit (1,192/8,192 used, 15%). Best available native estimate.",
                            "details": {"kind": "context", "usage": {"tokens": 1192, "contextWindow": 8192, "percent": 14.55}, "rollover": "unsupported"}},
    "context-disabled": {"text": "Automatic rollover is disabled in the available settings (Pi compaction.enabled=false). ≈99,000 tokens until the configured context limit (1,000/100,000 used, 1%). Best available native estimate.",
                         "details": {"kind": "context", "usage": {"tokens": 1000, "contextWindow": 100000, "percent": 1}, "rollover": "disabled"}},
    "context-unknown": {"text": "Context usage is not known until the next model response.", "details": {"kind": "context"}},
    "new-context": {"text": "Requested after foreground tools succeed.\nBackground work continues.", "details": {"kind": "new-context"}},
    "running": {"delayMs": 5000, "partial": {"text": "still working on it", "details": {"kind": "note-write"}}, "text": "Wrote .pi/notes/late.md", "details": {"kind": "note-write"}},
    "controls": {"text": "safe head \x1b[2J\x1b]52;c;c2VjcmV0\x07\x1b_hidden\x1b\\ safe tail\x00\r", "details": {"kind": "note-write"}},
    "malformed-details": {"text": "text kept", "details": {"kind": "history-search", "entries": [None]}},
}

handoff = "\n".join(f"Handoff line {i + 1}" for i in range(60))
messages = {
    "reminder": {"customType": "posthorse-reminder", "content": "Checkpoint now: save notes before the window fills.\n" + "remember this\n" * 6 + "LAST LINE"},
    "reminder-short": {"customType": "posthorse-reminder", "content": "one\ntwo\nthree"},
    "window": {"customType": "context-window", "content": "Context window window-2 starts here. Earlier conversation is not available in this window.\n\nHandoff from the previous window:\n" + handoff,
               "details": {"windowId": "window-2", "tokensBefore": 1000}},
    "window-wide": {"customType": "context-window", "content": "Context window window-3 starts here.\n\nHandoff from the previous window:\n" + wide * 80,
                    "details": {"windowId": "window-3", "tokensBefore": 2000}},
    "controls": {"customType": "posthorse-reminder", "content": "Checkpoint now\n" + "remember\n" * 8 + "LAST\x1b[2J\x1b]52;c;c2VjcmV0\x07\x00"},
}
print(json.dumps({"tools": tools, "messages": messages}, ensure_ascii=False, indent=1))
