#!/usr/bin/env python3
"""Generates config-system-cases.json, the corpus config-probe.test.ts runs through the original's config functions."""
import base64, json, itertools

files = []
def f(id, raw=None, b=None):
    files.append({"id": id, **({"raw": raw} if raw is not None else {"rawB64": base64.b64encode(b).decode()})})

# --- top-level shapes ---
for i, raw in enumerate(["", " ", "\n", "[]", "[{}]", "null", "true", "0", '"s"', "{}", "{ }", " {} ", "{}\n", "\ufeff{}", "{}{}", "{},", "{\"a\":1}x"]):
    f(f"shape-{i}", raw)

# --- every top-level key with edge values ---
numbers = ["1", "0", "-1", "1.5", "1e3", "1E3", "1e21", "1e22", "9007199254740991", "9007199254740992", "9007199254740993", "1e308",
           "1e309", "5e-324", "-0", "-0.0", "0.5", "2.0", "1.0e0", "100000000000000000000", '"1"', "true", "null", "[]", "{}", "[1]", "12345678901234567890", "-1e21", "-9007199254740991", "-9007199254740992", "9007199254740990.5", "1e-7", "0e0", "-1e-7", "1E+2", "123456789012345678901234567890", "-5", "0.1"]
for key in ["forwardingTimeoutMs", "promptMaxRows", "promptFieldMaxWidth", "reviewLogFieldMaxWidth", "toolInputPreviewMaxLength", "toolTextSummaryMaxLength"]:
    for j, n in enumerate(numbers):
        f(f"num-{key}-{j}", '{"%s":%s}' % (key, n))
for key in ["debugLog", "permissionReviewLog", "yoloMode", "doublePressToConfirm"]:
    for j, v in enumerate(["true", "false", "null", "0", "1", '"true"', '"false"', "[]", "{}", "[true]"]):
        f(f"bool-{key}-{j}", '{"%s":%s}' % (key, v))
for j, v in enumerate(['"https://example.com/s.json"', '""', "null", "1", "[]", '"\\u00e9"', '"\ud83d\ude00"']):
    f(f"schemakey-{j}", '{"$schema":%s}' % v)
lists = ["[]", '["a"]', '["a","a"]', '[""]', '["a",""]', '["a",1]', '["a",null]', "[[]]", '"a"', "{}", "null", '[" "]', '["\\u00e9"]', '["\ud83d\ude00"]',
         '["/x","/y"]', '[1]', '[true]', '[{"length":0}]', '[{"length":1}]', '[[1]]', '[[""]]', '["a",[]]', '[{}]', '[[],[]]', '[{"length":"x"}]', '[{"length":-1}]']
for key in ["piInfrastructureReadPaths", "authorizerChain"]:
    for j, v in enumerate(lists):
        f(f"list-{key}-{j}", '{"%s":%s}' % (key, v))
notifs = ["[]", '["bell"]', '["bell","bell"]', '["osc9","osc777","bell"]', '["BELL"]', '["beep"]', '["bell","beep"]', '["bell",1]', '[null]', '"bell"', "null", "{}", '[""]', '["bell","osc9","beep","x"]']
for j, v in enumerate(notifs):
    f(f"notif-{j}", '{"promptNotifications":%s}' % v)
dk = ['{}', '{"approve":"1"}', '{"approve":"1","deny":"4"}', '{"approve":1}', '{"approve":null}', '{"approve":""}', '{"approveAll":"1"}', '{"approve":"1","approve":"2"}',
      '{"deny":"j"}', '{"approve":"A"}', '[]', 'null', '"1"', '{"approve":"1","approveSession":"2","approveSessionBoth":"3","deny":"4","denyWithReason":"5","extra":"6"}',
      '{"denyWithReason":"r","approve":"y"}', '{"__proto__":"1"}', '{"approve":["1"]}']
for j, v in enumerate(dk):
    f(f"dialogkeys-{j}", '{"permissionDialogKeys":%s}' % v)

# --- unknown keys, order, duplicates ---
for j, raw in enumerate([
    '{"yoloMod":true}', '{"YoloMode":true}', '{"yoloMode":true,"other":1,"another":2}', '{"a":1,"yoloMode":"x"}',
    '{"__proto__":{"yoloMode":true}}', '{"__proto__":1}', '{"constructor":1}', '{"toString":1}', '{"":1}', '{"1":1}',
    '{"yoloMode":true,"yoloMode":false}', '{"yoloMode":false,"yoloMode":true}', '{"yoloMode":true,"yoloMode":"x"}',
    '{"permission":{"read":"allow"},"permission":{"write":"deny"}}', '{"debugLog":true,"permissionReviewLog":false,"yoloMode":true}',
    '{"yoloMode":true,"debugLog":true,"permissionReviewLog":false}',
    '{"shellTools":{},"permission":{},"authorizerChain":[],"piInfrastructureReadPaths":[],"promptNotifications":[],"forwardingTimeoutMs":5,"debugLog":false,"$schema":"s"}',
    '{"permissionDialogKeys":{"deny":"4","approve":"1"},"promptMaxRows":3,"doublePressToConfirm":false}',
    '{"promptNotifications":["bell"],"__proto__":[]}', '{"constructor":{}}', '{"yoloMode":true,"__proto__":{"x":1},"debugLog":true}',
]):
    f(f"order-{j}", raw)

