#!/usr/bin/env python3
"""Generates logging-cases.json: the inputs the logging probe (logging-probe.test.ts) runs through the original.

Value specs are JSON with markers: {"$bigint": "123"}, {"$error": {"name", "message", "stack"|null}}, {"$undefined": true},
{"$fn": true}, {"$nan": true}, {"$inf": 1|-1}, {"$negzero": true}, {"$id": "a", ...fields} (an object others may point to) and
{"$ref": "a"} (a pointer to it, which may close a cycle). Seeded: the same file every run."""
import json, random, itertools, os

rnd = random.Random(20261020)
here = os.path.dirname(os.path.abspath(__file__))

# ---- sensitive names
words = ["authorization", "Authorization", "AUTHORIZATION_HEADER", "apiKey", "api_key", "api-key", "apikey", "APIKEY", "x-api-key", "X-Api-Key",
         "ANTHROPIC_API_KEY", "secret", "Secret", "clientSecret", "CLIENT_SECRET", "token", "Token", "tokenCount", "accessToken", "refresh_token",
         "password", "PASSWORD", "passwd", "credential", "credentials", "cookie", "Set-Cookie", "privateKey", "private_key", "privatekey", "PRIVATE-KEY",
         "KEY", "key", "keys", "Keys", "OPENROUTER_KEY", "MY_KEY", "my-key", "cacheKey", "sortKeys", "keySet", "keyboard", "monkey", "turnkey",
         "donkeys", "whiskey", "toolName", "command", "path", "target", "origin", "matchedPattern", "resolution", "toolInputPreview", "Content-Type",
         "", "k", "K", "_key", "-key_", "key_", "_keys", "a_key", "A_KEY_B", "aKey", "aKeys", "aKeyb", "aKeyB", "aKey_", "aKey-", "9Key", "9KeyZ",
         "\u212Aey", "to\u212Aen", "\u017Fecret", "pa\u017F\u017Fword", "\u0130key", "\u00e9key", "key\u00e9", "TOKEN\n", "\nkey", "ke y", "key key",
         "tokens", "mytoken", "TOKEN_", "x.token", "a.key.b", "Key", "aKEY", "KEYS", "Keyring", "apiKeys", "api_keys", "api keys", "apikeyz"]
sensitive = [{"name": w} for w in words]
for w in ["Auth", "orization", "api", "private", "key", "secret", "tok", "pass", "cred", "cook"]:
    for sfx in ["", "s", "_", "-", "X", "x", "Key", "KEY"]:
        sensitive.append({"name": w + sfx})
        sensitive.append({"name": sfx + w})

# ---- stringify specs
def obj(*kv):
    return {k: v for k, v in kv}

strs = ["", "a", "plain", "with \"quotes\"", "back\\slash", "new\nline", "tab\t", "\u0000\u001f", "\u007f", "\u2028\u2029", "\u00e9", "\u4e2d\u6587",
        "\U0001F600", "a\U0001F600b", "\ud7ff", "\ue000", "\uffff", "<script>&", "\b\f\r"]
nums = [0, 1, -1, 1.5, 0.1, 1e21, 1e-7, 123456789012345680000, 5e-324, 1.7976931348623157e308, 100, 4294967295, -4294967296, 9007199254740991]
numspecs = nums + [{"$nan": True}, {"$inf": 1}, {"$inf": -1}, {"$negzero": True}]
stringify = []
def add(spec): stringify.append(spec)
for s in strs: add(s); add({"k": s}); add({s: 1} if s else {"": 1}); add([s])
for n in numspecs: add(n); add({"n": n}); add([n, n])
for v in [None, True, False]: add(v); add({"v": v}); add([v])
add({"$undefined": True}); add({"$fn": True}); add([{"$undefined": True}, {"$fn": True}, 1])
add({"a": {"$undefined": True}, "b": {"$fn": True}, "c": 1})
add({"$bigint": "9007199254740993"}); add({"size": {"$bigint": "-5"}}); add({"$bigint": "0"}); add([{"$bigint": "123456789012345678901234567890"}])
for err in [{"name": "Error", "message": "boom", "stack": "Error: boom\n    at somewhere"}, {"name": "TypeError", "message": "bad", "stack": "trace"},
            {"name": "Error", "message": "", "stack": None}, {"name": "RangeError", "message": "m\"q", "stack": "s\u00e9\U0001F600"}]:
    add({"$error": err}); add({"error": {"$error": err}}); add([{"$error": err}, {"$error": err}])
    add({"a": {"$error": err}, "b": {"$error": err}, "token": {"$error": err}})
