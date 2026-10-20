#!/usr/bin/env python3
"""Regenerates extensions/warden/skipped_twins_test.go: every upstream test case that has no Go twin, as a named
skipped subtest with its reason (nothing is dropped silently). Usage: python3 gen-skipped-twins.py <upstream tests dir>."""
import re, glob, os, sys, collections

tests = sys.argv[1] if len(sys.argv) > 1 else "oracle/tests"
here = os.path.dirname(os.path.abspath(__file__))
ext = os.path.join(here, "..", "extensions", "warden")

twinned = collections.defaultdict(set)
for f in glob.glob(os.path.join(ext, "*_test.go")):
    if f.endswith("skipped_twins_test.go"):
        continue
    s = open(f).read()
    for m in re.finditer(r"tests/([\w-]+\.test\.ts):(\d+)([^\n]*)", s):
        twinned[m.group(1)].add(int(m.group(2)))
        for n in re.findall(r":(\d+)", m.group(3)):
            twinned[m.group(1)].add(int(n))

MODULES = {
    "adaptive": "adaptive steering (steer suppression by past outcomes)", "arming": "arming rules", "ask": "pi-typesafe `ask` (covered by judge.go tests via the shared client)",
    "audit-discovery": "audit and discovery tooling", "backend": "pi-typesafe backend selection (the port uses components/typesafe)", "compact": "compaction evidence",
    "config": "upstream config loader and schema (the port has its own consent-first config)", "conscience-gunner": "conscience guard", "conscience": "conscience guard",
    "credential-shapes": "security guard (credential shapes)", "dedupe": "context saver", "eval-env": "upstream evaluation harness", "eval-score": "upstream evaluation harness",
    "eval-turn": "upstream evaluation harness", "eval-waste": "upstream evaluation harness", "eval-weak": "upstream evaluation harness", "excerpt": "context saver",
    "extension-omp-notice": "Oh My Pi host notice", "extension": "Pi-harness wiring test of upstream extension.ts (the port's wiring is tested by extension_test.go against PiG's events, not by these cases)",
    "filter": "context saver", "git-state": "git-state checks (ported as patterns_test.go twins through the GitRunner seam where listed)", "holds": "hold-feedback database and regret calibration",
    "host-compat": "Oh My Pi / host compatibility", "host-dirs": "upstream state directories", "host-tui": "upstream TUI panel", "index": "upstream package index", "judge-bench": "judge benchmark",
    "judge-cooldown": "judge cooldown after errors", "learning-dirs": "learning store", "learning-driver": "learning store", "learning": "learning store", "loops": "call-waste loops guard",
    "no-pi-import": "upstream packaging check", "notify": "desktop notifications", "output": "security guard (tool output masking)", "panel": "upstream TUI trace panel",
    "prefs-rules": "preferences guard", "prefs": "preferences guard", "prose": "prose (slop) guard", "recall": "context saver recall", "relevance": "relevance compaction", "rules-audit": "rules guard",
    "rules-calibrate": "rules guard", "rules-lint": "rules guard", "rules-log": "rules guard", "rules-replay": "rules guard", "rules": "rules guard (pi-warden.md)", "runaway": "runaway output guard",
    "saver": "context saver", "shell-writes": "shell-write detection (used by the rules guard)", "sqlite-adapter": "learning store", "sql-target": "SQL target classification (loopback/hosted database)",
    "subagent": "subagent supervision", "waste": "call-waste guard",
}


