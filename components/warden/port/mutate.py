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
 ("config-world-readable", "atomic.go", "os.Chmod(tmp, 0o600)", "os.Chmod(tmp, 0o644)"),
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
 ("tmp-file-left-behind", "atomic.go", "if err := os.Rename(tmp, path); err != nil {\n\t\t_ = os.Remove(tmp)\n", "if err := os.Rename(tmp, path); err != nil {\n"),
 # Added by the review (rev-pigpen-warden).
 ("stuck-task-not-redacted", "stuck.go", "taskText = headText(Redact(t), 1500)", "taskText = headText(t, 1500)"),
 ("done-task-not-redacted", "done.go", "\t\tt = Redact(t)\n", ""),
 ("excerpt-not-redacted", "describe.go", "s.Excerpt = Redact(sample(content, excerptLimit))", "s.Excerpt = sample(content, excerptLimit)"),
 ("context-not-redacted", "guard.go", '"text": truncate(Redact(m.Text), 750)', '"text": truncate(m.Text, 750)'),
 ("own-model-unbudgeted", "backend.go", 'Label: "The session model", Budget: budget}', 'Label: "The session model"}'),
 ("typesafe-retries", "backend.go", "none := 0", "none := 2"),
 ("env-consent-persisted", "extension.go", "\tbefore, file := w.fileCfg, w.fileCfg\n", "\tbefore, file := w.fileCfg, w.cfg\n"),
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

 # Configuration, trace file, shape, widget and panel slice (configui lane).
 ('cfg-project-weaker-switch-taken', 'config_load.go', 'if base && v == false {', 'if false && v == false {'),
 ('cfg-project-threshold-raised', 'config_load.go', '\tif parsed <= base {\n\t\treturn parsed\n\t}', '\tif true {\n\t\treturn parsed\n\t}'),
 ('cfg-project-declares-rules', 'config_load.go', '\tif !project {\n\t\tout.CommandRules =', '\tif true {\n\t\tout.CommandRules ='),
 ('cfg-project-unmasks-output', 'config_load.go', '\tif project {\n\t\tout.Enabled = projectSwitch("security.enabled"', '\tif false {\n\t\tout.Enabled = projectSwitch("security.enabled"'),
 ('cfg-project-removes-tools', 'config_load.go', 'tools = append([]string{}, base.Tools...)', 'tools = listed; _ = base.Tools'),
 ('cfg-project-fails-open', 'config_load.go', 'if project && failOpen && !base.FailOpen {', 'if false && failOpen && !base.FailOpen {'),
 ('cfg-block-is-not-deny', 'config_load.go', '\tcase sev == "block":\n\t\tseverity = "deny"', '\tcase sev == "block":\n\t\tseverity = "block"'),
 ('cfg-unknown-deny-severity-weaker', 'config_load.go', '\t\tif defaultSeverity == "deny" {\n\t\t\tseverity = "deny"\n\t\t} else {', '\t\tif false {\n\t\t\tseverity = "deny"\n\t\t} else {'),
 ('cfg-unknown-path-action-notes', 'config_load.go', '\t\taction = "confirm"\n\t\t*warnings = append(*warnings, unknownRuleValue("path rule"', '\t\taction = "note"\n\t\t*warnings = append(*warnings, unknownRuleValue("path rule"'),
 ('cfg-arming-without-action-kept', 'config_load.go', 'set action to confirm, hold, or block`)\n\t\treturn ArmingRule{}, false', 'set action to confirm, hold, or block`)\n\t\taction := "confirm"; _ = action'),
 ('cfg-arming-bare-number-silent', 'config_load.go', 'ok && reBareNumber.MatchString(jsTrim(s)) {', 'ok && false && reBareNumber.MatchString(jsTrim(s)) {'),
 ('cfg-duration-unitless-is-seconds', 'config_load.go', 'factor := 60_000.0', 'factor := 1000.0'),
 ('cfg-runaway-one-is-a-repeat', 'config_load.go', 'Repeats:         max(2, positiveInteger(raw["repeats"]', 'Repeats:         max(1, positiveInteger(raw["repeats"]'),
 ('cfg-judge-cooldown-uncapped', 'config_load.go', 'cooldown = int(math.Min(f, maxJudgeCooldownMs))', 'cooldown = int(f)'),
 ('cfg-conscience-timeout-floor', 'config_load.go', 'clampedInt(raw["timeoutMs"], base.TimeoutMs, 100, 10000)', 'clampedInt(raw["timeoutMs"], base.TimeoutMs, 0, 10000)'),
 ('cfg-retention-zero-rejected', 'config_load.go', 'RetentionDays: nonNegativeInteger(raw["retentionDays"]', 'RetentionDays: positiveInteger(raw["retentionDays"]'),
 ('cfg-offtask-confirm-alias-lost', 'config_load.go', 'coalesce(m["steer"], m["confirm"])', 'm["steer"]'),
 ('cfg-should-proceed-hold-lost', 'config_load.go', 'coalesce(shouldProceed["threshold"], shouldProceed["hold"])', 'shouldProceed["threshold"]'),
 ('cfg-slop-placeholder-lost', 'config_load.go', 'coalesce(raw["threshold"], raw["placeholder"])', 'raw["threshold"]'),
 ('cfg-legacy-action-timeout-lost', 'config_load.go', 'coalesce(raw["timeoutMs"], legacy["timeoutMs"])', 'raw["timeoutMs"]'),
 ('cfg-project-names-notify-command', 'config_load.go', 'applyNotify(base.Notify, raw["notify"], !project)', 'applyNotify(base.Notify, raw["notify"], true)'),
 ('cfg-project-enables-compaction', 'config_load.go', '\tif !project {\n\t\tenabled = boolean(raw["enabled"], base.Enabled)', '\tif true {\n\t\tenabled = boolean(raw["enabled"], base.Enabled)'),
 ('cfg-project-enables-filter', 'config_load.go', '\t\tif !project {\n\t\t\tout.Filter.Enabled', '\t\tif true {\n\t\t\tout.Filter.Enabled'),
 ('cfg-bad-backend-kept', 'config_load.go', 'out.Backend, out.BackendRefusal = BackendNone, backendRefusalFor(v)', 'out.Backend, out.BackendRefusal = base.Backend, backendRefusalFor(v)'),
 ('cfg-project-turns-warden-on', 'config_load.go', 'out.Enabled = projectEnabled(raw["enabled"], base.Enabled, &warnings)', 'out.Enabled = boolean(raw["enabled"], base.Enabled)'),
 ('cfg-panel-width-below-floor', 'config_load.go', 'isSafeInteger(f) && f >= 20 {', 'isSafeInteger(f) && f >= 0 {'),
 ('cfg-file-not-reloaded', 'config_runtime.go', 'if w.cfgPath != "" && w.configSig() != w.cfgSig {', 'if false && w.configSig() != w.cfgSig {'),
 ('cfg-untrusted-project-read', 'config_runtime.go', 'if w.projectTrusted && w.projectPath != "" {\n\t\tproject, perr', 'if w.projectPath != "" {\n\t\tproject, perr'),
 ('cfg-command-rewrites-whole-file', 'configfile.go', 'if !had || !jsonEqual(a, b) {', 'if !had || !jsonEqual(a, b) || true {'),
 ('cfg-nested-set-mutates-input', 'config_load.go', '\tresult := spreadObject(obj)\n', '\tresult := obj\n'),
 ('trace-file-ids-restart', 'trace_file.go', '\t\tf.base = base\n', '\t\tf.base = 0; _ = base\n'),
 ('trace-file-world-readable', 'trace_file.go', 'os.O_WRONLY, 0o600)', 'os.O_WRONLY, 0o644)'),
 ('trace-dir-world-readable', 'trace_file.go', 'os.MkdirAll(dir, 0o700)', 'os.MkdirAll(dir, 0o755)'),
 ('trace-file-failure-repeats', 'trace_file.go', '\tif stopped {\n\t\treturn\n\t}\n\tif err := write()', '\tif stopped && false {\n\t\treturn\n\t}\n\tif err := write()'),
 ('trace-judgments-line-repeats', 'trace_file.go', 'state == f.judgments {', 'false {'),
 ('trace-dir-relative-accepted', 'trace_file.go', 'if dir != "" && filepath.IsAbs(dir) {', 'if dir != "" {'),
 ('shape-security-missing-stays-on', 'shape.go', '\tif isUnset(c.Security) {', '\tif false {'),
 ('shape-widget-default-line-lost', 'shape.go', '\tif c.Widget.Runaway == "" {', '\tif false {'),
 ('widget-quiet-verdicts-not-folded', 'widget_render.go', '\t\tcase QuietColors[color]:', '\t\tcase QuietColors[color] && false:'),
 ('widget-worst-verdict-first', 'widget_render.go', 'severity(loud[j-1].status) > severity(loud[j].status)', 'severity(loud[j-1].status) < severity(loud[j].status)'),
 ('widget-rail-never-overflows', 'widget_render.go', 'const railMax = 10', 'const railMax = 100'),
 ('widget-slop-none-is-a-finding', 'widget_render.go', 'return loc != nil && !reNoneWord.MatchString', 'return loc != nil && reNoneWord.MatchString'),
 ('panel-config-q-closes-while-editing', 'panel.go', 'data == "q" && !p.isEditing()', 'data == "q"'),
 ('panel-escape-never-closes', 'panel.go', 'func (a panelActions) release() bool { return a.unfocus != nil && a.unfocus() }', 'func (a panelActions) release() bool { return true }'),
 ('shortcut-shape-unchecked', 'trace_session.go', '+[a-z0-9]+$|^f', '+.*$|^f'),
 ('config-set-value-not-coerced', 'config_cmd.go', 'value := ParseConfigValue(rawValue)', 'var value any = rawValue'),
 ('config-get-ignores-null', 'config_cmd.go', 'if value == nil && !hasNestedKey(', 'if value == nil || !hasNestedKey('),
 ('cfg-stuck-repeatsteer-ignored', 'config_load.go', '\t\tRepeatSteer:    boolean(raw["repeatSteer"], base.RepeatSteer),', '\t\tRepeatSteer:    base.RepeatSteer,'),
 ('cfg-stuck-nudge-ignored', 'config_load.go', '\t\tNudge:          boolean(raw["nudge"], base.Nudge),', '\t\tNudge:          base.Nudge,'),
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
