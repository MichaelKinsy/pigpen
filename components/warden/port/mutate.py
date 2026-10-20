#!/usr/bin/env python3
"""Mutation check of the port's key assertions (COMMON-DEFECTS 6). Each mutation breaks one guarantee in the Go
source; the test suite must fail. Usage (from anywhere, with the SDK go.work environment set):
  python3 mutate.py [id ...]     writes mutation-results.txt next to this file."""
import subprocess, sys, os, shutil
ext = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "extensions", "warden")
M = [
 ("consent-bypass", "extension.go", "if cfg.Consent != cfg.Backend {\n\t\tw.noteOnce(ctx, \"consent\"", "if false {\n\t\tw.noteOnce(ctx, \"consent\""),
 ("judge-when-disabled", "extension.go", "if !cfg.Enabled || cfg.Backend == BackendNone {\n\t\treturn nil", "if cfg.Backend == BackendNone {\n\t\treturn nil"),
 ("guard-when-disabled", "toolcall.go", "if !cfg.Enabled || !cfg.Action.Enabled", "if !cfg.Action.Enabled"),
 ("irreversible-never-confirms", "guard.go", "if j.Irreversible >= config.Irreversible.Confirm {", "if j.Irreversible >= config.Irreversible.Confirm+1 {"),
 ("hold-does-not-block", "toolcall.go", '"block": true', '"block": false'),
 ("bearer-not-redacted", "redact.go", '\tout = reBearer.ReplaceAllString(out, "${1}"+redacted)\n', ''),
 ("config-world-readable", "configfile.go", "0o600); err != nil", "0o644); err != nil"),
 ("budget-off-by-one", "backend.go", "b.used >= b.Max", "b.used > b.Max"),
 ("steer-budget-off-by-one", "steer_core.go", "w.steersThisRun >= cfg.SteerBudget", "w.steersThisRun > cfg.SteerBudget"),
 ("repeat-window-empty", "guard.go", "window: 3}", "window: 0}"),
 ("stale-prejudgment-reused", "action_guard.go", "ready.key == key &&", "true &&"),
 ("force-push-flag-f-missed", "patterns.go", "(?:-f|--force)(?:[^-\\w]|$)", "(?:--force)(?:[^-\\w]|$)"),
 ("dangerous-rm-authorizable", "authorize.go", 'neverAuthorized  = setOf("rm-recursive-dangerous-target")', 'neverAuthorized  = setOf("none")'),
 ("deny-rule-only-confirms", "guard.go", "\t\t\tlevel = LevelDeny\n\t\t\treasons = append(reasons, orDefault(hit.Message, hit.Label))", "\t\t\tlevel = LevelConfirm\n\t\t\treasons = append(reasons, orDefault(hit.Message, hit.Label))"),
 ("disable-keeps-consent", "commands.go", 'cfg.Enabled, cfg.Consent = false, ""', "cfg.Enabled = false"),
 ("prejudge-outlives-nothing", "action_guard.go", "bg := context.WithoutCancel(ctx)", "bg := ctx"),
 ("branch-error-not-reported", "steer.go", 'w.noteOnce(ctx, "branch", "warden could not read the session ("+err.Error()+"): judging with your latest prompt only.")', "_ = err"),
 ("quick-repeat-fires-on-success-without-readonly", "stuck.go", "if !l.Failed && !l.ReadOnly {", "if false {"),
 ("quick-repeat-ignores-changes", "stuck.go", "\t\tif a.Changes {\n\t\t\treturn nil\n\t\t}", "\t\tif false && a.Changes {\n\t\t\treturn nil\n\t\t}"),
 ("quick-repeat-once-per-key", "stuck.go", "w.quickRepeated[l.Key] = true\n\treturn &QuickRepeat", "\treturn &QuickRepeat"),
 ("differing-output-still-repeat", "stuck.go", "if prev.OutputKey != l.OutputKey || prev.Failed != l.Failed {", "if prev.Failed != l.Failed {"),
 ("steer-hidden-flag-ignored", "extension.go", 'cfg.SteerVisible = truthy(v)', '_ = v'),
 ("steer-type-renamed", "extension.go", '"pi-warden-steer"', '"pigpen-warden-steer"'),
 ("tmp-file-left-behind", "configfile.go", "\t\t_ = os.Remove(tmp)\n", ""),
 # Added by the review (rev-pigpen-warden).
 ("stuck-task-not-redacted", "stuck.go", "taskText = headText(Redact(t), 1500)", "taskText = headText(t, 1500)"),
 ("done-task-not-redacted", "done.go", "\t\tt = Redact(t)\n", ""),
 ("excerpt-not-redacted", "describe.go", "s.Excerpt = Redact(sample(content, excerptLimit))", "s.Excerpt = sample(content, excerptLimit)"),
 ("context-not-redacted", "guard.go", '"text": truncate(Redact(m.Text), 750)', '"text": truncate(m.Text, 750)'),
 ("own-model-unbudgeted", "backend.go", 'Label: "The session model", Budget: budget}', 'Label: "The session model"}'),
 ("typesafe-retries", "backend.go", "none := 0", "none := 2"),
 ("env-consent-persisted", "extension.go", "\tfile := w.fileCfg\n", "\tfile := w.cfg\n"),
 ("status-claims-unready-judge", "steer.go", "w.statusBackend(cfg), cfg.Mode", "describeBackendShort(cfg.Backend), cfg.Mode"),
 # Steering and trace core (steer_core.go).
 ("steer-critical-set-narrowed", "steer_core.go", "SteerGuardStuck: true, SteerGuardDone: true, SteerGuardRunaway: true, SteerGuardSubagent: true", "SteerGuardStuck: true, SteerGuardDone: true, SteerGuardRunaway: true"),
 ("steer-critical-is-any-guard", "steer_core.go", "\t\tif !criticalSteerGuards[g] {\n\t\t\tcritical = false", "\t\tif criticalSteerGuards[g] {\n\t\t\tcritical = false"),
 ("steer-repeat-delivers", "steer_core.go", "(!over && !w.repeats.Seen(content))", "(!over && !w.repeats.Seen(content) || true)"),
 ("steer-over-budget-records-fingerprint", "steer_core.go", "deliver := critical || (!over && !w.repeats.Seen(content))", "seen := w.repeats.Seen(content)\n\tdeliver := critical || (!over && !seen)"),
 ("steer-guard-not-counted", "steer_core.go", "\t\tw.stats.SteerGuards[g]++\n", ""),
 ("steer-skip-not-counted", "steer_core.go", "w.stats.SteersSkipped++\n\t\tw.mu.Unlock()\n\t\treturn false", "w.mu.Unlock()\n\t\treturn false"),
 ("steer-trigger-no-continuation", "steer_core.go", "if opt.TriggerTurn {\n\t\tw.continuation = true", "if false {\n\t\tw.continuation = true"),
 ("steer-display-override-ignored", "steer_core.go", "display = *opt.Display", "_ = opt.Display"),
 ("budget-available-allows-spent", "steer_core.go", "w.steersThisRun < cfg.SteerBudget", "w.steersThisRun <= cfg.SteerBudget"),
 ("record-keeps-old-guard-line", "steer_core.go", "w.widget = append(w.widget[:i:i], w.widget[i+1:]...)\n\t\t\tbreak", "_ = i\n\t\t\tbreak"),
 ("trace-limit-unbounded", "steer_core.go", "if len(t.items) > t.limit {", "if false {"),
 ("trace-amend-dropped-entry", "steer_core.go", "\t\t\te.Details = append(e.Details, note)\n", ""),
 ("adaptive-trace-only-sent", "steer_core.go", "if w.adaptor.request(model, kind, cfg.Steers) != SteerTraceOnly {", "if true {"),
 ("adaptive-muted-not-counted", "steer_core.go", "w.bump(func(s *stats) { s.SteersMuted++ })", ""),
 ("note-error-budget-flag-lost", "steer_core.go", "w.budgetExhausted = true", "_ = code"),
 # Tools slice: loops, prefs, lessons, recall, the compaction appendix (tools_wiring.go, loops.go, prefs*.go, compact.go).
 ("loops-open-cap-off-by-one", "loops.go", "len(openLoops(store)) >= maxOpenLoops", "len(openLoops(store)) > maxOpenLoops"),
 ("loops-drop-without-reason-allowed", "loops.go", 'if action == "drop" && reason == "" {', "if false {"),
 ("loops-text-not-redacted", "loops.go", "func oneLine(text string) string { return Redact(jsTrim(jsCollapseSpace(text))) }", "func oneLine(text string) string { return jsTrim(jsCollapseSpace(text)) }"),
 ("loops-session-id-escapes-folder", "loops.go", 'reUnsafeSessionChar.ReplaceAllString(session, "_")', "session"),
 ("loops-reminder-repeats", "tools_wiring.go", "same := fingerprint == w.tools.loopsReminded", "same := false"),
 ("loops-reminder-starts-a-turn", "tools_wiring.go", 'SteerOptions{DeliverAs: "nextTurn"}', 'SteerOptions{DeliverAs: "followUp", TriggerTurn: true}'),
 ("loops-reminder-not-remembered", "tools_wiring.go", "w.tools.loopsReminded = fingerprint", "_ = fingerprint"),
 ("prefs-inject-flag-ignored", "tools_wiring.go", "cfg.Prefs.Enabled && cfg.Prefs.Inject && len(", "cfg.Prefs.Enabled && len("),
 ("prefs-injected-again-on-resume", "tools_wiring.go", "len(w.customMessages(ctx, prefsType)) == 0", "true"),
 ("prefs-weakening-injected", "prefs.go", "case pref.Kind == classWeakens:", "case false:"),
 ("prefs-one-session-injected", "prefs.go", "case pref.Sessions < injectSessions:", "case pref.Sessions < 1:"),
 ("prefs-stale-injected", "prefs.go", "case pref.LastAt < current:", "case false:"),
 ("lesson-unconfirmed-injected", "prefs.go", "case !confirmed:", "case false:"),
 ("lesson-forgotten-recorded-again", "prefs.go", "if isForgotten(in.Store, [][]string{words}, rare) {", "if false {"),
 ("remember-ignores-prefs-off", "tools_wiring.go", 'if !cfg.Enabled || !cfg.Prefs.Enabled {\n\t\toff := "enabled"\n\t\tif cfg.Enabled {\n\t\t\toff = "prefs.enabled"\n\t\t}\n\t\treturn toolReply(', 'if !cfg.Enabled {\n\t\toff := "enabled"\n\t\tif cfg.Enabled {\n\t\t\toff = "prefs.enabled"\n\t\t}\n\t\treturn toolReply('),
 ("forget-not-persisted", "tools_wiring.go", "return struct{}{}, &next", "_ = next\n\t\t\treturn struct{}{}, nil"),
 ("scan-reads-the-current-session", "prefs_scan.go", '|| name == exclude {', '|| false {'),
 ("compact-appendix-flag-ignored", "tools_wiring.go", "!cfg.Enabled || !cfg.Context.CompactAppendix", "!cfg.Enabled"),
 ("compact-saved-outputs-uncapped", "compact.go", "savedKeepWhenOver    = 3", "savedKeepWhenOver    = 8"),
 ("recall-lists-checks", "compact.go", 'sec.name == "failed" || sec.name == "verification" || sec.name == "saved"', 'sec.name == "failed" || sec.name == "verification" || sec.name == "saved" || sec.name == "checks"'),
 ("compact-error-line-first-not-last", "compact.go", "for i := len(lines) - 1; i >= 0; i-- {\n\t\tif reErrorLine", "for i := 0; i < len(lines); i++ {\n\t\tif reErrorLine"),
]
EQUIVALENT = {"judge-when-disabled": "unreachable: toolcall.go returns before judgeFor when warden is off; the check stays as defence in depth"}
only = set(sys.argv[1:])
out = []
for mid, f, old, new in M:
    if only and mid not in only: continue
    p = os.path.join(ext, f); src = open(p).read()
    if src.count(old) != 1:
        out.append(f"{mid}: SKIPPED (pattern matches {src.count(old)}x in {f})"); print(out[-1]); continue
    shutil.copy(p, p + ".orig")
    try:
        open(p, "w").write(src.replace(old, new))
        r = subprocess.run(["go", "test", "-count=1", "-failfast", "."], cwd=ext, capture_output=True, text=True, timeout=600)
        killed = r.returncode != 0
        first = next((l for l in (r.stdout + r.stderr).splitlines() if l.startswith("--- FAIL") or "build failed" in l or "[setup failed]" in l), "")
        out.append(f"{mid}: {'KILLED' if killed else 'SURVIVED (equivalent mutant: ' + EQUIVALENT[mid] + ')' if mid in EQUIVALENT else 'SURVIVED'} {first}".rstrip())
    finally:
        shutil.move(p + ".orig", p)
    print(out[-1], flush=True)
open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "mutation-results.txt"), "w").write("\n".join(out) + "\n")
