#!/usr/bin/env python3
"""Generates scenarios/*.json for the frame-equivalence harness (see recorder/record.ts and
extensions/rpiv-ask-user-question/frames_test.go). The JSON files are the committed scenarios; this script is
where they are authored (symbolic keys instead of raw escape strings). Run: python3 gen-scenarios.py

Scenario: {name, description, params (the tool's arguments), width, rows, collapseKey?, locale?, keybindings?,
rawInput? (false = the host has no raw onTerminalInput listener), hyperlinks? (true = OSC 8 supported; default off), steps}. A step is one chunk of raw terminal
input {input, label?, render? (false = no frame), settle? ("notify": wait for the editor-failure notification),
resize? ({width, rows} applied before the input)}. Every scenario must end by completing the questionnaire.
"""
import json
import os

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "scenarios")

KEYS = {
    "up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D",
    "enter": "\r", "esc": "\x1b", "tab": "\t", "stab": "\x1b[Z", "space": " ",
    "bs": "\x7f", "del": "\x1b[3~", "home": "\x1b[H", "end": "\x1b[F",
    "pgup": "\x1b[5~", "pgdn": "\x1b[6~",
    "ctrl-c": "\x03", "ctrl-u": "\x15", "ctrl-g": "\x07", "ctrl-]": "\x1d",
    "ctrl-a": "\x01", "ctrl-e": "\x05", "ctrl-w": "\x17", "ctrl-k": "\x0b", "ctrl-y": "\x19",
    "ctrl-p": "\x10", "ctrl-n": "\x0e", "ctrl-q": "\x11", "ctrl-j": "\n", "ctrl-z": "\x1a", "ctrl-_": "\x1f",
    "alt-b": "\x1bb", "alt-f": "\x1bf", "alt-d": "\x1bd", "alt-o": "\x1bo", "alt-backspace": "\x1b\x7f",
    "shift-enter": "\x1b[13;2u", "ctrl-enter": "\x1b[13;5u",
    # Kitty keyboard protocol: CSI-u and the event-type form (1 press, 2 repeat, 3 release)
    "k-enter": "\x1b[13u", "k-enter-press": "\x1b[13;1:1u", "k-enter-release": "\x1b[13;1:3u",
    "k-esc": "\x1b[27u", "k-down": "\x1b[1;1:1B", "k-down-release": "\x1b[1;1:3B", "k-up": "\x1b[1;1:1A",
    "k-tab": "\x1b[9u", "k-space": "\x1b[32u",
    "k-ctrl-]": "\x1b[93;5u", "k-ctrl-]-repeat": "\x1b[93;5:2u", "k-ctrl-]-release": "\x1b[93;5:3u",
    "k-a": "\x1b[97u", "k-b": "\x1b[98u", "k-A": "\x1b[97;2u",
}


def paste(text):
    return "\x1b[200~" + text + "\x1b[201~"


def key(*names):
    return [{"input": KEYS.get(n, n), "label": n} for n in names]


def keys_quiet(*names):
    """Keys with no frame (only the next step with a frame shows their effect)."""
    return [{"input": KEYS.get(n, n), "label": n, "render": False} for n in names]


def typed(chunks, every=False):
    """Typed text, one chunk per character (or per given cluster); a frame only after the last (every=True: all)."""
    if isinstance(chunks, str):
        chunks = list(chunks)
    out = []
    for i, c in enumerate(chunks):
        step = {"input": c, "label": "type"}
        if not every and i != len(chunks) - 1:
            step["render"] = False
        out.append(step)
    return out


def pasted(text, label="paste"):
    return [{"input": paste(text), "label": label}]


def raw(data, label=None, **kw):
    s = {"input": data}
    if label:
        s["label"] = label
    s.update(kw)
    return [s]


def Q(question, header, options, multi=False):
    q = {"question": question, "header": header, "options": [
        ({"label": l, "description": d} if p is None else {"label": l, "description": d, "preview": p})
        for (l, d, p) in [o if len(o) == 3 else (o[0], o[1], None) for o in options]]}
    if multi:
        q["multiSelect"] = True
    return q