# --- permission map edge cases ---
perm = [
    '{}', '{"*":"allow"}', '{"*":"maybe"}', '{"*":1}', '{"*":null}', '{"*":[]}', '{"*":{}}', '{"*":{"":"allow"}}', '{"*":{"x":"allow"}}',
    '{"bash":{"*":"ask","git *":"allow","npm *":{"action":"deny","reason":"no"}}}', '{"bash":{"x":{"action":"deny"}}}', '{"bash":{"x":{"action":"allow"}}}',
    '{"bash":{"x":{"action":"deny","reason":null}}}', '{"bash":{"x":{"action":"deny","reason":""}}}', '{"bash":{"x":{"action":"deny","extra":1}}}',
    '{"bash":{"x":{"action":"deny","reason":["a"]}}}', '{"bash":{"x":{}}}', '{"bash":{"x":[]}}', '{"bash":{"x":null}}', '{"bash":{"x":1}}',
    '{"read":"allow","write":"deny","edit":"ask","grep":"allow","find":"allow","ls":"allow"}',
    '{"path":"allow","path_read":"deny","path_write":"ask"}', '{"path_read":{"*":"deny","~/dev/*":"allow"}}', '{"path_write":"x"}',
    '{"path_wrote":"allow"}', '{"path_":"allow"}', '{"external_directory_":"allow"}', '{"external_directory_reed":"allow","path_x":"deny"}',
    '{"path_read":"allow","path_reed":"allow"}', '{"path_reed":"maybe"}', '{"my_path_x":"allow"}', '{"pathread":"allow"}', '{"Path_read":"allow"}',
    '{"mcp":"allow","skill":"ask"}', '{"mcp":{"exa:*":"allow","*":"ask"}}', '{"skills":"allow"}', '{"":"allow"}', '{"":"maybe"}', '{"":{"x":"allow"}}',
    '{"a":"allow","":"allow","path_x":"allow"}', '{"1":"allow","0":"deny","b":"ask","a":"allow"}', '{"b":"ask","1":"allow","a":"allow","path":"deny"}',
    '{"zzz":"allow","*":"ask","bash":"deny","mcp":"ask","skill":"deny","external_directory":"ask"}', '{"__proto__":"allow"}', '{"__proto__":{"x":"allow"}}',
    '{"bash":{"__proto__":"allow","x":"deny"}}', '{"bash":{"__proto__":"maybe"}}', '{"constructor":"allow"}', '{"hasOwnProperty":{"*":"deny"}}',
    '{"bash":{"a":"allow","a":"deny"}}', '{"bash":"allow","bash":"deny"}', '{"bash":{"1":"allow","0":"deny","x":"ask"}}',
    '{"bash":{"%s":{"action":"deny","reason":"%s"}}}' % ("p", "r" * 500), '{"bash":{"p":{"action":"deny","reason":"%s"}}}' % ("r" * 501),
    '{"bash":{"p":{"action":"deny","reason":"%s"}}}' % ("\ud83d\ude00" * 250), '{"bash":{"p":{"action":"deny","reason":"%s"}}}' % ("\ud83d\ude00" * 251),
    '{"bash":{"p":{"action":"deny","reason":"%s"},"q":{"action":"deny","reason":"%s"}}}' % ("r" * 501, "s" * 600),
    '{"bash":{"p":{"action":"deny","reason":"%s"},"q":"maybe"}}' % ("r" * 501), '{"bash":{"p":{"action":"deny","reason":"%s"}},"read":"x"}' % ("r" * 501),
    '{"bash":{"p":{"action":"deny","reason":"%s"}},"path_wrote":"allow"}' % ("r" * 501),
    '{"permission":{}}', '[]', '"allow"', 'null', '1',
    '{"__proto__":"maybe"}', '{"bash":{"__proto__":{"action":"allow"}}}', '{"__proto__":{"x":"maybe"}}', '{"__proto__":"allow","read":"deny"}',
    '{"bash":{"a":{"action":"deny","reason":"x"},"b":{"action":"deny","reason":5},"c":"maybe"}}', '{"a":{"b":{"c":1}}}', '{"bash":{"a":{"action":"deny","reason":"x","action":"deny"}}}',
    '{"bash":{"__proto__":{"action":"deny","reason":5}}}', '{"bash":{"__proto__":{"action":"deny","reason":"%s"}}}' % ("r" * 501), '{"read":"allow","bash":{"x":"allow"},"path_read":"x","path_wrote":"y"}',
    '{"path_wrote":"y","path_reed":"x","external_directory_x":"z"}', '{"bash":"x","mcp":{"a":"y"},"skill":3,"path_write":[]}', '{"bash":{"":"x"}}', '{"":"x"}',
    '{"bash":{"a":"allow"},"bash2":{"b":{"action":"deny","reason":"%s"}}}' % ("r" * 501),
]
for j, p in enumerate(perm):
    f(f"perm-{j}", '{"permission":%s}' % p)