add({"$id": "n", "name": "root", "self": {"$ref": "n"}})
add({"$id": "p", "kind": "parent", "child": {"kind": "child", "parent": {"$ref": "p"}}})
add({"$id": "s", "id": 1})  # a lone object
shared = {"$id": "sh", "id": 1}
add({"first": shared, "second": {"$ref": "sh"}})
add({"first": shared, "second": [{"$ref": "sh"}, {"$ref": "sh"}]})
add([{"$id": "x", "v": 1}, {"$ref": "x"}, {"$ref": "x"}])
add({"$id": "arr", "list": [{"$ref": "arr"}]})
add({"a": [], "b": []}); add({"a": {}, "b": {}}); add([[], []]); add([{}, {}])
add({"0": "zero", "b": 1, "10": "ten", "2": "two", "a": 2})
add({"headers": {"authorization": "Bearer TEST", "Accept": "x"}, "toolName": "http"})
add({"credentials": {"user": "root", "password": "hunter2"}})
add({"entries": [{"name": "prod", "apiKey": "sk-real"}, {"name": "dev", "token": None}]})
add({"token": None, "apiKey": {"$undefined": True}, "secret": 0, "password": False, "cookie": "", "key": []})
add({"token": {"$error": {"name": "Error", "message": "m", "stack": "s"}}, "x": 1})
add({"Token": {"a": 1}, "keys": [1, 2], "cacheKey": {"deep": {"token": "x"}}, "sortKeys": True})
add({"a": {"b": {"c": {"d": {"e": {"authorization": "x", "ok": 1}}}}}})
add({"list": [[{"token": "t"}, [{"password": "p"}]], "x"]})
add({"__proto__": {"x": 1}, "constructor": "c", "toString": "t"})
add({"unicode": "\u00e9\U0001F600\u4e2d", "key\u00e9": 1, "\U0001F600": {"token": "x"}})
for i in range(120):
    depth = rnd.randint(1, 4)
    def gen(d):
        k = rnd.random()
        if d == 0 or k < 0.3:
            return rnd.choice([rnd.choice(strs), rnd.choice(nums), None, True, False, {"$bigint": str(rnd.randint(-10**20, 10**20))}, {"$undefined": True}, {"$fn": True}])
        if k < 0.55:
            return [gen(d - 1) for _ in range(rnd.randint(0, 3))]
        o = {}
        for _ in range(rnd.randint(0, 4)):
            o[rnd.choice(words + ["a", "b", "c", "1", "2"])] = gen(d - 1)
        return o
    add(gen(depth))

# ---- cap specs (value, width)
cap = []
widths = [0, 1, 2, 3, 5, 10, 40, 1000, 2.5, -1, -3, {"$nan": True}, {"$inf": 1}, 1e21]
cap_values = ["", "a", "abcdef", "a" * 30, "\U0001F600\U0001F600", "x\U0001F600y", "\u00e9\u00e9\u00e9", "\u4e2d\u6587\u4e2d", "a\U0001F600", "\U0001F600a",
              {"command": "b" * 12, "path": "c" * 12, "n": 1, "t": True, "z": None, "u": {"$undefined": True}},
              {"log": {"details": {"command": "d" * 9}}}, {"paths": ["e" * 7, "ok", ["f" * 9, {"g": "h" * 9}]]}, [], {}, [[]], 5, None, True,
              {"$error": {"name": "Error", "message": "m" * 20, "stack": "s" * 20}}, {"e": {"$error": {"name": "Error", "message": "m" * 20, "stack": "s" * 20}}},
              {"0": "z" * 9, "b": "y" * 9, "1": ["x" * 9]}, {"$bigint": "123456789012345678901234567890"}, {"u": "\U0001F600" * 6}]
for v in cap_values:
    for w in widths:
        cap.append({"value": v, "width": w})

# ---- command redaction
secret_names = ["KEY", "TOKEN", "API_KEY", "MY_SECRET", "password", "OUT", "PATH", "apiKey", "X_AUTH_TOKEN", "COOKIE", "monkey", "SORT_KEYS", "k"]
values = ["sk-abc", '"sk abc"', "'sk abc'", "$SECRET", '"$(cat f)"', "`cat f`", 'a"b"c', "a'b'c", "", "${X:-y}", "\u00e9\U0001F600", "$'a\\nb'", '"a\\"b"',
          "a\\ b", "$((1+2))", "~/x", "*", "{a,b}", '"$A$B"', "'a'\"b\"'c'"]