LIB = Q("Which library should we use for date formatting?", "Library", [
    ("date-fns (Recommended)", "Modular, tree-shakeable functions"),
    ("Luxon", "Immutable wrapper with time zone support"),
    ("Day.js", "Tiny Moment-compatible API"),
])
FEAT = Q("Which features do you want to enable?", "Features", [
    ("Auth", "Sign-in and sessions"),
    ("Billing", "Plans and invoices"),
    ("Search", "Full-text search over records"),
    ("Export", "CSV and JSON download"),
], multi=True)
DB = Q("Which database fits the workload?", "Database", [
    ("SQLite", "Embedded, zero setup", "file.db\n  └─ one file"),
    ("Postgres", "Server, concurrent writers", "app ──▶ pg\n       └─ WAL"),
    ("DuckDB", "Columnar analytics", "scan ▒▒▒▒▒▒▒▒"),
])
LONG_PREVIEW = "\n".join("line %02d: the quick brown fox jumps over the lazy dog" % i for i in range(1, 31))
LONGP = Q("Which layout?", "Layout", [
    ("Wide", "Many columns", LONG_PREVIEW),
    ("Tall", "Many rows", "\n".join("row %02d" % i for i in range(1, 41))),
    ("Compact", "Dense", "tiny"),
])
MD_PREVIEW = "\n".join([
    "# Heading one", "", "Some **bold**, *italic*, `inline code` and a [link](https://example.com).", "",
    "- first item", "- second item", "  - nested item", "", "1. one", "2. two", "", "> a quoted line", "",
    "```", "plain fence", "  indented", "```", "", "---", "", "| a | b |", "|---|---|", "| 1 | 2 |",
])
MDQ = Q("Which template?", "Template", [
    ("Rich", "Markdown showcase", MD_PREVIEW),
    ("Plain", "Just text", "no markup here, only a sentence that is long enough to wrap in a narrow pane."),
    ("Code", "Fenced code", "```\nfunc main() {\n\tprintln(\"hi\")\n}\n```"),
])
LANGQ = Q("Which snippet?", "Snippet", [
    ("TypeScript", "ts fence", "```ts\nconst x: number = 1;\n// comment\nfunction f(a: string) { return `${a}!`; }\n```"),
    ("Go", "go fence", "```go\nfunc main() {\n\tfmt.Println(\"hi\", 42)\n}\n```"),
    ("JSON", "json fence", "```json\n{\"a\": [1, true, null], \"b\": \"x\"}\n```"),
    ("Text", "text fence", "```text\nnot highlighted\n```"),
])
MIXP = Q("Which option has a preview?", "Mixed", [
    ("Has preview", "Shows a preview", "this option has a preview"),
    ("No preview", "Has none"),
    ("Also preview", "Another one", "second\npreview"),
])
LONGTEXT = Q(
    "This is a deliberately long question text that has to wrap across several lines at narrow terminal widths, "
    "and it keeps going so that wrapping is exercised properly?", "A long header text",
    [("An option label that is very long and goes up to sixty characters wide", "A description that is also long enough to wrap onto a second and a third line when the terminal is narrow"),
     ("Short", "short"),
     ("Third", "")])
CJK = Q("どのライブラリを使いますか？ 日本語と😀の幅", "言語", [
    ("日本語ライブラリ", "全角文字の説明文がここに入ります。折り返しの確認用です。"),
    ("Emoji 😀🎉 label", "A description with emoji 👨‍👩‍👧 and combining e\u0301 marks"),
    ("한국어", "한글 설명 text mixed"),
])
Q2 = Q("Pick the runtime", "Runtime", [("Node", "JavaScript"), ("Bun", "Fast"), ("Deno", "Secure")])
Q3 = Q("Pick the editor", "Editor", [("Vim", "Modal"), ("Emacs", "Extensible"), ("Nano", "Simple")])
FOUR = [
    Q("First question?", "Alpha header", [("A1", "a"), ("A2", "b")]),
    Q("Second question?", "Beta header ok", [("B1", "a"), ("B2", "b")], multi=True),
    Q("Third question?", "Gamma", [("C1", "a"), ("C2", "b", "preview c2")]),
    Q("Fourth question?", "Delta header 16", [("D1", "a"), ("D2", "b")]),
]

SCENARIOS = []


def sc(name, questions, steps, width=80, rows=24, description="", **opts):
    s = {"name": name, "description": description, "params": {"questions": questions}, "width": width, "rows": rows}
    s.update(opts)
    s["steps"] = [dict(x) for x in steps]
    SCENARIOS.append(s)


# ---- single select ---------------------------------------------------------------------------------------------
sc("single-basic", [LIB], key("down", "down", "down", "down", "up", "up", "up", "enter"),
   description="navigate, wrap both ways, pick an option")
sc("single-pick-last-option", [LIB], key("down", "down", "enter"))
sc("single-cancel-esc", [LIB], key("down", "esc"))
sc("single-cancel-ctrl-c", [LIB], key("ctrl-c"))
sc("single-w40", [LIB], key("down", "n") + typed("note") + key("enter", "enter"), width=40)
sc("single-w20", [LIB], key("down", "down", "up", "enter"), width=20)
sc("single-w12", [LIB], key("down", "enter"), width=12)
sc("single-wrap-long-text-w50", [LONGTEXT], key("down", "down", "up", "enter"), width=50)
sc("single-wrap-long-text-w24", [LONGTEXT], key("down", "enter"), width=24)
sc("single-cjk-emoji-w40", [CJK], key("down", "down", "enter"), width=40)
sc("single-cjk-emoji-w80", [CJK], key("down", "enter"), width=80)
sc("single-kitty-keys", [LIB], key("k-down", "k-down-release", "k-up", "k-down", "k-enter-release", "k-enter"),
   description="Kitty protocol press and release events: releases are dropped before the component")