# Per-case reasons for the cases that stay skipped in the configuration, host and backend files (each case says why).
CASES = {
    "config.test.ts": [
        ("the tracker reads a string duration", "needs the arming tracker, which the guard-core lane ports with arming.ts (the config half, ParseDuration, is twinned by TestParseDuration and config.test.ts:417)"),
    ],
    "shape.test.ts": [
        ("the 0.9.0 live crash", "guardCurrentSections guards the extension against a stale shape module in Node's module graph; the Go package is one unit, so a stale shape module cannot exist (the completeConfig half is twinned)"),
    ],
    "host-dirs.test.ts": [
        ("indexDir:", "indexDir belongs to the conscience lane (index-cmd.ts); the port's index directory is under DataDir and is pinned by that lane's tests, not by PI_CODING_AGENT_DIR precedence"),
        ("every path function places", "each lane pins its own path function under DataDir (loops, prefs, holds, rules log, steer stats, index); the config and project paths of this case are twinned by TestLoadConfigReadUserConfigAndSetUserSettingHonourInjectedDirs"),
        ("gated on the marker", "legacyDataNotice migrates data from Pi's agent directory to Oh My Pi's; the port's data has always lived under the PiG config home (D5) and has no legacy location to migrate from"),
        ("the same directory under another spelling", "legacyDataNotice (Pi to Oh My Pi migration): the port has no migration, so there is no spelling of a legacy directory to compare"),
        ("quoting survives a path with a space", "legacyDataNotice (Pi to Oh My Pi migration): the port prints no shell command to run, so there is no quoting to check"),
    ],
    "host-compat.test.ts": [
        ("keep the host's `this` binding", "a JavaScript Proxy binding `this` of the host API; Go method values have no `this` to lose"),
        ("gets the API back unchanged", "adaptHost returns Pi's API unchanged; PiG registers the extension through the SDK directly, so there is no adapter to bypass"),
        ("before_agent_start sees the active skills", "Oh My Pi's before_agent_start lacks systemPromptOptions; PiG's event carries them natively (the conscience lane reads them there)"),
        ("appendSystemPrompt set by the handler becomes", "Oh My Pi wants the system prompt as an array result; PiG takes Pi's result shape (the conscience lane's appendSystemPrompt path)"),
        ("keeps the rest of the handler result", "Oh My Pi's systemPrompt array result merge; PiG takes Pi's result shape, so no merge is needed"),
        ("marked handled for the host", "Oh My Pi reads an input result as { handled }; PiG reads Pi's { action: \"handled\" }"),
        ("waits for both agent_end handlers", "Oh My Pi has no agent_settled event, so the adapter fakes one after agent_end; PiG delivers EventAgentSettled itself (the supervision lane wires it)"),
        ("runs after an agent_end registered first", "the stand-in agent_settled ordering against another extension's agent_end; PiG's own EventAgentSettled has no such ordering to reproduce"),
        ("will continue does not settle", "willContinue handling of the Oh My Pi stand-in; PiG's EventAgentSettled fires once per prompt itself"),
        ("cancelled async work settles after draining", "Oh My Pi async-job snapshot polling for the stand-in settle; PiG exposes no getAsyncJobSnapshot and needs none"),
        ("cancellation during an earlier agent_end handler", "Oh My Pi async-job snapshot taken before later agent_end handlers; no counterpart on PiG"),
        ("cancellation in an earlier extension can drain", "Oh My Pi async-job drain by an earlier extension; no counterpart on PiG"),
        ("empty async snapshot does not settle a continuation", "the grace period of Oh My Pi's stand-in settle; no counterpart on PiG"),
        ("queued and delivering async results delay", "Oh My Pi async delivery state in the stand-in settle; no counterpart on PiG"),
        ("async work watcher does not settle after a later", "Oh My Pi async-work watcher generation counter; no counterpart on PiG"),
        ("followed by a continuation does not settle", "Oh My Pi continuation detection by the stand-in settle; PiG's EventAgentSettled is host-decided"),
        ("checked for idle after agent_end returns", "Oh My Pi idle check by the stand-in settle; PiG's EventAgentSettled is host-decided"),
        ("continuation starting before the deferred idle check", "Oh My Pi deferred idle check; PiG's EventAgentSettled is host-decided"),
        ("rejected agent_end handler leaves host error handling", "Oh My Pi error reporting around the stand-in settle; PiG reports handler errors itself (host_errors_test.go)"),
        ("host timeout advances to settle", "Oh My Pi per-callback timeouts around the stand-in settle; PiG owns handler deadlines"),
        ("command contexts get getSystemPromptOptions", "Oh My Pi command contexts lack getSystemPromptOptions; PiG's command context has it natively"),
    ],
    "backend.test.ts": [
        ("names pass through, junk is refused", "D3: resolves openrouter and commandcode, which the port does not have (typesafe and own-model only); the refusal of junk values is twinned by config.test.ts:307"),
        ("an endpoint object is kept as written", "D3: endpoint objects are not a backend of the port; the loader refuses them (config.test.ts:307 twin)"),
        ("loginStoresKey", "the port has no TypeSafe login store: the key comes from TYPESAFE_API_KEY only (D2)"),
        ("backendName shows", "D3: names registry and endpoint backends; the port names its two backends in describeBackend"),
        ("describeBackend names the label", "D3: its cases are commandcode and an endpoint object; the port's typesafe and own-model lines are pinned by the disclosure tests"),
        ("typesafe returns the original text unchanged", "the port has no disclosureFor host rewriting: Disclosure(backend, target) builds each backend's text (disclosure tests)"),
        ("openrouter replaces the host", "D3: openrouter is not a backend of the port"),
        ("an endpoint object replaces the host", "D3: endpoint objects are not a backend of the port"),
        ("typesafe backend omits the backend field", "judgeOptions builds pi-typesafe's createTypeSafe options; the Go client is components/typesafe, which takes no such options"),
        ("commandcode and openrouter are forwarded", "D3: commandcode and openrouter are not backends of the port"),
        ("reaches createTypeSafe unchanged", "D3: endpoint objects are not a backend of the port; createTypeSafe is pi-typesafe's"),
        ("backend hosts come from pi-typesafe's registry", "pi-typesafe's DECISIONS_BACKENDS registry; the Go client's endpoints live in components/typesafe"),
    ],
    "extension-omp-notice.test.ts": [
        ("notice once, marker written", "the Oh My Pi migration notice (legacy data under Pi's agent directory moved to Oh My Pi's); PiG is not Oh My Pi and the port's data never lived in Pi's directory (D5)"),
    ],
}

