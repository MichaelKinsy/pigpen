#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (c) 2026 Michael Kinsy
"""Generate the differential cases from omp's real wire schemas (real_wire.json, written by
`oracle.ts schemas`). Deterministic. Writes cases.jsonl: {"id","tool","tag","args"} where
"args" is the JSON TEXT of the arguments (key order as a model would write it).

Usage: python3 gen.py [real_wire.json] [cases.jsonl]

Cases 0..15921 are the base corpus the review measured; cases after that are the extra classes
(tag prefix "x:"). Do not reorder the base section: ids are stable."""
import copy
import json
import sys

real_path = sys.argv[1] if len(sys.argv) > 1 else 'real_wire.json'
out_path = sys.argv[2] if len(sys.argv) > 2 else 'cases.jsonl'
real = json.load(open(real_path, encoding='utf-8'))
TOOLS = [t for t in real if t not in ('security_scan', 'think', 'report_issue', 'yield')]

def sample(s, full=True):
    if 'enum' in s: return s['enum'][0]
    if 'anyOf' in s: return sample(s['anyOf'][0], full)
    t = s.get('type')
    if isinstance(t, list): t = t[0]
    if t == 'string':
        if 'pattern' in s: return 'svc-1'
        return 'abc'
    if t in ('number', 'integer'):
        v = 7
        if 'minimum' in s: v = max(v, s['minimum'])
        if 'maximum' in s: v = min(v, s['maximum'])
        return v
    if t == 'boolean': return True
    if t == 'array':
        n = max(1, s.get('minItems', 1))
        return [sample(s.get('items', {}), full) for _ in range(n)]
    if t == 'object':
        o = {}
        req = s.get('required', [])
        for k, ps in s.get('properties', {}).items():
            if full or k in req: o[k] = sample(ps, full)
        return o
    return 'abc'

POOL = [None, "null", "", "abc", " abc ", "abc\n", "5", "5.5", "-3", "1e3", "007", " 12 ", "true", "false", "yes", "TRUE", "no", "on", "0", "1",
        0, 1, 5, -1, 5.5, 1e21, 999999, -999999, 0.5, True, False, [], ["a"], ["a", "b"], [1], [None], [["a"]], [{"a": 1}], {}, {"a": 1},
        "[]", "[\"a\"]", "[\"a\",\"b\"]", "[1,2]", "{}", "{\"a\":1}", "\"quoted\"", "[\"a\"] trailing", "[\"a\"", "[\"a\",]", "{\"a\":1}}", "null ", "NULL", "undefined", "x" * 300]

def slots(s, path=()):
    out = []
    if 'anyOf' in s:
        for b in s['anyOf']:
            if b.get('type') in ('array', 'object'): out += slots(b, path)
        return out
    t = s.get('type')
    if t == 'object':
        for k, ps in s.get('properties', {}).items():
            out.append((path + (k,), ps)); out += slots(ps, path + (k,))
    elif t == 'array' and isinstance(s.get('items'), dict):
        out.append((path + (0,), s['items'])); out += slots(s['items'], path + (0,))
    return out

def get(o, p):
    for k in p: o = o[k]
    return o

def setp(o, p, v):
    o = copy.deepcopy(o); cur = o
    for k in p[:-1]:
        if isinstance(cur, dict) and k not in cur: return None
        if isinstance(cur, list) and (not isinstance(k, int) or k >= len(cur)): return None
        cur = cur[k]
    if isinstance(cur, list):
        if not isinstance(p[-1], int) or p[-1] >= len(cur): return None
        cur[p[-1]] = v
    elif isinstance(cur, dict): cur[p[-1]] = v
    else: return None
    return o

def delp(o, p):
    o = copy.deepcopy(o); cur = o
    for k in p[:-1]:
        try: cur = cur[k]
        except Exception: return None
    try: del cur[p[-1]]
    except Exception: return None
    return o