sc("single-empty-description", [Q("Empty descriptions?", "Empty", [("One", ""), ("Two", "second")])],
   key("down", "enter"))
sc("single-crlf-normalised", [Q("Line one\r\nline two\rline three?", "CRLF", [("A\r\nB", "desc\r\nmore"), ("C", "c")])],
   key("enter"), description="line terminators are normalised before display")

# ---- multi select ----------------------------------------------------------------------------------------------
sc("multi-basic", [FEAT], key("space", "down", "enter", "down", "down", "space", "down", "down", "enter"),
   description="space and enter toggle; Next confirms")
sc("multi-uncheck", [FEAT], key("space", "space", "down", "space", "down", "down", "down", "down", "enter"))
sc("multi-next-empty", [FEAT], key("down", "down", "down", "down", "down", "enter"), description="Next with nothing checked")
sc("multi-w40", [FEAT], key("space", "down", "space", "down", "down", "down", "down", "enter"), width=40)
sc("multi-w20", [FEAT], key("space", "down", "space", "down", "down", "down", "down", "enter"), width=20)
sc("multi-type-something", [FEAT], key("space", "down", "down", "down", "down") + typed("my own") + key("enter"),
   description="the Type something row inside a multi-select question")
sc("multi-rows-10", [FEAT], key("space", "down", "space", "down", "down", "down", "down", "enter"), rows=10)
sc("multi-notes", [FEAT], key("space", "n") + typed("why") + key("enter", "down", "down", "down", "down", "down", "enter"))
sc("multi-cjk", [Q("功能？", "功能", [("认证", "登录和会话"), ("账单 💰", "计划和发票"), ("搜索", "全文检索")], multi=True)],
   key("space", "down", "space", "down", "down", "enter"), width=30)

# ---- multi-question flows --------------------------------------------------------------------------------------
sc("flow-three", [LIB, FEAT, DB],
   key("enter", "space", "down", "down", "down", "down", "down", "enter", "down", "enter", "enter"),
   description="auto-advance across tabs, the Submit tab, Submit")
sc("flow-tab-navigation", [LIB, FEAT, DB],
   key("tab", "tab", "tab", "tab", "right", "left", "stab", "stab", "stab", "left", "esc"))
sc("flow-partial-then-cancel", [LIB, FEAT, DB],
   key("enter", "tab", "tab", "down", "up", "down", "enter"),
   description="one answer, Submit tab shows the missing warning; Cancel row")
sc("flow-partial-submit", [LIB, FEAT, DB], key("enter", "tab", "tab", "enter"),
   description="Submit with unanswered questions")
sc("flow-revisit-answered", [LIB, Q2, Q3],
   key("down", "enter", "down", "enter", "stab", "stab", "down", "enter", "tab", "tab", "enter"),
   description="answer, go back, change an answer")
sc("flow-draft-preserved", [LIB, Q2],
   key("down", "down", "down") + typed("draft") + key("tab", "tab", "tab", "stab", "stab", "enter", "esc"),
   description="a custom draft survives tab switches")
sc("flow-w40", [LIB, FEAT, DB], key("enter", "space", "down", "down", "down", "down", "down", "enter", "enter", "enter"), width=40)
sc("flow-w20", [LIB, Q2], key("enter", "enter", "enter"), width=20)
sc("flow-four-w80", FOUR, key("enter", "space", "down", "down", "down", "enter", "enter", "enter", "enter"))
sc("flow-four-w40", FOUR, key("tab", "tab", "tab", "tab", "enter"), width=40)
sc("flow-four-w24", FOUR, key("right", "right", "right", "right", "enter"), width=24)
sc("flow-all-answered", [LIB, Q2], key("enter", "enter", "up", "down", "enter"))
sc("flow-global-note", [LIB, Q2], key("enter", "enter", "n") + typed("overall") + key("enter", "enter"),
   description="n on the Submit tab opens the global note")
sc("flow-global-note-escape", [LIB, Q2], key("enter", "enter", "n") + typed("overall") + key("esc", "esc"))
sc("flow-multi-cancel-esc", [LIB, Q2], key("esc"))

# ---- previews --------------------------------------------------------------------------------------------------
sc("preview-short", [DB], key("down", "down", "up", "enter"), width=100)
sc("preview-short-w60", [DB], key("down", "enter"), width=60)
sc("preview-short-w40", [DB], key("down", "enter"), width=40)
sc("preview-short-w20", [DB], key("down", "enter"), width=20)
sc("preview-long", [LONGP], key("down", "down", "enter"), width=100)
sc("preview-long-rows12", [LONGP], key("down", "up", "enter"), width=100, rows=12)
sc("preview-long-rows8-w40", [LONGP], key("down", "enter"), width=40, rows=8)
sc("preview-markdown", [MDQ], key("down", "down", "up", "enter"), width=100)
sc("preview-markdown-w60", [MDQ], key("enter"), width=60)
sc("preview-markdown-w30", [MDQ], key("enter"), width=30)
sc("preview-markdown-hyperlinks", [MDQ], key("enter"), width=100, hyperlinks=True, description="OSC 8 links when the terminal supports them")
sc("preview-code-fence-languages", [LANGQ], key("down", "down", "down", "enter"), width=100,
   description="fenced code with and without a highlightable language")
