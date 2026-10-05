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

def reason(f, name):
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