shell = [
    '{}', '{"exec_command":{"commandArgument":"cmd"}}', '{"exec_command":{"commandArgument":"cmd","workdirArgument":"workdir"}}',
    '{"exec_command":{"workdirArgument":"workdir"}}', '{"exec_command":{"commandArgument":""}}', '{"exec_command":{"commandArgument":"cmd","workdirArgument":""}}',
    '{"exec_command":{"commandArgument":"cmd","extra":1}}', '{"exec_command":{"commandArgument":1}}', '{"exec_command":{}}', '{"exec_command":[]}', '{"exec_command":null}',
    '{"":{"commandArgument":"cmd"}}', '{"__proto__":{"commandArgument":"cmd"}}', '{"a":{"commandArgument":"x"},"__proto__":{"commandArgument":"y"},"b":{"commandArgument":"z"}}',
    '{"1":{"commandArgument":"x"},"a":{"commandArgument":"y"},"0":{"commandArgument":"z"}}', '[]', 'null', '"x"',
    '{"a":{"commandArgument":"x"},"b":{"workdirArgument":"y","extra":1},"":{}}', '{"a":{"workdirArgument":null,"commandArgument":"c"}}',
    '{"a":{"commandArgument":"c","commandArgument":"d"}}', '{"__proto__":1}', '{"a":{"commandArgument":"c","__proto__":1}}', '{"a":{"commandArgument":[]}}',
    '{"a":{"commandArgument":{"length":0}}}', '{"a":{"commandArgument":[""]}}', '{"a":{"commandArgument":{}}}', '{"a":{"commandArgument":"c","workdirArgument":[]}}',
    '{"a":{"commandArgument":""},"b":{"commandArgument":1}}', '{"":1}', '{"":{"commandArgument":"c"},"a":{}}',
]
for j, s in enumerate(shell):
    f(f"shell-{j}", '{"shellTools":%s}' % s)

# --- seeded fuzz: random configs from a grammar of valid and invalid pieces ---
import random as _r
_rnd = _r.Random(20260101)
def _val(depth=0):
    c = _rnd.random()
    if c < 0.15: return _rnd.choice(["true", "false", "null"])
    if c < 0.3: return _rnd.choice(["0", "1", "-1", "24", "400", "1.5", "1e3", "9007199254740993", '"x"', '""'])
    if c < 0.45: return json.dumps(_rnd.choice(["allow", "deny", "ask", "maybe", "ALLOW", "", "git *", "~/x/*"]))
    if c < 0.6 and depth < 3: return "[" + ",".join(_val(depth + 1) for _ in range(_rnd.randint(0, 3))) + "]"
    if depth < 3: return "{" + ",".join(json.dumps(_rnd.choice(["*", "bash", "read", "path", "path_read", "path_wrt", "external_directory", "mcp", "skill", "a", "", "__proto__", "action", "reason", "approve", "commandArgument"])) + ":" + _val(depth + 1) for _ in range(_rnd.randint(0, 3))) + "}"
    return "1"
_top = ["$schema", "debugLog", "permissionReviewLog", "yoloMode", "doublePressToConfirm", "permissionDialogKeys", "promptNotifications", "forwardingTimeoutMs", "promptMaxRows", "promptFieldMaxWidth", "reviewLogFieldMaxWidth", "toolInputPreviewMaxLength", "toolTextSummaryMaxLength", "piInfrastructureReadPaths", "authorizerChain", "permission", "shellTools", "extra"]
for _i in range(250):
    _keys = _rnd.sample(_top, _rnd.randint(1, 5))
    f(f"fuzz-{_i}", "{" + ",".join(json.dumps(k) + ":" + _val() for k in _keys) + "}")
# fuzz of valid-shaped permission maps (mostly accepted), to compare the schema's output order
_surf = ["*", "path", "path_read", "path_write", "external_directory", "external_directory_read", "external_directory_write", "bash", "mcp", "skill", "read", "write", "edit", "zz", "a1", "1", "0", "ext_tool"]
def _pv():
    if _rnd.random() < 0.4: return json.dumps(_rnd.choice(["allow", "deny", "ask"]))
    return "{" + ",".join(json.dumps(_rnd.choice(["*", "x", "git *", "rm *", "1", "0", "b"])) + ":" + (json.dumps(_rnd.choice(["allow", "deny", "ask"])) if _rnd.random() < 0.7 else '{"action":"deny","reason":"r"}') for _ in range(_rnd.randint(0, 3))) + "}"