sc("preview-mixed", [MIXP], key("down", "down", "up", "enter"), width=90)
sc("preview-mixed-w40", [MIXP], key("down", "enter"), width=40)
sc("preview-type-something", [DB], key("down", "down", "down") + typed("a custom answer wider than the options column") + key("enter"),
   width=100, description="the Type something row expands to the full pane width while typing")
sc("preview-type-something-w40", [DB], key("down", "down", "down") + typed("custom answer") + key("esc"), width=40)
sc("preview-notes", [DB], key("n") + typed("preview note") + key("enter", "enter"), width=100)
sc("preview-multi-question", [DB, MDQ, LIB],
   key("down", "tab", "down", "tab", "tab", "stab", "stab", "enter", "enter", "enter"), width=100,
   description="cross-tab adaptive left width across preview and plain questions")
sc("preview-multi-question-w50", [DB, LIB], key("down", "enter", "enter", "enter"), width=50)
sc("preview-multi-with-multiselect", [DB, FEAT], key("down", "enter", "space", "down", "down", "down", "down", "down", "enter", "enter"), width=100)

# ---- notes -----------------------------------------------------------------------------------------------------
sc("notes-basic", [LIB], key("n") + typed("remember this") + key("enter", "enter"))
sc("notes-escape", [LIB], key("n") + typed("draft") + key("esc", "n", "esc", "enter"), description="Esc leaves the editor, the draft is kept")
sc("notes-multiline", [LIB], key("n") + typed("line one") + key("shift-enter") + typed("line two") + key("ctrl-j") + typed("three") + key("enter", "enter"))
sc("notes-edit-keys", [LIB], key("n") + typed("hello world") + key("left", "left", "bs", "home", "alt-f", "ctrl-k", "ctrl-y", "end", "enter", "enter"))
sc("notes-paste", [LIB], key("n") + pasted("pasted note\nsecond line") + key("enter", "enter"))
sc("notes-paste-large", [LIB], key("n") + pasted("\n".join("line %d" % i for i in range(1, 16))) + key("enter", "enter"),
   description="a large paste becomes a marker in the editor and expands in the answer")
sc("notes-w20", [LIB], key("n") + typed("a long note that wraps many times") + key("enter", "enter"), width=20)
sc("notes-cjk", [LIB], key("n") + typed(["日", "本", "語", "😀", "👨‍👩‍👧"]) + key("enter", "enter"), width=30)
sc("notes-per-tab", [LIB, Q2], key("n") + typed("first") + key("enter", "enter", "n") + typed("second") + key("enter", "enter", "enter"))
sc("notes-rows-10", [LIB], key("n") + typed("x") + key("shift-enter") + typed("y") + key("shift-enter") + typed("z") + key("enter", "enter"), rows=10)
sc("notes-then-collapse", [LIB], key("n") + typed("abc") + key("ctrl-]", "ctrl-]", "enter", "enter"))

# ---- Type something (inline input) ----------------------------------------------------------------------------
TS = ["down", "down", "down"]
sc("input-basic", [LIB], key(*TS) + typed("custom") + key("enter"))
sc("input-empty-enter", [LIB], key(*TS) + key("enter"), description="Enter on an empty buffer")
sc("input-escape", [LIB], key(*TS) + typed("abc") + key("esc"))
sc("input-editing", [LIB], key(*TS) + typed("hello world") + key("left", "left", "bs", "del", "home", "right", "alt-f", "alt-b", "end", "ctrl-w", "enter"))
sc("input-undo-yank", [LIB], key(*TS) + typed("one two three") + key("ctrl-w", "ctrl-w", "ctrl-y", "ctrl-_", "enter"))
sc("input-ctrl-u-clear", [LIB], key(*TS) + typed("clear me") + key("left", "left", "ctrl-u", "enter"), description="Ctrl+U clears the whole draft")
sc("input-paste", [LIB], key(*TS) + pasted("pasted text") + key("enter"))
sc("input-paste-newlines", [LIB], key(*TS) + pasted("first\nsecond\nthird") + key("enter"))
sc("input-paste-large", [LIB], key(*TS) + pasted("\n".join("row %d" % i for i in range(1, 14))) + key("enter"))
sc("input-paste-cjk", [LIB], key(*TS) + pasted("日本語のテキストを貼り付け") + key("enter"), width=30)
sc("input-wrap-w40", [LIB], key(*TS) + typed("this custom answer is long enough that it must wrap at forty columns") + key("enter"), width=40)
sc("input-wrap-w20", [LIB], key(*TS) + typed("this custom answer wraps at twenty columns wide") + key("up", "left", "enter"), width=20)
sc("input-multiline", [LIB], key(*TS) + typed("alpha") + key("shift-enter") + typed("beta") + key("shift-enter") + typed("gamma") + key("up", "up", "up", "down", "down", "enter"),
   description="newline inside the input; up/down move in the draft before they navigate options")