headers = ["Authorization", "X-Api-Key", "Cookie", "Content-Type", "X-Auth-Token", "apiKey", "Proxy-Authorization", "X-Token", "Accept", "x-key", "legalKeys"]
tmpl = [
    "{N}={V} curl https://x", "{N}={V}", "export {N}={V}", "env {N}={V} deploy", "env -i {N}={V} deploy a", "cmd && {N}={V} deploy",
    "cmd; {N}={V} deploy", "cmd | {N}={V} tee f", "bash -c '{N}={V} x'", 'bash -c "{N}={V} x"', "sh -c {N}={V}", "sudo bash -c '{N}={V} deploy'",
    "eval '{N}={V} x'", 'eval "{N}={V}"', "(( {N}={V} ))", "$({N}={V} cmd)", "echo $({N}={V} cmd)", "( {N}={V} cmd )", "{{ {N}={V}; }}", "local {N}={V}",
    "declare -x {N}={V}", "readonly {N}={V}", "{N}={V} {N}={V} cmd", "{N}+={V}", "{N}[0]={V}", "cmd --{N}={V}", "cmd -e {N}={V}", "docker run -e {N}={V} img",
    "if true; then {N}={V} x; fi", "for i in 1; do {N}={V} x; done", "x=1 {N}={V} y=2 cmd", "cat <<EOF\n{N}={V}\nEOF", "cat <<'EOF'\n{N}={V}\nEOF",
    "{N}={V} cat <<EOF\nbody\nEOF", "bash <<EOF -c '{N}={V} x'\nbody\nEOF", "cmd > f {N}={V}", ">f {N}={V} cmd", "xargs -I{{}} sh -c '{N}={V} x'",
    "timeout 5 bash -c '{N}={V} x'", "sudo -u root env {N}={V} cmd", "nice -n 5 {N}={V} cmd", "find . -exec sh -c '{N}={V} x' \\;",
    "bash -ec '{N}={V} d'", "/bin/sh -c '{N}={V}'", "time bash -c '{N}={V} d'", "nohup {N}={V} cmd &", "{N}={V} cmd &", "bash -c 'bash -c \"{N}={V} x\"'",
]
redact = []
for t in tmpl:
    for n in secret_names:
        for v in rnd.sample(values, 5):
            redact.append(t.replace("{N}", n).replace("{V}", v).replace("{{", "{").replace("}}", "}"))
htmpl = ['curl -H "{H}: {V}" https://x', "curl -H '{H}: {V}' https://x", 'curl -H"{H}: {V}" https://x', "curl -H {H}:{V} https://x", 'curl -H "{H}:{V}"',
         'curl -H "{H}  :  {V}"', "curl --header '{H}: {V}'", 'curl -H "{H}: "{V}', 'curl -H Auth"orization: "{V}', 'curl -H "{H}"', "curl -H {H}", 'curl -H "{H}:"',
         'curl -H "{H}: "', "curl -H '{H}:' x", 'bash -c \'curl -H "{H}: {V}" x\'', 'echo "{H}: {V}"', 'grep -n "{H}: {V}" x', 'curl -H "\u00a0{H}: {V}"',
         'curl -H "{H}:\u00a0{V}"', 'curl -H "{H}:\u3000"', 'curl -H "{H}: \u00e9"', '"{H}: {V}"', "'{H}: {V}'", "{H}: {V}", "x {H}:{V} y"]
hvals = ["Bearer sk-abc", "$TOKEN", "$(cat t)", "x", "a b c", "'q'", '\\"q\\"', "\u00e9\U0001F600", ""]
for t in htmpl:
    for h in headers:
        for v in rnd.sample(hvals, 3):
            redact.append(t.replace("{H}", h).replace("{V}", v))
frag = ["KEY=sk-1", "TOKEN='x y'", 'API_KEY="a b"', "export SECRET=z", "curl", "-H", '"Authorization: Bearer q"', "'X-Api-Key: k'", "&&", "||", ";", "|", "&", "(", ")",
        "$(", "`", "bash -c", "sh -c", "eval", "sudo", "env", "xargs", "timeout 5", "nice", "'", '"', "<<EOF", "EOF", "\n", "echo hi", "\u00e9", "\U0001F600", "#c",
        "> out", "2>&1", "{", "}", "$((", "))", "\\", "$'x'", "<(", "--key", "--token=abc", "-e", "-c", "--", "if", "then", "fi", "for", "do", "done", "x=1", "a=$(b)"]
for i in range(1500):
    redact.append(" ".join(rnd.choice(frag) for _ in range(rnd.randint(1, 9))))
for i in range(300):
    redact.append(rnd.choice(["bash -c '", 'sh -c "', "eval '", "sudo bash -c '", "env TOKEN=a bash -c '"]) + " ".join(rnd.choice(frag) for _ in range(rnd.randint(1, 6))) + rnd.choice(["'", '"', "", "' x", " 'y'"]))
redact += ["", " ", "KEY=", "\n", "git status --short", "bash --help", "bash -c", "eval", "ls | wc -l", "echo $HOME $PWD ~", "TOKEN=\u00e9 x", "\u00e9=1 KEY=x y"]
cases_path = os.path.join(here, "cases.json")
if os.path.exists(cases_path):
    base = json.load(open(cases_path))
    redact += [c["command"] for c in base.get("bash", [])][:700]