def enum_near(s):
    out = []
    for e in s.get('enum', [])[:3]:
        if isinstance(e, str): out += [e.upper(), " " + e + " ", e + "\n", e + "x", e[:-1], "\t" + e, e.capitalize()]
    return out

def num_edge(s):
    out = []
    for k in ('minimum', 'maximum'):
        if k in s: out += [s[k], s[k] - 1, s[k] + 1, str(s[k]), str(s[k] + 1), str(s[k] - 1), s[k] + 0.5]
    if s.get('type') == 'integer': out += [1.0, 2.5, "2.5", "3.0", 1e3]
    if 'pattern' in s: out += ["bad name!", "-x", "a" * 49, " ", "  ", "A.b_c-9"]
    return out

cases = []; seen = set()
def add(tool, args, tag):
    key = tool + "|" + json.dumps(args, sort_keys=False)
    if key in seen: return
    seen.add(key); cases.append({"id": len(cases), "tool": tool, "tag": tag, "args": json.dumps(args, ensure_ascii=False)})

# ---------------------------------------------------------------- base corpus (ids 0..15921)
for tool in TOOLS:
    s = real[tool]['wire']
    full = sample(s, True); req = sample(s, False)
    add(tool, full, "valid-full"); add(tool, req, "valid-req"); add(tool, {}, "empty")
    sl = slots(s)
    for base, bn in ((full, "full"), (req, "req")):
        for p, ps in sl:
            ps_path = "/".join(map(str, p))
            if bn == "req" and len(p) > 1: continue
            d = delp(base, p)
            if d is not None and bn == "full": add(tool, d, f"delete:{ps_path}")
            for v in POOL + enum_near(ps) + num_edge(ps):
                n = setp(base, p, v)
                if n is not None: add(tool, n, f"set:{bn}:{ps_path}")
        for k, v in (("zz", 1), ("zz", None), ("zz", "null"), ("zz", "x"), ("zz", {"a": 1}), ("__proto__x", 1), ("constructor", "x"), ("i", "doing"), ("\"zz\"", 1)):
            n = copy.deepcopy(base); n[k] = v; add(tool, n, f"extra-root:{bn}")
        for p, ps in sl:
            if ps.get('type') == 'object':
                cur = None
                try: cur = get(base, p)
                except Exception: pass
                if isinstance(cur, dict):
                    for k, v in (("zz", 1), ("zz", None), ("zz", "null"), ("zz", "x")):
                        c2 = dict(cur); c2[k] = v
                        n = setp(base, p, c2)
                        if n is not None: add(tool, n, f"extra-nested:{'/'.join(map(str, p))}")
        for k in list(base.keys()):
            n = {(json.dumps(kk) if kk == k else kk): vv for kk, vv in base.items()}
            add(tool, n, f"dquote-key:{bn}")
        for k, v in base.items():
            if isinstance(v, str):
                for suf in ("\n", "\r\n", " ", "\n\n", "\t"):
                    n = dict(base); n[k] = v + suf; add(tool, n, f"trail:{bn}:{k}")
    for v in (None, "null", ""):
        n = dict(req)
        for k in s.get('properties', {}):
            if k not in s.get('required', []): n[k] = v
        add(tool, n, "all-optional-nullish")
    for v in (None, "null", ""):
        n = dict(full)
        for k in s.get('required', []): n[k] = v
        add(tool, n, "all-required-nullish")
    for alt in ({"_input": "abc"}, {"wrong": "abc"}, {"wrong": "abc", "other": "def"}, {"wrong": 5}, {"wrong": "abc", "n": 5}, {"wrong": ""}):
        add(tool, alt, "single-string-remap")
    for p, ps in sl:
        if len(p) == 1 and ps.get('type') == 'array':
            item = sample(ps['items'], True) if isinstance(ps.get('items'), dict) else 'a'
            n = delp(full, p)
            if isinstance(item, dict):
                for k, v in item.items(): n[f"{p[0]}[0].{k}"] = v
            else: n[f"{p[0]}[0]"] = item
            add(tool, n, "flattened")
            n2 = dict(n); n2[f"{p[0]}[2]"] = item if not isinstance(item, dict) else "x"; add(tool, n2, "flattened-gap")
    for k, v in list(full.items()):
        if isinstance(v, str):
            others = [kk for kk in full if kk != k]
            n = {k: v + "</arg_key>\n" + "".join(f"<arg_key>{kk}</arg_key>\n<arg_value>{full[kk] if isinstance(full[kk], str) else json.dumps(full[kk])}</arg_value>\n" for kk in others)}
            add(tool, n, "spill")
            break
    add(tool, {k: json.dumps(v) if not isinstance(v, str) else v for k, v in full.items()}, "all-stringified")
    add(tool, {k: [v] for k, v in full.items()}, "all-wrapped-array")