for _i in range(150):
    _ks = _rnd.sample(_surf, _rnd.randint(1, 7))
    f(f"permfuzz-{_i}", '{"permission":{' + ",".join(json.dumps(k) + ":" + _pv() for k in _ks) + '}}')

# --- JSONC and text-level edge cases ---
jsonc = [
    '{ // c\n"yoloMode": true }', '{ /* c */ "yoloMode": true }', '{ "yoloMode": true /* c */ }', '/* c */ {}', '// c\n{}', '{} // c', '{} /* c', '{ /* c */ }',
    '{ "a": "//" }', "{ 'a': 1 }", '{ "yoloMode": true, }', '{ "yoloMode": true // c\n, "debugLog": true }', '{ "yoloMode": /* c */ true }',
    '{"permission":{"bash":{"//x":"allow","/*y*/":"deny"}}}', '{"permission":{"bash":{"a\\"//b":"allow"}}}', '{"permission":{"bash":{"a\\\\":"allow"}}} // x',
    '{ "x": "unterminated', '{ "a": \'b }', '{ "a": \'b\' }', '/**/{}', '/*/ x */{}', '{"a":1/2}', '{ "a" : 1 // c', '{\r\n// c\r\n"yoloMode":true\r\n}',
    '\t{\t"yoloMode"\t:\ttrue\t}\t', '{"yoloMode":true}\n\n\n', '\n\n{\n\n"yoloMode"\n:\n true}', '{"yoloMode": tru}', '{"yoloMode": truee}', '{"yoloMode": True}',
    '{"yoloMode": nul}', '{"yoloMode": 01}', '{"yoloMode": 1.}', '{"yoloMode": .5}', '{"yoloMode": +1}', '{"yoloMode": 1e}', '{"yoloMode": 0x10}', '{"yoloMode": -}',
    '{"yoloMode": "a\nb"}', '{"yoloMode": "a\tb"}', '{"yoloMode": "\\x"}', '{"yoloMode": "\\u12"}', '{"yoloMode": "\\uZZZZ"}', '{"yoloMode": "\\', '{"yoloMode": "\\ud800"}',
    '{"$schema": "\\ud800"}', '{"$schema": "\\u00e9\\ud83d\\ude00"}', '{"$schema":"\u2028\u2029"}', '{"yoloMode" true}', '{"yoloMode":}', '{:true}', '{"a" "b"}', '{"a":1 "b":2}',
    '{"a":1,,"b":2}', '[1,]', '[,]', '{,}', '{"a":[1 2]}', '{"a":[1,2}', '{"a":{"b":1]}',
    'NaN', 'undefined', 'Infinity', '[object Object]', '-Infinity', 'nope', 'this is a very long text that goes on and on and on and on, not json',
    '{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb}',
    '{"a":1}\n{"b":2}', '{"a":1} x', 'x {"a":1}', '\u00e9', '{"\u00e9": 1}', '{"yoloMode": \u00e9}', '\ud83d\ude00', '{"yoloMode": \ud83d\ude00}', '{"\ud83d\ude00": x}',
    '{ "yoloMode": true }\u00a0', '\u00a0{}', '\u2028{}', '{\u00a0}', '\u000b{}', '{}\u0000', '\u0000', '{"a":"\u0001"}',
]
for j, raw in enumerate(jsonc):
    f(f"text-{j}", raw)

valid = '{\n  // knobs\n  "debugLog": true,\n  "yoloMode": false,\n  "permission": { "*": "ask", "bash": { "git *": "allow", "rm *": { "action": "deny", "reason": "no" } } },\n  "shellTools": { "x": { "commandArgument": "cmd" } }\n}'
for n in range(1, len(valid), 7):
    f(f"trunc-{n}", valid[:n])
for n in range(0, len(valid), 11):
    f(f"drop-{n}", valid[:n] + valid[n + 1:])

# raw bytes: invalid UTF-8, BOM, overlong, truncated sequences
f("bytes-bom", b=b'\xef\xbb\xbf{}')
f("bytes-bom-config", b=b'\xef\xbb\xbf{"yoloMode":true}')
f("bytes-bad-continuation", b=b'{"$schema":"\xc3("}')
f("bytes-lone-continuation", b=b'{"$schema":"\x80"}')
f("bytes-truncated-3", b=b'{"$schema":"\xe2\x82"}')
f("bytes-truncated-4", b=b'{"$schema":"\xf0\x9f\x98"}')
f("bytes-overlong", b=b'{"$schema":"\xc0\xaf"}')
f("bytes-surrogate-utf8", b=b'{"$schema":"\xed\xa0\x80"}')
f("bytes-fe", b=b'{"$schema":"\xfe\xff"}')
f("bytes-f5", b=b'{"$schema":"\xf5\x80\x80\x80"}')
f("bytes-e0-bad", b=b'{"$schema":"\xe0\x80\x80"}')
f("bytes-f4-bad", b=b'{"$schema":"\xf4\x90\x80\x80"}')
f("bytes-key", b=b'{"\xff":1}')
f("bytes-value-error", b=b'{"yoloMode": \xff}')
f("bytes-nul", b=b'{"yoloMode":true}\x00')
f("bytes-empty", b=b'')
f("bytes-crlf", b=b'{\r\n"yoloMode": x\r\n}')
f("bytes-cr-only", b=b'{\r"yoloMode": x\r}')

