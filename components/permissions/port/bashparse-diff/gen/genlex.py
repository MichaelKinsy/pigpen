#!/usr/bin/env python3
"""Transpiles the generated lexer of tree-sitter-bash 0.25.1 (ts_lex, ts_lex_keywords and their
character sets in src/parser.c) into Go (bashparse/internal/ts/lex_gen.go).

  python3 genlex.py <tree-sitter-bash>/src/parser.c <out lex_gen.go>

The generated C is a goto state machine built from a handful of macros (parser.h START_LEXER,
ADVANCE, ADVANCE_MAP, SKIP, ACCEPT_TOKEN, END_STATE); each maps one to one onto Go:
  START_LEXER/next_state/start  ->  `for { lookahead = l.lookahead(); eof = l.eof(); switch state {...} }`
  ADVANCE(n)                    ->  `state = n; l.advance(false); continue`
  SKIP(n)                       ->  `state = n; l.advance(true); continue`
  ACCEPT_TOKEN(sym)             ->  `result = true; l.setResult(sym); l.markEnd()`
  END_STATE()                   ->  `return result`
Symbol names become their numeric values from the enum in parser.c. Anything the script does not
recognise is a hard error, so a grammar update cannot silently change the lexer.
"""
import re
import sys

src = open(sys.argv[1]).read()

# ---- symbol values
enum = re.search(r"enum ts_symbol_identifiers \{(.*?)\};", src, re.S).group(1)
syms = {}
for name, val in re.findall(r"(\w+)\s*=\s*(\d+)", enum):
    syms[name] = int(val)
syms["ts_builtin_sym_end"] = 0

def sym(name):
    if name not in syms:
        sys.exit("unknown symbol " + name)
    return syms[name]

# ---- character sets
sets = {}
for m in re.finditer(r"static const TSCharacterRange (\w+)\[\] = \{(.*?)\};", src, re.S):
    sets[m.group(1)] = m.group(2)

def func_body(name):
    start = src.index("static bool %s(TSLexer *lexer, TSStateId state) {" % name)
    end = src.index("\n}\n", start)
    return src[start:end]

CHAR = r"'(?:\\.|[^'\\])'"

def go_literal(tok):
    tok = tok.strip()
    if re.fullmatch(CHAR, tok):
        c = tok
        if c == "'\\0'":
            return "0"
        return c
    if re.fullmatch(r"0x[0-9a-fA-F]+|\d+", tok):
        return tok
    sys.exit("bad literal " + tok)

def cond_to_go(cond):
    # lookahead comparisons, eof, set_contains, &&, ||, !, parentheses
    cond = re.sub(r"set_contains\((\w+), (\d+), lookahead\)", lambda m: "setContains(%s, lookahead)" % m.group(1), cond)
    # validate: only known tokens remain
    probe = re.sub(CHAR, "C", cond)
    probe = re.sub(r"setContains\(\w+, lookahead\)", "S", probe)
    probe = re.sub(r"0x[0-9a-fA-F]+|\b\d+\b", "N", probe)
    probe = re.sub(r"\blookahead\b|\beof\b", "V", probe)
    probe = re.sub(r"[\s()!&|<>=]", "", probe)
    probe = probe.replace("C", "").replace("S", "").replace("N", "").replace("V", "")
    if probe:
        sys.exit("unrecognised condition residue %r in %r" % (probe, cond))
    return cond.replace("\\0", "\\x00") if False else cond.replace("'\\0'", "0")

def action_to_go(a):
    a = a.strip()
    m = re.fullmatch(r"ADVANCE\((\d+)\)", a)
    if m:
        return "state = %s; l.advance(false); continue" % m.group(1)
    m = re.fullmatch(r"SKIP\((\d+)\)", a)
    if m:
        return "state = %s; l.advance(true); continue" % m.group(1)
    m = re.fullmatch(r"ACCEPT_TOKEN\((\w+)\)", a)
    if m:
        return "result = true; l.setResult(%d); l.markEnd()" % sym(m.group(1))
    sys.exit("unknown action " + a)

def split_statements(text):
    """Splits on ';' outside character literals (the map has ';' as a key)."""
    chunks, cur, i = [], [], 0
    while i < len(text):
        c = text[i]
        if c == "'":
            m = re.match(CHAR, text[i:])
            if not m:
                sys.exit("bad char literal near %r" % text[i:i + 10])
            cur.append(m.group(0))
            i += len(m.group(0))
            continue
        if c == ";":
            chunks.append("".join(cur))
            cur = []
        else:
            cur.append(c)
        i += 1
    chunks.append("".join(cur))
    return chunks