seen = set(); uniq = []
for c in redact:
    if c not in seen:
        seen.add(c); uniq.append(c)
redact = uniq

# ---- mask specs
mask = [
    {"toolName": "bash", "command": 'KEY="sk-abc" curl https://x', "executedUnit": "TOKEN=sk-def deploy", "matchedPattern": "curl *"},
    {"forwarding": {"requests": [{"command": "KEY=sk-abc deploy"}]}},
    {"command": None}, {"command": 5}, {"command": ["KEY=a x", {"command": "K=b y"}]}, {"executedUnit": {"command": "TOKEN=z q", "other": "TOKEN=z q"}},
    {"a": {"b": {"c": {"executedUnit": "API_KEY=1 x"}}}}, [{"command": "KEY=1 x"}], "KEY=x", {"command": ""},
    {"command": {"$undefined": True}, "x": "KEY=1"}, {"0": {"command": "KEY=a x"}, "z": 1}, {"command": "bash -c 'TOKEN=t d'", "executedUnit": "TOKEN=t d"},
    {"e": {"$error": {"name": "Error", "message": "KEY=a x", "stack": "s"}}, "command": "KEY=a x"},
]

# ---- line specs (the logger end to end)
lines = []
lw = [None, 5, 20, 40, 1000, 0]
ldetails = [
    {"toolName": "bash", "command": 'KEY="sk-secret" curl https://x'}, {"toolName": "bash", "command": "bash -c 'TOKEN=s d'", "executedUnit": "TOKEN=s d"},
    {"decidedBy": {"kind": "authorizer", "name": "m", "reason": "f" * 60, "apiKey": "x"}}, {"headers": {"authorization": "Bearer " + "T" * 50}},
    {"command": "a" * 80}, {"timestamp": "mine", "stream": "mine", "event": "mine", "extension": "mine"}, {"1": "one", "a": "b", "0": "zero"},
    {"e": {"$error": {"name": "Error", "message": "m", "stack": "s" * 30}}}, {"size": {"$bigint": "10"}}, {"$id": "s", "self": {"$ref": "s"}},
    {"u": {"$undefined": True}, "n": None, "list": ["x" * 30, {"token": "t"}]}, {"command": 'echo \u00e9\U0001F600 && KEY=sk-x deploy', "path": "\U0001F600" * 10},
    {"command": "curl -H \"Authorization: Bearer sk\" https://" + "h" * 40}, {}, {"nested": {"command": "K=1 x" , "n": [{"executedUnit": "TOKEN=1 y"}]}},
]
for st in ["debug", "review"]:
    for d in ldetails:
        for w in (lw if st == "review" else [None]):
            lines.append({"stream": st, "event": rnd.choice(["permission.decision", "permission_request.blocked", "e\u00e9"]), "details": d, "width": w})

# ---- presentation
kinds = ["bash", "mcp", "tool", "path", "external_directory", "bash_external_directory", "skill", "skill_read", "forwarded"]
ctxs = [None, "command_substitution", "process_substitution", "subshell"]
def nul(x): return None if x is None else x
payloads = []
for kind in kinds:
    for surface in ["bash", "skill", "read", "external_directory", ""]:
        for value in ["", "rm -rf build", "/etc/hosts"]:
            for ctx in ctxs[:2] if rnd.random() < .5 else ctxs:
                req = {"requester": {"agentName": rnd.choice([None, "scout"]), "forwarded": rnd.choice([True, False]), "sessionId": rnd.choice([None, "child-7", ""])},
                       "surface": surface, "toolName": rnd.choice([None, "bash"]), "invokedToolName": rnd.choice([None, "exec_command", ""]), "value": value,
                       "matchedPattern": rnd.choice([None, "rm *", "", "*"]), "commandContext": ctx, "executedUnit": rnd.choice([None, "aws s3 rm s3://b", ""])}
                ev = [{"label": rnd.choice(["external path", "working directory", "x"]), "text": rnd.choice(["/etc/hosts", "~/.ssh/config", "/repo", ""]),
                       "detail": rnd.choice([None, "/home/me/.ssh/config"])} for _ in range(rnd.randint(0, 4))]
                payloads.append({"kind": kind, "request": req, "evidence": ev, "annotations": [{"source": "judge", "text": "t"}] if rnd.random() < .3 else []})

out = {"sensitive": sensitive, "stringify": stringify, "cap": cap, "redact": redact, "mask": mask, "lines": lines, "payloads": payloads}
json.dump(out, open(os.path.join(here, "logging-cases.json"), "w"), ensure_ascii=False, indent=0)
print({k: len(v) for k, v in out.items()})