# --- loader scenarios ---
loaders = []
def L(id, **kw):
    loaders.append({"id": id, **kw})

g = lambda perm: json.dumps({"permission": perm})
L("empty", agentNames=["coder"])
L("global", **{"global": g({"*": "ask", "read": "allow"})}, agentNames=["coder"])
L("project-trusted", **{"global": g({"*": "ask", "bash": "deny"}), "project": g({"bash": "allow"}), "includeProject": True})
L("project-untrusted", **{"global": g({"*": "ask", "bash": "deny"}), "project": g({"bash": "allow"}), "includeProject": False, "agentNames": ["coder"],
   "projectAgents": {"coder": "---\npermission:\n  bash: allow\n---\n"}})
L("project-default", **{"global": g({"*": "ask", "bash": "deny"}), "project": g({"bash": "allow"})})
L("project-yolo-untrusted", **{"global": '{"yoloMode":false}', "project": '{"yoloMode":true,"debugLog":true}', "includeProject": False})
L("project-yolo-trusted", **{"global": '{"yoloMode":false}', "project": '{"yoloMode":true,"debugLog":true}', "includeProject": True})
L("project-invalid", **{"global": g({"*": "ask"}), "project": '{"permisson":{}}', "includeProject": True, "agentNames": ["a"]})
L("project-invalid-json", **{"global": g({"*": "ask"}), "project": '{"permission":', "includeProject": True})
L("global-invalid", **{"global": '{"permission":[1]}', "project": g({"bash": "allow"}), "includeProject": True})
L("global-invalid-untrusted", **{"global": '{"permission":[1]}', "project": g({"bash": "allow"}), "includeProject": False})
L("both-invalid", **{"global": '{"x":1}', "project": '{"y":1}', "includeProject": True})
L("legacy-global", legacyGlobal='{"defaultPolicy":{"tools":"allow"}}')
L("legacy-global-flat", legacyGlobal=g({"*": "allow", "bash": {"*": "ask"}}))
L("legacy-project-trusted", legacyProject='{"bash":{"x":"allow"}}', includeProject=True)
L("legacy-project-untrusted", legacyProject='{"bash":{"x":"allow"}}', includeProject=False)
L("legacy-ext", legacyExt='{"debugLog":true,"yoloMode":true}')
L("legacy-ext-invalid", legacyExt='{"nope":1}')
L("legacy-all", legacyGlobal=g({"read": "allow"}), legacyProject=g({"write": "deny"}), legacyExt='{"debugLog":true}', **{"global": g({"edit": "ask"}), "project": g({"grep": "deny"})}, includeProject=True)
L("bash-fallback", **{"global": g({"*": "allow"})})
L("bash-fallback-project-fixes", **{"global": g({"*": "allow"}), "project": g({"bash": {"*": "ask"}}), "includeProject": True})
L("bash-fallback-project-untrusted", **{"global": g({"*": "allow"}), "project": g({"bash": {"*": "ask"}}), "includeProject": False})
L("deprecated-caps", **{"global": '{"toolInputPreviewMaxLength":400,"toolTextSummaryMaxLength":9}'})
L("deprecated-caps-project", **{"global": '{"toolInputPreviewMaxLength":400}', "project": '{"toolTextSummaryMaxLength":9}', "includeProject": True})
L("dialog-keys-bad", **{"global": '{"permissionDialogKeys":{"approve":"A","deny":"j","approveSession":"b"}}'})
L("dialog-keys-project-replaces", **{"global": '{"permissionDialogKeys":{"approve":"1","deny":"4"}}', "project": '{"permissionDialogKeys":{"deny":"1"}}', "includeProject": True})
L("shell-merge", **{"global": '{"shellTools":{"a":{"commandArgument":"x"},"b":{"commandArgument":"y"}}}', "project": '{"shellTools":{"b":{"commandArgument":"z"},"c":{"commandArgument":"w"}}}', "includeProject": True})
L("arrays-replace", **{"global": '{"piInfrastructureReadPaths":["/a"],"authorizerChain":["j1"],"promptNotifications":["bell"]}', "project": '{"piInfrastructureReadPaths":[],"promptNotifications":[]}', "includeProject": True})
L("numbers-merge", **{"global": '{"promptMaxRows":24,"forwardingTimeoutMs":1000}', "project": '{"promptMaxRows":10,"reviewLogFieldMaxWidth":5}', "includeProject": True})
L("perm-merge-order", **{"global": g({"bash": {"a": "allow", "b": "ask"}, "read": "allow", "path": "deny"}), "project": g({"bash": {"b": "deny", "c": "allow"}, "path_read": "allow", "write": "ask"}), "includeProject": True})
L("perm-sugar", **{"global": g({"path": {"*": "deny"}, "path_read": {"~/x": "allow"}}), "project": g({"path": {"*": "ask"}}), "includeProject": True})
L("comments", **{"global": '{\n// c\n"permission": {"*":"ask" /* x */}\n}'})
L("bom-global", **{"global": '\ufeff{"yoloMode":true}'})