sc("input-cursor-then-nav", [LIB], key(*TS) + typed("draft") + key("up", "down", "enter"), description="up leaves the Type something row, down returns with the draft")
sc("input-cjk-emoji", [LIB], key(*TS) + typed(["日", "本", "語", " ", "😀", "👨‍👩‍👧", "e\u0301", "🇯🇵"]) + key("left", "left", "bs", "enter"), width=40)
sc("input-cjk-wrap-w20", [LIB], key(*TS) + typed(list("日本語の長い文章がここに入ります")) + key("enter"), width=20)
sc("input-kitty-text", [LIB], key(*TS) + key("k-a", "k-b", "k-A", "k-enter"), description="Kitty CSI-u text keys")
sc("input-external-editor-key", [LIB], key(*TS) + typed("abc") + [{"input": KEYS["ctrl-g"], "label": "ctrl-g", "settle": "notify"}] + key("enter"),
   description="Ctrl+G: the editor failure is reported and the draft kept (the notification text differs: known)")
sc("input-multi-tab-draft", [LIB, Q2], key(*TS) + typed("first") + key("enter") + key("down", "down", "down") + typed("second") + key("enter", "enter"))
sc("input-rows-10", [LIB], key(*TS) + typed("a") + key("shift-enter") + typed("b") + key("shift-enter") + typed("c") + key("shift-enter") + typed("d") + key("enter"), rows=10)
sc("input-collapse", [LIB], key(*TS) + typed("abc") + key("ctrl-]", "x", "ctrl-]") + typed("d") + key("enter"))

# ---- collapse / expand -----------------------------------------------------------------------------------------
sc("collapse-overlay", [LIB], key("down", "ctrl-]", "down", "ctrl-]", "ctrl-]", "k-ctrl-]-release", "k-ctrl-]", "esc"),
   description="the overlay is hidden through the handle; the raw listener reopens it; one hide notice")
sc("collapse-kitty-events", [LIB], key("k-ctrl-]", "k-ctrl-]-repeat", "k-ctrl-]-release", "k-ctrl-]", "k-ctrl-]-release", "enter"))
sc("collapse-fallback-no-raw", [LIB], key("ctrl-]", "down", "ctrl-]", "down", "enter"), rawInput=False,
   description="no raw listener: the visible one-line row, keys swallowed")
sc("collapse-fallback-cancel", [LIB], key("ctrl-]", "esc"), rawInput=False)
sc("collapse-submit-tab", [LIB, Q2], key("enter", "enter", "ctrl-]", "ctrl-]", "enter"))
sc("collapse-multi-select", [FEAT], key("space", "ctrl-]", "space", "ctrl-]", "down", "down", "down", "down", "down", "enter"))
sc("collapse-preview", [DB], key("down", "ctrl-]", "ctrl-]", "enter"), width=100)
sc("collapse-w20", [LIB], key("ctrl-]", "ctrl-]", "enter"), width=20, rawInput=False)
sc("collapse-alt-o", [LIB], key("alt-o", "alt-o", "ctrl-]", "enter"), collapseKey="alt+o", description="configured collapse key; hint shows Alt+O")
sc("collapse-alt-o-fallback", [LIB], key("alt-o", "esc"), collapseKey="alt+o", rawInput=False)
sc("collapse-off", [LIB], key("ctrl-]", "enter"), collapseKey="off", description="shortcut disabled: ctrl+] does nothing, hint line omits it")
sc("collapse-invalid-spec", [LIB], key("ctrl-]", "ctrl-]", "enter"), collapseKey="ctr+]", description="an invalid spec falls back to the default")
sc("collapse-pagedown", [LIB], key("pgdn", "pgdn", "enter"), collapseKey="PageDown", rawInput=False, description="spec lower-cased, key named PageDown in the hint")

# ---- keybindings -----------------------------------------------------------------------------------------------
sc("keybindings-ctrl-p-ctrl-n", [LIB], key("ctrl-n", "ctrl-n", "ctrl-p", "down", "up", "enter"),
   keybindings={"tui.select.up": "ctrl+p", "tui.select.down": "ctrl+n"}, description="user keybindings.json override: arrows no longer navigate")
sc("keybindings-cancel-remap", [LIB], key("esc", "ctrl-c", "ctrl-q"), keybindings={"tui.select.cancel": "ctrl+q"})
sc("keybindings-slack-style", [LIB], key(*TS) + typed("one") + key("enter") + typed("two") + key("ctrl-enter"),
   keybindings={"tui.input.newLine": "enter", "tui.input.submit": "ctrl+enter", "tui.select.confirm": "ctrl+enter"},
   description="Enter is newline, ctrl+enter confirms (#156)")