def reason(f, name):
    for needle, why in CASES.get(f, []):
        if needle in name:
            return why
    base = f[:-len(".test.ts")]
    n = name.lower()
    if f == "guard.test.ts":
        table = [("findsecrets|syntheticish", "security guard (secret detection) is not part of this port"), ("session scratch", "session scratch (temp-directory rm exemption) is not part of this port"),
                 ("hostpaths|host path|wardenhostpaths|/warden index|index file", "upstream host paths (its own state directories) are not part of this port"), ("pathrules|path rule", "path rules are not part of this port"),
                 ("slop", "slop guard is not part of this port"), ("regret", "hold-feedback regret question is not part of this port"), ("rules.enabled", "rules guard is not part of this port"),
                 ("large output|commandfamily", "large-output steer is not part of this port")]
        for pat, why in table:
            if re.search(pat, n):
                return why
        return "not ported: owner decision needed"
    if f == "shape.test.ts":
        return "config schema migration (completeConfig) is not part of this port: the Go config merges defaults over a JSON file"
    if f == "stuck-evidence.test.ts":
        return "stuck evidence (parsed failures, edit diffs) is off in this port: upstream's documented `stuck.evidence: false`"
    if f == "steer-delivery.test.ts":
        return "Pi host behavior (a steer rides the tool-result request); the port relies on PiG's SendMessage deliverAs=steer, see PORT.md"
    return "module not part of this port: " + MODULES.get(base, "outside the four-guard scope (owner decision needed)")

rows = []
for path in sorted(glob.glob(os.path.join(tests, "*.test.ts"))):
    f = os.path.basename(path)
    for i, line in enumerate(open(path).read().split("\n"), 1):
        m = re.match(r"\s*(?:test|it)\((\"|'|`)(.*?)\1", line)
        if m and i not in twinned[f]:
            rows.append((f, i, m.group(2), reason(f, m.group(2))))

out = ['package warden', '', '// Code generated by port/gen-skipped-twins.py; DO NOT EDIT.', '//',
       '// Every upstream test case (pi-warden a12b2703) without a Go twin, as a named skipped subtest with the reason.',
       '// A case is never dropped silently: it is a twin (a `// twin: tests/<file>:<line>` comment on a Go test) or it is listed here.', '',
       'import "testing"', '', 'var skippedTwins = []struct {', '\tFile   string', '\tLine   int', '\tName   string', '\tReason string', '}{']
for f, i, name, why in rows:
    out.append('\t{%s, %d, %s, %s},' % (json_s := '"%s"' % f, i, '"%s"' % name.replace('\\', '\\\\').replace('"', '\\"'), '"%s"' % why.replace('"', '\\"')))
out += ['}', '', 'func TestSkippedUpstreamTwins(t *testing.T) {', '\tif len(skippedTwins) == 0 {', '\t\tt.Fatal("no skipped twins listed")', '\t}',
        '\tfor _, c := range skippedTwins {', '\t\tt.Run(c.File+":"+itoaInt(c.Line), func(t *testing.T) { t.Skip(c.Name + " -- " + c.Reason) })', '\t}', '}', '']
open(os.path.join(ext, "skipped_twins_test.go"), "w").write("\n".join(out))
import subprocess; subprocess.run(["gofmt", "-w", os.path.join(ext, "skipped_twins_test.go")])
by = collections.Counter(r[3].split(":")[0] if r[3].startswith("module") else r[3] for r in rows)
print(len(rows), "skipped cases")