def transpile(name, goname):
    body = func_body(name)
    body = body[body.index("switch (state) {") + len("switch (state) {"):]
    out = []
    out.append("func %s(l *lexer, state uint16) bool {" % goname)
    out.append("\tresult := false")
    out.append("\tvar lookahead int32")
    out.append("\tvar eof bool")
    out.append("\tfor {")
    out.append("\t\tlookahead = l.data.lookahead")
    out.append("\t\teof = l.eof()")
    out.append("\t\t_ = eof")
    out.append("\t\tswitch state {")
    chunks = split_statements(body)
    for chunk in chunks:
        chunk = chunk.strip()
        if not chunk or chunk == "}":
            continue
        # a chunk may start with case labels / default
        while True:
            m = re.match(r"(?:case (\d+):|default:)\s*", chunk)
            if not m:
                break
            if m.group(1) is not None:
                out.append("\t\tcase %s:" % m.group(1))
            else:
                out.append("\t\tdefault:")
            chunk = chunk[m.end():]
        if not chunk or chunk == "}":
            continue
        if chunk == "END_STATE()":
            out.append("\t\t\treturn result")
            continue
        if chunk == "return false":
            out.append("\t\t\treturn false")
            continue
        m = re.fullmatch(r"ADVANCE_MAP\((.*)\)", chunk, re.S)
        if m:
            toks = [t.strip() for t in re.findall(r"(%s|0x[0-9a-fA-F]+|\d+)\s*,?" % CHAR, m.group(1))]
            if len(toks) % 2:
                sys.exit("odd ADVANCE_MAP")
            seen = set()
            out.append("\t\t\tswitch lookahead {")
            for i in range(0, len(toks), 2):
                key, target = go_literal(toks[i]), toks[i + 1]
                kv = eval_char(key)
                if kv in seen:
                    continue  # the C map takes the first match
                seen.add(kv)
                out.append("\t\t\tcase %s:\n\t\t\t\tstate = %s; l.advance(false); continue" % (key, target))
            out.append("\t\t\t}")
            continue
        m = re.fullmatch(r"if \((.*)\)\s+((?:ADVANCE|SKIP|ACCEPT_TOKEN)\(\w+\))", chunk, re.S)
        if m:
            cond = re.sub(r"\s+", " ", m.group(1))
            out.append("\t\t\tif %s { %s }" % (cond_to_go(cond), action_to_go(m.group(2))))
            continue
        m = re.fullmatch(r"(ACCEPT_TOKEN\(\w+\)|ADVANCE\(\d+\)|SKIP\(\d+\))", chunk)
        if m:
            out.append("\t\t\t" + action_to_go(m.group(1)))
            continue
        sys.exit("unrecognised chunk: %r" % chunk[:200])
    out.append("\t\t}")
    out.append("\t}")
    out.append("}")
    return "\n".join(out)

def eval_char(lit):
    if lit.startswith("'"):
        body = lit[1:-1]
        esc = {"n": 10, "t": 9, "r": 13, "v": 11, "f": 12, "0": 0, "\\": 92, "'": 39, '"': 34, "a": 7, "b": 8}
        if body.startswith("\\"):
            return esc[body[1]]
        return ord(body)
    return int(lit, 0)

go = ["// Code generated by port/bashparse-diff/gen/genlex.py from tree-sitter-bash 0.25.1 src/parser.c; DO NOT EDIT.", "", "package ts", ""]
go.append("var charSets = map[string][]charRange{}")
go.append("")
for name, body in sets.items():
    ranges = re.findall(r"\{\s*(%s|0x[0-9a-fA-F]+|\d+)\s*,\s*(%s|0x[0-9a-fA-F]+|\d+)\s*\}" % (CHAR, CHAR), body)
    go.append("var %s = []charRange{" % name)
    for a, b in ranges:
        go.append("\t{%s, %s}," % (go_literal(a), go_literal(b)))
    go.append("}")
    go.append("")
go.append(transpile("ts_lex", "lexMain"))
go.append("")
go.append(transpile("ts_lex_keywords", "lexKeywords"))
go.append("")
text = "\n".join(go).replace("var charSets = map[string][]charRange{}\n\n", "")
open(sys.argv[2], "w").write(text)