sc("keybindings-notes-slack", [LIB], key("n") + typed("a") + key("enter") + typed("b") + key("ctrl-enter", "ctrl-enter"),
   keybindings={"tui.input.newLine": "enter", "tui.input.submit": "ctrl+enter", "tui.select.confirm": "ctrl+enter"})
sc("keybindings-editor-up-down", [LIB], key(*TS) + typed("a") + key("shift-enter") + typed("b") + key("ctrl-p", "ctrl-p", "ctrl-n", "enter"),
   keybindings={"tui.editor.cursorUp": "ctrl+p", "tui.editor.cursorDown": "ctrl+n"})
sc("keybindings-clear-remap", [LIB], key(*TS) + typed("abc") + key("ctrl-u", "ctrl-k", "enter"),
   keybindings={"tui.editor.deleteToLineStart": "ctrl+k"})
sc("keybindings-external-editor-remap", [LIB], key(*TS) + typed("abc") + [{"input": KEYS["ctrl-q"], "label": "ctrl-q", "settle": "notify"}, {"input": KEYS["enter"], "label": "enter"}],
   keybindings={"app.editor.external": "ctrl+q"})

# ---- resize / overflow -----------------------------------------------------------------------------------------
sc("resize", [LIB, DB],
   key("down") + [{"input": KEYS["down"], "label": "down w40", "resize": {"width": 40, "rows": 24}},
                  {"input": KEYS["tab"], "label": "tab w20 rows10", "resize": {"width": 20, "rows": 10}},
                  {"input": KEYS["enter"], "label": "enter w100 rows30", "resize": {"width": 100, "rows": 30}}]
   + key("enter"), width=80, description="the terminal is resized between keystrokes")
sc("overflow-rows-6", [LONGP], key("down", "enter"), width=60, rows=6)
sc("overflow-rows-5-multi", [FEAT], key("space", "down", "enter", "down", "down", "down", "down", "enter"), rows=5)
sc("overflow-rows-8-flow", [LIB, FEAT, DB], key("enter", "space", "down", "down", "down", "down", "down", "enter", "enter", "enter"), rows=8, width=60)
sc("overflow-rows-12-long-text", [LONGTEXT], key("down", "enter"), width=30, rows=12)
sc("overflow-rows-1", [LIB], key("enter"), rows=1)

# ---- locales (one flow, every locale) -------------------------------------------------------------------------
for code in ["de", "en", "es", "fr", "pt", "pt-BR", "ru", "uk", "zh"]:
    sc("locale-" + code, [LIB, FEAT, DB],
       key("down", "n") + typed("x") + key("enter", "enter", "space", "down", "down", "down", "down")
       + typed("z") + key("enter", "down", "enter", "n") + typed("g") + key("enter", "enter"),
       locale=code, width=80, description="notes, Type something, Submit tab and global note in locale " + code)
sc("locale-ru-w40", [LIB, FEAT], key("down", "ctrl-]", "ctrl-]", "enter", "space", "down", "down", "down", "down", "down", "enter", "enter"), locale="ru", width=40, rawInput=False)
sc("locale-zh-preview", [DB], key("down", "enter"), locale="zh", width=60)
sc("locale-de-w30-collapse", [LIB], key("ctrl-]", "esc"), locale="de", width=30, rawInput=False)

# ---- more edge cases (editor behaviour, unicode, previews, terminal protocols) --------------------------------
WIDE = Q("Wide-character preview?", "Wide", [
    ("日本語", "全角", "┌──┐\n│日本│\n└──┘\n全角の行がボックスに入るかどうか、幅の計算を確認します。"),
    ("Emoji", "Emoji rows", "😀😀😀 smile\n👨‍👩‍👧 family\n🇯🇵 flag\ne\u0301 combining"),
    ("Tabs", "Tab and ANSI", "col1\tcol2\n\x1b[31mred text\x1b[0m plain\n  leading spaces\ntrailing   "),
])
LONGWORD = Q("A question with averyveryverylongwordthatcannotbewrappedatspacesbecauseithasnone and more?", "Long word", [
    ("averyveryverylongoptionlabelwithoutanyspacesinitatall", "adescriptionwithoutspacesthatisalsoverylongandmustbehardwrapped"),
    ("Second", "second"),
])
FOURP = Q("Four previews?", "Fours", [("One", "1", "alpha\nbeta"), ("Two", "2", "gamma"), ("Three", "3", "delta\n\nepsilon"), ("Four", "4", "zeta")])
NLDESC = Q("Newlines in description?", "NL", [("One", "line one\nline two\n\nline four"), ("Two", "  leading and trailing  ")])
sc("edge-wide-preview-w100", [WIDE], key("down", "down", "up", "enter"), width=100)
sc("edge-wide-preview-w50", [WIDE], key("down", "down", "enter"), width=50)
sc("edge-wide-preview-w24", [WIDE], key("enter"), width=24)
sc("edge-long-word-w30", [LONGWORD], key("down", "enter"), width=30)
sc("edge-long-word-w12", [LONGWORD], key("enter"), width=12)
sc("edge-four-previews", [FOURP], key("down", "down", "down", "up", "enter"), width=70)
sc("edge-description-newlines", [NLDESC], key("down", "enter"), width=40)
sc("edge-preview-w16", [DB], key("down", "enter"), width=16)
sc("edge-preview-rows-4", [DB], key("enter"), width=100, rows=4)
sc("edge-empty-preview-string", [Q("Empty preview?", "Empty", [("A", "a", ""), ("B", "b", "has one")])], key("down", "enter"))
sc("edge-markdown-nested", [Q("Nested markdown?", "MD", [("List", "lists", "1. one\n   - nested a\n   - nested b\n2. two\n\n- [ ] task\n- [x] done\n\n> quote\n> > inner\n\n### h3\n\n#### h4\n\n~~strike~~ and __bold__ and _it_"), ("Table", "table", "| name | value |\n|------|-------|\n| a    | 1     |\n| long cell here | 22222 |\n\n***\n\nline  \nbreak")])],
   key("down", "enter"), width=90)