BASE = len(cases)

# ---------------------------------------------------------------- extra classes (tag "x:...")
IDENT = ["path", "paths", "file", "file_path", "filePath", "filepath", "url", "uri", "title", "label"]
NUMS = [1e21, 1e20, 1.5e300, 1e-7, 1e-6, 0.000001, -1e-7, -0.0, 0.1, 0.30000000000000004, 123456789012345680000, 9007199254740993,
        100, 1.0, -5, 2.5e-10, 1e100, 123456.789, 5e-324, 1.7976931348623157e308]
def utf16(n, ch): return ch * n
LENS = [utf16(39, "a"), utf16(40, "a"), utf16(41, "a"), utf16(40, "é"), utf16(41, "é"), utf16(20, "😀"), utf16(21, "😀"), utf16(39, "a") + "😀",
        "<a&b>", "a\u2028b", "tab\there", "q\"uote", "back\\slash", utf16(255, "a"), utf16(256, "a"), utf16(257, "a"), utf16(128, "😀"), utf16(129, "😀"), utf16(300, "é")]
for tool in TOOLS:
    s = real[tool]['wire']
    full = sample(s, True); req = sample(s, False)
    props = s.get('properties', {}); required = s.get('required', [])
    sl = slots(s)
    # identifier newline trim: every identifier-named string at any depth, several terminators, plus non-identifier controls
    for p, ps in sl:
        leaf = p[-1] if not isinstance(p[-1], int) else (p[-2] if len(p) > 1 else None)
        cur = None
        try: cur = get(full, p)
        except Exception: continue
        if isinstance(cur, str):
            for suf in ("\n", "\r\n", "\r", "\n\n\n", " \n", "\n ", "\t\n"):
                n = setp(full, p, cur + suf)
                if n is not None: add(tool, n, f"x:ident-trim:{'/'.join(map(str, p))}")
    # number formatting: numbers into every root string field
    for k, ps in props.items():
        if ps.get('type') == 'string' or 'enum' in ps or 'anyOf' in ps:
            for v in NUMS:
                n = dict(req); n[k] = v; add(tool, n, f"x:number-format:{k}")
    # UTF-16 lengths and escaping: into a failing position (pattern/enum) and into a long passing string beside a failure
    for k, ps in props.items():
        if 'enum' in ps or 'pattern' in ps:
            for v in LENS:
                n = dict(req); n[k] = v; add(tool, n, f"x:utf16:{k}")
    strs = [k for k, ps in props.items() if ps.get('type') == 'string' and 'pattern' not in ps and 'enum' not in ps]
    bad = [k for k, ps in props.items() if ps.get('type') in ('number', 'integer', 'boolean')]
    if strs and bad:
        for v in LENS:
            n = dict(req); n[strs[0]] = v; n[bad[0]] = "not-a-value"; add(tool, n, f"x:utf16-received:{strs[0]}")
    # double-encoded keys: each root key at depth 1 and 2, all keys at once, a collision, nested keys
    for base, bn in ((full, "full"), (req, "req")):
        for k in list(base.keys()):
            k2 = json.dumps(json.dumps(k))
            add(tool, {(k2 if kk == k else kk): vv for kk, vv in base.items()}, f"x:dquote2:{bn}")
            n = dict(base); n[json.dumps(k)] = base[k]; add(tool, n, f"x:dquote-collision:{bn}")
        add(tool, {json.dumps(kk): vv for kk, vv in base.items()}, f"x:dquote-all:{bn}")
    for p, ps in sl:
        if ps.get('type') == 'object':
            try: cur = get(full, p)
            except Exception: continue
            if isinstance(cur, dict) and cur:
                n = setp(full, p, {json.dumps(kk): vv for kk, vv in cur.items()})
                if n is not None: add(tool, n, f"x:dquote-nested:{'/'.join(map(str, p))}")
    # flattened arrays: every root array, 1..3 items, with and without the other keys, bad shapes
    for k, ps in props.items():
        if ps.get('type') != 'array': continue
        item = sample(ps['items'], True) if isinstance(ps.get('items'), dict) else 'a'
        for base, bn in ((full, "full"), (req, "req")):
            for count in (1, 2, 3):
                n = {kk: vv for kk, vv in base.items() if kk != k}
                for i in range(count):
                    if isinstance(item, dict):
                        for ik, iv in item.items(): n[f"{k}[{i}].{ik}"] = iv
                    else: n[f"{k}[{i}]"] = item
                add(tool, n, f"x:flattened:{bn}:{k}")
            n = {kk: vv for kk, vv in base.items() if kk != k}; n[f"{k}[1]"] = item if not isinstance(item, dict) else item
            add(tool, n, f"x:flattened-sparse:{bn}:{k}")
            n = dict(base); n[f"{k}[0]"] = item; add(tool, n, f"x:flattened-conflict:{bn}:{k}")
            n = {kk: vv for kk, vv in base.items() if kk != k}; n[f"{k}[x]"] = item; n[f"{k}.0"] = item
            add(tool, n, f"x:flattened-malformed:{bn}:{k}")
    # union fields and array fields: richer value set
    UV = ["a", "", "1", 1, 0, True, None, "null", [], ["a"], ["a", "b"], [1], [1, "a"], [None], [[]], {}, {"a": "b"},
          "[]", "[\"a\"]", "[\"a\", \"b\"]", "[1]", "[\"a\"", "[a]", " [\"a\"] ", "[\"a\"]\n", "[{\"x\":1}]", "{\"a\":1}", "a,b", "a;b", "\"a\""]
    for p, ps in sl:
        if 'anyOf' in ps or isinstance(ps.get('type'), list) or ps.get('type') == 'array':
            for base, bn in ((full, "full"), (req, "req")):
                if bn == "req" and len(p) > 1: continue
                for v in UV:
                    n = setp(base, p, v)
                    if n is not None: add(tool, n, f"x:union-array:{bn}:{'/'.join(map(str, p))}")
    # pairs of simultaneous defects (several issues in one message, order of issues)
    keys = list(props.keys())
    for a in range(len(keys)):
        for b in range(a + 1, min(len(keys), a + 4)):
            for va, vb in (({}, {}), (None, None), ([], 5), ("", "")):
                n = dict(full); n[keys[a]] = va; n[keys[b]] = vb; add(tool, n, "x:two-defects")
            n = {kk: vv for kk, vv in full.items() if kk not in (keys[a], keys[b])}; add(tool, n, "x:two-missing")
    # model-order variants: reversed key order (Received arguments must follow the model's order)
    add(tool, dict(reversed(list(full.items()))), "x:reversed-valid")
    if bad:
        n = dict(reversed(list(full.items()))); n[bad[0]] = "not-a-value"; add(tool, n, "x:reversed-invalid")
with open(out_path, 'w', encoding='utf-8') as f:
    for c in cases: f.write(json.dumps(c, ensure_ascii=False) + "\n")
from collections import Counter
print("base", BASE, "extra", len(cases) - BASE, "total", len(cases))
print(sorted(Counter(c['tag'].split(':')[1] if c['tag'].startswith('x:') else 'base' for c in cases).items()))