md = lambda body: "---\n" + body + "\n---\n# Agent\n"
agents = {
    "plain": "# No frontmatter\n",
    "empty-fm": "---\n---\n# x\n",
    "unclosed": "---\npermission:\n  bash: allow\n",
    "bash-allow": md("permission:\n  bash: allow"),
    "bash-map": md('permission:\n  bash:\n    "git *": allow\n    "rm *": deny'),
    "deny-reason": md('permission:\n  bash:\n    "rm *":\n      action: deny\n      reason: "no way"'),
    "deny-reason-bad": md('permission:\n  bash:\n    "rm *":\n      action: allow'),
    "invalid-values": md("permission:\n  bash: maybe\n  read: allow\n  write: nope"),
    "all-invalid": md("permission:\n  bash: maybe"),
    "nested-bad": md("permission:\n  bash:\n    x: nope"),
    "no-permission": md("name: coder\ndescription: d\nmodel: m"),
    "permission-scalar": md("permission: allow"),
    "other-keys": md("name: coder\ndescription: 'Does things'\npermission:\n  '*': ask\n  read: allow\ntools: a, b"),
    "crlf": "---\r\npermission:\r\n  bash: allow\r\n---\r\n# x",
    "tabs": md("permission:\n\tbash: allow"),
    "comments": md("# top\npermission:\n  # inner\n  bash: allow"),
    "quoted-keys": md('permission:\n  "bash": allow\n  \'read\': deny'),
    "dedent": md("permission:\n  bash:\n    x: allow\n  read: deny\nname: n"),
    "proto-key": md("permission:\n  __proto__: allow\n  read: allow"),
    "proto-top": md("__proto__: allow\npermission:\n  read: allow"),
    "dup-keys": md("permission:\n  read: allow\n  read: deny"),
    "dup-permission": md("permission:\n  read: allow\npermission:\n  write: deny"),
    "unicode": md("permission:\n  bash:\n    \"caf\u00e9 *\": allow"),
    "colon-in-value": md("permission:\n  bash:\n    \"git push origin: x\": deny"),
    "colon-in-key": md("permission:\n  mcp:\n    exa:search: allow"),
    "empty-value-key": md("permission:\n  bash:\n  read: allow"),
    "yaml-list": md("permission:\n  bash:\n    - allow"),
    "indent-zero": md("permission:\nbash: allow"),
    "index-keys": md("permission:\n  1: allow\n  0: deny\n  b: ask"),
    "triple-dash-in-body": md("permission:\n  read: allow") + "---\nname: x\n---\n",
    "start-space": " ---\npermission:\n  read: allow\n---\n",
    "double-quote-pair": md("permission:\n  bash: '\"allow\"'"),
    "single-quote-char": md("permission:\n  bash: '"),
    "star-first": md("permission:\n  \"*\": deny\n  bash: allow"),
}
names = list(agents)
L("agents-global", agents=agents, agentNames=names)
L("agents-project", projectAgents=agents, agentNames=names, includeProject=True)
L("agents-both", agents={"a": md("permission:\n  bash: allow"), "b": md("permission:\n  bash: deny")},
  projectAgents={"a": md("permission:\n  bash: deny"), "c": md("permission:\n  read: allow")}, agentNames=["a", "b", "c", "d"], includeProject=True)
L("agents-untrusted", agents={"a": md("permission:\n  bash: allow")}, projectAgents={"a": md("permission:\n  bash: deny")}, agentNames=["a"], includeProject=False)
L("agent-names-odd", agents={"x y": md("permission:\n  read: allow"), "dot.name": md("permission:\n  read: deny")}, agentNames=["x y", "dot.name", "", "../agent", "x"])
L("agent-dir-as-file", agents={}, agentNames=["coder"])

