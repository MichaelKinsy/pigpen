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
 # Context saver (saver.go, dedupe.go, excerpt.go, filter.go, relevance.go, output_context.go, tool_output.go, context_wiring.go, context_compaction.go).
 ("ledger-recall-counted-twice", "saver.go", "\t\tif !state.recalled {\n\t\t\tstate.recalled = true", "\t\tif true {\n\t\t\tstate.recalled = true"),
 ("ledger-whole-file-recall-keeps-saving", "saver.go", "if kind == RecallFull && !state.restored {", "if false && !state.restored {"),
 ("ledger-duplicate-bytes-lost", "saver.go", "\tl.duplicates++\n\tl.bytesSaved += bytesSaved", "\tl.duplicates++"),
 ("ledger-turn-end-not-counted", "saver.go", "\tl.turns++\n", ""),
 ("ledger-windows-path-json-form-missed", "saver.go", "return strings.Contains(text, quoted[1:len(quoted)-1])", "return strings.Contains(text, quoted[1:len(quoted)-1]) && quoted == \"\""),
 ("dedupe-tail-cut", "dedupe.go", "cutoff := utf16Len(text) - TailKeepChars", "cutoff := utf16Len(text)"),
 ("dedupe-short-runs-cut", "dedupe.go", "RunMinLines = 20", "RunMinLines = 5"),
 ("dedupe-trailing-space-significant", "dedupe.go", "normalized := jsTrimEnd(line)", "normalized := line"),
 ("dedupe-pointer-wording", "dedupe.go", "omitted; full text: %s]", "omitted; text: %s]"),
 ("excerpt-summary-tail-lost", "excerpt.go", "if format != FormatGitDiff && format != FormatGitLog && len(tail) > 0 {", "if false {"),
 ("excerpt-cap-ignored", "excerpt.go", "if size+utf16Len(clipped)+1 > formatExcerptLimit-400 {", "if false {"),
 ("filter-tail-dropped", "filter.go", "FilterTailChars = 1000", "FilterTailChars = 0"),
 ("filter-threshold-exclusive", "filter.go", "if scores[i] >= minScore && own(i) > 0 {", "if scores[i] > minScore && own(i) > 0 {"),
 ("filter-failed-batch-ignored", "filter.go", "\t\tif result.OK {\n\t\t\tcontinue\n\t\t}", "\t\tif true {\n\t\t\tcontinue\n\t\t}"),
 ("filter-chunks-not-redacted", "filter.go", "field := Redact(chunk)", "field := chunk"),
 ("filter-question-limit-ignored", "filter.go", "len(batch.IDs) < FilterMaxQuestions", "true"),
 ("relevance-flagged-result-kept", "relevance.go", "u.Flagged = strings.Contains(result, injectionBanner)", "u.Flagged = false"),
 ("relevance-reserve-ignored", "relevance.go", "left-len(requests) < RequestReserve", "left-len(requests) < 0"),
 ("relevance-wrapper-not-escaped", "relevance.go", "safe := escapeWrapperTags(body)", "safe := body"),
 ("relevance-fence-not-longer", "relevance.go", "ticks := strings.Repeat(\"`\", max(3, longest+1))", "ticks := \"```\""),
 ("relevance-labels-not-redacted", "relevance.go", "label := relClip(u.Label, labelChars)", "label := u.Label"),
 ("relevance-threshold-never-raised", "relevance.go", "if raised > threshold {", "if false {"),
 ("relevance-unbounded-requests", "relevance.go", "if cap := opts.Config.MaxRequests; len(requests) > cap {", "if cap := opts.Config.MaxRequests; false && len(requests) > cap {"),
 ("compress-all-compresses", "output_context.go", "if retention == RetentionAll {", "if false {"),
 ("compress-confidence-ignored", "output_context.go", "if confidence >= cfg.Confidence && (", "if (")
 ,("output-key-keeps-colour", "output_context.go", "reANSI.ReplaceAllString(text, \"\")", "text"),
 ("stored-output-world-readable", "output_context.go", "os.O_EXCL, 0o600)", "os.O_EXCL, 0o644)"),
 ("learner-never-forgets-recalled", "output_context.go", "\t\tif o.Recalled {\n\t\t\tc.Recalled++\n\t\t}", ""),
 ("duplicate-is-judged", "tool_output.go", "case hasEarlier || recallRead:", "case recallRead:"),
 ("multi-block-flattened", "tool_output.go", "multiBlock := len(textBlocks) > 1 && !hasEarlier && !recallRead", "multiBlock := false"),
 ("banner-only-on-first-block", "tool_output.go", "if index == len(textBlocks) {", "if false {"),
 ("masking-skipped", "tool_output.go", "\tif maskOutput {\n\t\tfor _, raw := range rawTexts {", "\tif false {\n\t\tfor _, raw := range rawTexts {"),
 ("secret-announced-every-time", "tool_output.go", "\tfor _, id := range unseenSecrets {\n\t\tst.secretsSeen[id] = true\n\t}", ""),
 ("dedupe-runs-switch-ignored", "tool_output.go", "cfg.Context.DedupeRuns && !hasEarlier", "!hasEarlier"),
 ("message-dedupe-default-on", "context_wiring.go", "!cfg.Context.DedupeRuns || !cfg.Context.DedupeMessages", "!cfg.Context.DedupeRuns"),
 ("turn-end-ledger-lost", "context_wiring.go", "\tst.ledger.TurnEnd()\n", ""),
 ("recall-not-recorded", "context_wiring.go", "recalled, ok := st.ledger.NoteAccess(serialized, kind)", "recalled, ok := \"\", false"),
 ("compaction-skip-providers-ignored", "context_compaction.go", "slices.Contains(cfg.Compaction.SkipProviders, provider)", "slices.Contains(cfg.Compaction.SkipProviders, provider+\"-\")"),
 ("compaction-cut-point-lost", "context_compaction.go", '"firstKeptEntryId": asString(preparation["firstKeptEntryId"]),', '"firstKeptEntryId": "",'),
 ("compaction-judge-error-silent", "context_compaction.go", "case FallbackReasonJudgeError:\n\t\t\tdetail := result.Detail", "case \"never\":\n\t\t\tdetail := result.Detail"),
]
EQUIVALENT = {"judge-when-disabled": "unreachable: toolcall.go returns before judgeFor when warden is off; the check stays as defence in depth",
              "banner-only-on-first-block": "a notice and several text blocks never meet outside the per-block path: more than one block means judged per block, and a block that is not judged (a duplicate or a recall) has no verdict, so no notice"}
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
        r = subprocess.run(["go", "test", "-count=1", "-failfast"] + (["-run", os.environ["MUTATE_RUN"]] if os.environ.get("MUTATE_RUN") else []) + ["."], cwd=ext, capture_output=True, text=True, timeout=600)
        killed = r.returncode != 0
        first = next((l for l in (r.stdout + r.stderr).splitlines() if l.startswith("--- FAIL") or "build failed" in l or "[setup failed]" in l), "")
        out.append(f"{mid}: {'KILLED' if killed else 'SURVIVED (equivalent mutant: ' + EQUIVALENT[mid] + ')' if mid in EQUIVALENT else 'SURVIVED'} {first}".rstrip())
    finally:
        shutil.move(p + ".orig", p)
    print(out[-1], flush=True)
open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "mutation-results.txt"), "w").write("\n".join(out) + "\n")
