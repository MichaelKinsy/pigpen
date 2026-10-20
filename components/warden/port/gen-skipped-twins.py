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

# Cases of the rules lane that cannot be Go twins, with the per-case reason (file, line).
RULES_CASES = {
    ("rules.test.ts", 427): "reads examples/pi-warden.md, examples/pi-warden.json and examples/config.json: they ship in the npm package but are not vendored in port/oracle, and the project and user config overrides are the config loader's, not the rules guard's",
    ("shell-writes.test.ts", 162): "describeAction and buildRequest are guard.ts (the guard lane); shellWrites and mergeWrites are ported for describeAction's `writes` and `excerpt`",
    ("extension.test.ts", 286): "wiring case: the first-run notice part is TestRulesABoundRulesFilesEntryKeepsTheFirstRunNoticeAway; the escalation request carrying the rules content is the guard lane's",
    ("extension.test.ts", 643): "wiring case: TestRulesPastTheCapOneNoticePerSession",
    ("extension.test.ts", 665): "wiring case: TestRulesPastTheCapOneNoticePerSession (the notices-off half)",
    ("extension.test.ts", 1771): "wiring case: TestRulesAHeredocWriteInBashIsJudgedAsAWriteAndASkippedFormIsTraced",
    ("extension.test.ts", 1807): "wiring case: TestRulesAppendsToOneFileAreOneRequestAndPastFiveFilesTheRestAreSkipped",
    ("extension.test.ts", 1834): "wiring case: TestRulesADeclinedWriteGetsNoSteerAndTheApprovedRetryDoes",
    ("extension.test.ts", 1860): "wiring case: TestRulesADeclinedWriteGetsNoSteerAndTheApprovedRetryDoes (the confirm dialog half)",
    ("extension.test.ts", 2421): "wiring case: TestInitRefusesWithoutForceAndAsksTheAgentWithIt",
    ("extension.test.ts", 2439): "wiring case: TestInitRefusesWithoutForceAndAsksTheAgentWithIt",
    ("extension.test.ts", 2538): "wiring case: TestRulesCommandListsTheRulesAndCheckNamesWhatNeedsAttention and TestRulesCheckWithoutConsentSendsNothing",
    ("extension.test.ts", 2586): "wiring case: TestRulesAuditNeedsYesHeadlessAndWritesTheMarkdownCopy",
    ("extension.test.ts", 4851): "wiring case: TestRulesCalibrateDeclineSendsNothing",
    ("extension.test.ts", 4870): "wiring case: TestRulesCalibrateYesSendsRespectsTheCapAndRecords",
    ("extension.test.ts", 4895): "wiring case: TestRulesTuneWithNothingFlaggedAsksNothing",
    ("extension.test.ts", 4904): "wiring case: TestRulesCommandListsTheRulesAndCheckNamesWhatNeedsAttention (check, then tune sends one prompt)",
    ("extension.test.ts", 4984): "wiring case: TestRulesTurnRulesSteerOnceAtTheEndOfTheRun",
    ("extension.test.ts", 5018): "wiring case: TestRulesTurnRulesAskNothingWithoutTurnRulesOrARepository",
    ("extension.test.ts", 5032): "wiring case: TestRulesTurnRulesAskNothingWithoutTurnRulesOrARepository",
    ("extension.test.ts", 5047): "wiring case: TestRulesTurnRulesPassJudgesAtMostFiveUnseenFiles",
    ("extension.test.ts", 5073): "wiring case: TestRulesAgentStartDoesNotWaitForTheSnapshot",
}

def reason(f, name, line=0):
    if (f, line) in RULES_CASES:
        return RULES_CASES[(f, line)]
    if f == "rules-replay.test.ts":
        return "scripts/rules-replay.mjs (a repository maintainer script, npm run eval:*) is absent from the published package files (dist, extensions, examples) and not vendored; case: " + name
    base = f[:-len(".test.ts")]
    n = name.lower()
    if f == "guard.test.ts":
        table = [("findsecrets|syntheticish", "security guard (secret detection) is not part of this port"), ("session scratch", "session scratch (temp-directory rm exemption) is not part of this port"),
                 ("hostpaths|host path|wardenhostpaths|/warden index|index file", "upstream host paths (its own state directories) are not part of this port"), ("pathrules|path rule", "path rules are not part of this port"),
                 ("slop", "slop guard is not part of this port"), ("regret", "hold-feedback regret question is not part of this port"), ("rules.enabled", "the rules content of the action request is the guard lane's (guard.ts evaluateAction); resolveRulesFile is ported in rules_file.go"),
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
            rows.append((f, i, m.group(2), reason(f, m.group(2), i)))

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