sc("edge-markdown-w26", [Q("Nested markdown?", "MD", [("List", "lists", "1. one\n   - nested a\n   - nested b\n\n> quote that is long enough to wrap in the narrow pane\n\n`code that is long enough to wrap`"), ("B", "b")])],
   key("enter"), width=26)
sc("edge-markdown-html-and-escapes", [Q("Escapes?", "Esc", [("A", "a", "a *star* \\*escaped\\* <b>html</b> &amp; entity\nhttps://autolink.example.com/path and www.example.org\n\n    indented code block\n\n![image alt](http://x/y.png)"), ("B", "b")])],
   key("enter"), width=70)

# editor behaviour: notes and Type something
sc("edge-input-tab-key", [LIB], key(*TS) + typed("a") + key("tab") + typed("b") + key("enter"), description="Tab in the inline input")
sc("edge-notes-tab-key", [LIB], key("n") + typed("a") + key("tab") + typed("b") + key("enter", "enter"))
sc("edge-input-word-nav", [LIB], key(*TS) + typed("one two  three-four five") + key("alt-b", "alt-b", "ctrl-w", "alt-f", "alt-d", "end", "alt-backspace", "enter"))
sc("edge-input-pageupdown-home-end", [LIB], key(*TS) + typed("alpha") + key("shift-enter") + typed("beta") + key("shift-enter") + typed("gamma") + key("pgup", "home", "pgdn", "end", "home", "ctrl-e", "ctrl-a", "enter"))
sc("edge-input-long-lines-scroll", [LIB], key(*TS) + typed("l1") + key("shift-enter") + typed("l2") + key("shift-enter") + typed("l3") + key("shift-enter") + typed("l4") + key("shift-enter") + typed("l5") + key("shift-enter") + typed("l6") + key("shift-enter") + typed("l7") + key("up", "up", "up", "up", "up", "up", "enter"), rows=24)
sc("edge-input-rows-6", [LIB], key(*TS) + typed("l1") + key("shift-enter") + typed("l2") + key("shift-enter") + typed("l3") + key("shift-enter") + typed("l4") + key("enter"), rows=6)
sc("edge-input-ansi-paste", [LIB], key(*TS) + pasted("red \x1b[31mtext\x1b[0m and \x1b]8;;http://x\x07link\x1b]8;;\x07 done") + key("enter"), description="escape sequences inside a bracketed paste")
sc("edge-input-paste-tabs-cr", [LIB], key(*TS) + pasted("a\tb\r\nc\rd") + key("enter"))
sc("edge-input-paste-then-type", [LIB], key(*TS) + pasted("x") + typed("yz") + key("left", "bs", "enter"))
sc("edge-input-two-large-pastes", [LIB], key(*TS) + pasted("\n".join("a%d" % i for i in range(12))) + typed(" mid ") + pasted("\n".join("b%d" % i for i in range(12))) + key("enter"))
sc("edge-input-arabic-rtl", [LIB], key(*TS) + typed(list("مرحبا بالعالم")) + key("left", "left", "bs", "enter"), width=30)
sc("edge-input-zero-width", [LIB], key(*TS) + typed(["a", "\u200b", "b", "\u200d", "c", "\u00ad", "d"]) + key("enter"))
sc("edge-input-wide-at-boundary-w21", [LIB], key(*TS) + typed(list("ab日本語cde😀😀😀fgh")) + key("enter"), width=21)
sc("edge-input-wrap-then-nav-keys", [LIB], key(*TS) + typed("word " * 12) + key("up", "up", "down", "home", "end", "enter"), width=24)
sc("edge-input-long-word-w20", [LIB], key(*TS) + typed("averyveryverylongwordwithoutspaces") + key("enter"), width=20)
sc("edge-input-resize", [LIB], key(*TS) + typed("some custom answer text that wraps") + [{"input": KEYS["left"], "label": "left w24", "resize": {"width": 24, "rows": 24}}, {"input": KEYS["enter"], "label": "enter w60", "resize": {"width": 60, "rows": 24}}])
sc("edge-notes-resize", [LIB], key("n") + typed("some note text that wraps") + [{"input": KEYS["left"], "label": "left w22", "resize": {"width": 22, "rows": 12}}, {"input": KEYS["enter"], "label": "enter"}, {"input": KEYS["enter"], "label": "enter"}])
sc("edge-notes-kill-yank-undo", [LIB], key("n") + typed("alpha beta gamma") + key("alt-b", "ctrl-k", "ctrl-a", "ctrl-y", "ctrl-_", "ctrl-u", "enter", "enter"))
sc("edge-notes-multiline-nav", [LIB], key("n") + typed("aa") + key("shift-enter") + typed("bb") + key("shift-enter") + typed("cc") + key("up", "up", "home", "down", "end", "enter", "enter"))
sc("edge-notes-global-paste-large", [LIB, Q2], key("enter", "enter", "n") + pasted("\n".join("g%d" % i for i in range(14))) + key("enter", "enter"))
sc("edge-notes-paste-while-collapsed", [LIB], key("n", "ctrl-]") + pasted("lost paste") + key("ctrl-]") + typed("kept") + key("enter", "enter"))
sc("edge-notes-on-multiselect-next-row", [FEAT], key("space", "down", "down", "down", "down", "down", "n") + typed("n") + key("enter", "enter"), description="n on the Next row opens the notes editor")
sc("edge-n-key-in-input-types-n", [LIB], key(*TS) + typed("n n") + key("enter"))