mcps = [
    ("empty", "{}"), ("servers", '{"mcpServers":{"exa":{},"research":{}}}'), ("dash", '{"mcp-servers":{"a-b":{},"c":{}}}'),
    ("both-keys", '{"mcpServers":{"x":{}},"mcp-servers":{"y":{}}}'), ("null-servers", '{"mcpServers":null,"mcp-servers":{"y":{}}}'),
    ("false-servers", '{"mcpServers":false,"mcp-servers":{"y":{}}}'), ("array", '{"mcpServers":["a"]}'), ("string", '{"mcpServers":"a"}'),
    ("trim", '{"mcpServers":{" exa ":{},"  ":{},"":{},"x":{}}}'), ("comments", '{\n// c\n"mcpServers":{"a":{} /* x */}}'),
    ("bad-json", '{"mcpServers":'), ("top-array", '[1]'), ("top-null", 'null'), ("trailing", '{"mcpServers":{"a":{},}}'),
    ("ties", '{"mcpServers":{"bb":{},"aa":{},"Ab":{},"ab":{},"b_":{},"b-":{},"b1":{},"b.":{},"B2":{},"b ":{}}}'),
    ("ties-punct", '{"mcpServers":{"a_":{},"a-":{},"a,":{},"a;":{},"a:":{},"a!":{},"a?":{},"a.":{},"a\'":{},"a\\"":{},"a(":{},"a)":{},"a[":{},"a]":{},"a{":{},"a}":{},"a@":{},"a*":{},"a/":{},"a\\\\":{},"a&":{},"a#":{},"a%":{},"a`":{},"a^":{},"a+":{},"a<":{},"a=":{},"a>":{},"a|":{},"a~":{},"a$":{},"a0":{},"a9":{},"aa":{},"aZ":{}}}'),
    ("case", '{"mcpServers":{"abc":{},"ABC":{},"Abc":{},"aBc":{},"abC":{}}}'),
    ("lengths", '{"mcpServers":{"a":{},"bbb":{},"cc":{},"dddd":{},"ee":{}}}'),
    ("unicode", '{"mcpServers":{"caf\u00e9":{},"cafe":{},"\u00e9":{},"e":{},"f":{}}}'),
    ("astral", '{"mcpServers":{"\ud83d\ude00":{},"ab":{},"abc":{}}}'),
    ("indexkeys", '{"mcpServers":{"b":{},"1":{},"a":{},"0":{}}}'),
    ("proto", '{"mcpServers":{"__proto__":{},"a":{}}}'), ("dups", '{"mcpServers":{"a":{},"a":{}}}'),
]
for n, c in mcps:
    L("mcp-global-" + n, mcpGlobal=c)
    L("mcp-project-" + n, mcpGlobal='{"mcpServers":{"gg":{},"shared":{}}}', mcpProject=c, includeProject=True)
L("mcp-project-untrusted", mcpGlobal='{"mcpServers":{"gg":{}}}', mcpProject='{"mcpServers":{"pp":{}}}', includeProject=False)

yaml = []
def Y(id, m): yaml.append({"id": id, "markdown": m})
for n, c in agents.items():
    Y("agent-" + n, c)
for i, m in enumerate(["", "---", "---\n", "---\n---", "---\n---\n", "---\na: 1\n---", "---\na: 1\n--", "---\na: 1\n---x", "----\na: 1\n---", "---\r\na: 1\r\n---\r\n",
                       "---\na: 1\n\n---\n", "---\n\n---\n", "\ufeff---\na: 1\n---\n", "a: 1", "a:", ":a", "a: ' ", "a: \"b'", "a: 'b\"", "a: ''", "a: \"\"", "a: '\"'",
                       " a: 1", "\ta: 1", "a : 1", "a:1", "a: 1 # c", "# a: 1", "  # a: 1", "'a': 1", '"a": 1', "'a\": 1", "\"a: 1", "a'': 1", "a\u00a0: 1", "\u00a0a: 1",
                       "a: \u00a0", "a:\n  b:\n    c: 1\n  d: 2\ne: 3", "a:\n    b: 1\n  c: 2", "a:\n  b: 1\n a: 2", "a:\n b:\n  c: 1\n   d: 2",
                       "a: 1\r\nb: 2\r\n", "a: 1\rb: 2", "a: 1\n\n\n\nb: 2", "x:y: 1", "a: b: c", "a:: 1", "a: :", ": 1", "a:  \n  b: 1", "a: \"\n", "\u2028a: 1",
                       "a: 1\u2028b: 2", "a: \"x\" ", "a: \"x\" y\"", "constructor: 1", "__proto__:\n  a: 1\nb: 2", "toString: x", "a:\n  __proto__: 1\n  b: 2", "1: a\n0: b\nx: c"]):
    Y(f"text-{i}", m)

dialog = []
def D(id, o): dialog.append({"id": id, "overrides": o})
D("none", {})
for a in ["approve", "approveSession", "approveSessionBoth", "deny", "denyWithReason"]:
    for v in ["1", "y", "s", "b", "n", "r", "j", "k", "A", "", "yy", "+", "/", "\\", "`", "é", " ", "ctrl+g", "escape", "z", "9", "!", "?", "~", "{"]:
        D(f"{a}-{v!r}", {a: v})