# terminal protocols for the keys the questionnaire routes
sc("edge-modify-other-keys-collapse", [LIB], key("down") + raw("\x1b[27;5;93~", "mok ctrl+]") + raw("\x1b[27;5;93~", "mok ctrl+]") + key("enter"), description="xterm modifyOtherKeys form of the collapse key")
sc("edge-kitty-notes-and-submit", [LIB, Q2], key("k-enter") + key("k-enter") + raw("\x1b[110u", "kitty n") + typed("note") + key("k-enter", "k-enter"), description="Kitty CSI-u n and enter on the Submit tab")
sc("edge-kitty-alternate-keys", [LIB], key(*TS) + raw("\x1b[97:65;2u", "shifted A") + raw("\x1b[98;1:1u", "kitty b press") + raw("\x1b[98;1:2u", "kitty b repeat") + raw("\x1b[98;1:3u", "kitty b release") + raw("\x1b[13;1:2u", "enter repeat"))
sc("edge-kitty-arrows-modifiers", [LIB], raw("\x1b[1;2B", "shift+down") + raw("\x1b[1;5B", "ctrl+down") + raw("\x1b[1;3A", "alt+up") + key("down") + raw("\x1b[13;2u", "shift+enter") + key("enter"))
sc("edge-unknown-sequences", [LIB], raw("\x1b[99~", "unknown tilde") + raw("\x1b[?1;2c", "device attributes") + raw("\x1bOP", "F1 ss3") + raw("\x1b[Z", "shift+tab") + raw("x", "plain x") + key("enter"))
sc("edge-ss3-arrows", [LIB], raw("\x1bOB", "ss3 down") + raw("\x1bOB", "ss3 down") + raw("\x1bOA", "ss3 up") + key("enter"))
sc("edge-collapse-in-submit-tab-multiselect-flow", [FEAT, Q2], key("space", "down", "down", "down", "down", "down", "enter", "ctrl-]", "ctrl-]", "enter", "enter"))
sc("edge-collapse-repeat-fallback", [LIB], raw("\x1b[93;5u", "k ctrl+]") + raw("\x1b[93;5:3u", "release") + raw("\x1b[93;5:2u", "repeat") + raw("\x1b[93;5u", "k ctrl+]") + key("esc"), rawInput=False,
   description="fallback mode: the key reaches the component, which drops release and treats repeat as a press")
sc("edge-collapse-modifier-only", [LIB], raw("\x1b[93;5u", "ctrl+]") + raw("\x1b[93;6u", "ctrl+shift+]") + raw("\x1b[93;5u", "ctrl+]") + key("esc"))


def main():
    os.makedirs(OUT, exist_ok=True)
    for f in os.listdir(OUT):
        if f.endswith(".json"):
            os.remove(os.path.join(OUT, f))
    names = set()
    for s in SCENARIOS:
        assert s["name"] not in names, s["name"]
        names.add(s["name"])
        with open(os.path.join(OUT, s["name"] + ".json"), "w") as fh:
            json.dump(s, fh, ensure_ascii=False, indent="\t")
            fh.write("\n")
    print(len(SCENARIOS), "scenarios")


main()