import random
rnd = random.Random(7)
pool = ["1", "2", "3", "y", "s", "b", "n", "r", "j", "k", "a", "q", "A", "+", "", "x"]
acts = ["approve", "approveSession", "approveSessionBoth", "deny", "denyWithReason"]
for i in range(300):
    o = {}
    for a in acts:
        if rnd.random() < 0.6:
            o[a] = rnd.choice(pool)
    D(f"rand-{i}", o)

saves = []
def S(id, existing, nxt): saves.append({"id": id, "existing": existing, "next": nxt})
base = {"debugLog": False, "permissionReviewLog": True, "yoloMode": False, "doublePressToConfirm": True}
S("none-existing", None, base)
S("yolo", None, {**base, "yoloMode": True})
S("existing-perm", '{"permission":{"*":"ask","bash":{"git *":"allow"}}}', {**base, "debugLog": True})
S("existing-order", '{"yoloMode":true,"permission":{"read":"allow"},"debugLog":false}', {**base, "yoloMode": False})
S("existing-all-knobs", '{"$schema":"s","debugLog":true,"permissionReviewLog":false,"yoloMode":true,"doublePressToConfirm":false,"permissionDialogKeys":{"deny":"4","approve":"1"},"promptNotifications":["bell"],"forwardingTimeoutMs":5000,"promptMaxRows":10,"promptFieldMaxWidth":50,"reviewLogFieldMaxWidth":70,"toolInputPreviewMaxLength":800,"toolTextSummaryMaxLength":90,"piInfrastructureReadPaths":["/x"],"authorizerChain":["j"],"permission":{"zzz":"allow","bash":{"a":"deny"},"*":"ask"},"shellTools":{"b":{"workdirArgument":"w","commandArgument":"c"}}}', {**base, "yoloMode": True})
S("existing-invalid", '{"nope":1,"permission":{"*":"allow"}}', {**base, "debugLog": True})
S("existing-bad-json", '{"permission":', {**base, "debugLog": True})
S("existing-empty-object", '{}', {**base})
S("existing-comments", '{\n// c\n"permission":{"*":"ask"}}', {**base, "debugLog": True})
S("existing-unicode", '{"permission":{"caf\u00e9":"allow","\ud83d\ude00":{"*":"deny"}}}', base)
S("existing-big-numbers", '{"forwardingTimeoutMs":1e21,"promptMaxRows":9007199254740991}', base)
S("existing-index-keys", '{"permission":{"b":"ask","1":"allow","a":"deny","0":"deny"}}', base)
S("existing-deny-reason", '{"permission":{"bash":{"rm *":{"action":"deny","reason":"no \\"quotes\\" \\n x"}}}}', base)
S("next-extras", None, {**base, "forwardingTimeoutMs": 5, "promptMaxRows": 3, "piInfrastructureReadPaths": ["/z"], "shellTools": {"a": {"commandArgument": "b"}}, "toolInputPreviewMaxLength": 9})
S("next-undefined-knobs", None, {})
S("next-nonbool", None, {"debugLog": "yes", "permissionReviewLog": 0, "yoloMode": 1, "doublePressToConfirm": None})
S("next-partial", None, {"yoloMode": True})

commands = []
rules = [
    {"surface": "read", "pattern": "*", "action": "allow", "layer": "config", "origin": "global"},
    {"surface": "bash", "pattern": "rm *", "action": "deny", "layer": "config", "origin": "project"},
    {"surface": "bash", "pattern": "x", "action": "ask", "layer": "default", "origin": "builtin"},
    {"surface": "edit", "pattern": "a\"b", "action": "allow", "layer": "config"},
    {"surface": "mcp", "pattern": "exa:*", "action": "allow", "layer": "config", "origin": "agent"},
]
args = ["show", "path", "help", "reset", "unknown", "", "  SHOW  ", "Path", "show extra", "HELP", "reset now"]
completions = ["", "s", "S", "pa", "path extra", "zzz", "  ", " p", "h", "r", "show ", "re"]
for cid, cfgd in [("defaults", None), ("yolo", {"yoloMode": True, "debugLog": True, "permissionReviewLog": False})]:
    for ui in [True, False]:
        for rl, rr in [("norules", []), ("rules", rules), ("onlydefault", [rules[2]])]:
            commands.append({"id": f"{cid}-{'ui' if ui else 'noui'}-{rl}", "config": cfgd, "rules": rr, "args": args, "hasUI": ui, "completions": completions})

out = {"files": files, "loaders": [], "yaml": yaml, "dialogKeys": dialog, "saves": saves, "commands": commands}
for l in loaders:
    if l is None: continue
    l = dict(l)
    out["loaders"].append(l)
print(json.dumps(out, indent=0, ensure_ascii=True), file=open("config-system-cases.json", "w"))
print(len(files), len(out["loaders"]), len(yaml), len(dialog), len(saves), len(commands))
