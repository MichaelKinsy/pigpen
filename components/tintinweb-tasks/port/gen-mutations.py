#!/usr/bin/env python3
"""Generates port/mutations.json: one deliberate defect per contract row of port/PORT.md, and checks that every
`find` string is unique in its file (a mutation that does not apply must not pass silently)."""
import json, os, sys

root = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "extensions", "tintinweb-tasks")
M = []


def m(name, file, find, replace):
    M.append({"name": name, "file": file, "find": find, "replace": replace})


# store.go: CRUD, dependencies, the file format and the lock
m("next-id-not-advanced", "store.go", "s.nextID++\n", "")
m("create-status-not-pending", "store.go", "Status: statusPending,\n\t\t\tActiveForm", "Status: statusInProgress,\n\t\t\tActiveForm")
m("blocks-edge-not-bidirectional", "store.go", "other.BlockedBy = append(other.BlockedBy, id)", "_ = other")
m("blockedby-edge-not-bidirectional", "store.go", "other.Blocks = append(other.Blocks, id)", "_ = other")
SELF = 'warnings = append(warnings, "#"+id+" blocks itself")\n\t\t\t\tcase other == nil:\n\t\t\t\t\twarnings = append(warnings, "#"+target+" does not exist")\n\t\t\t\tcase slices.Contains(%s, %s):\n\t\t\t\t\twarnings = append(warnings, "cycle: #"+id+" and #"+target+" block each other")'
for which, (lst, arg) in {"blocks": ("other.Blocks", "id"), "blockedby": ("t.Blocks", "target")}.items():
    find = SELF % (lst, arg)
    m("cycle-warning-text-" + which, "store.go", find, find.replace("block each other", "block"))
    m("self-warning-text-" + which, "store.go", find, find.replace('" blocks itself"', '" blocks itself."'))
    m("dangling-warning-text-" + which, "store.go", find, find.replace('" does not exist"', '" missing"'))
m("duplicate-edges-allowed", "store.go", "if !slices.Contains(t.Blocks, target) {", "if true {")
m("delete-keeps-edges", "store.go", "s.remove(id)\n\t\t\ts.dropEdgesTo(id)\n\t\t\treturn updateResult{ChangedFields: []string{\"deleted\"}", "s.remove(id)\n\t\t\treturn updateResult{ChangedFields: []string{\"deleted\"}")
m("metadata-null-sets", "store.go", "if value == nil {\n\t\t\t\t\tdelete(t.Metadata, key)\n\t\t\t\t} else {\n\t\t\t\t\tt.Metadata[key] = value\n\t\t\t\t}", "t.Metadata[key] = value")
m("clear-completed-keeps-edges", "store.go", "t.Blocks = slices.DeleteFunc(t.Blocks, func(b string) bool { return s.tasks[b] == nil })", "_ = t")
m("update-not-found-has-fields", "store.go", "return updateResult{ChangedFields: changed, Warnings: warnings}\n\t\t}\n\t\tif f.Status", "return updateResult{ChangedFields: []string{\"status\"}, Warnings: warnings}\n\t\t}\n\t\tif f.Status")
m("load-nextid-ignored", "store.go", "n > maxID {\n\t\ts.nextID = int(n)", "n > maxID && false {\n\t\ts.nextID = int(n)")
m("load-no-recompute-max", "store.go", "s.nextID = int(maxID) + 1", "s.nextID = 1")
m("load-keeps-non-task-entries", "store.go", "id, ok := m[\"id\"].(string)\n\t\tif !ok {\n\t\t\tcontinue\n\t\t}", "id, _ := m[\"id\"].(string)")
m("lock-token-not-checked", "store.go", "string(data) == token", "len(data) >= 0")
m("lock-garbage-never-stale", "store.go", "stale := i >= 2", "stale := false")
m("lock-live-pid-reclaimed", "store.go", "stale = !isProcessRunning(pid)", "stale = true")
m("save-not-atomic", "store.go", "return os.Rename(tmp, s.filePath)", "return os.WriteFile(s.filePath, bytes.TrimRight(buf.Bytes(), \"\\n\"), 0o644)")
m("list-no-reload", "store.go", "func (s *taskStore) list(order any) []*task {\n\ts.load()", "func (s *taskStore) list(order any) []*task {")
m("seed-not-noop", "store.go", "if len(s.tasks) > 0 {\n\t\treturn\n\t}\n\twithLock(s, func() struct{}", "if false {\n\t\treturn\n\t}\n\twithLock(s, func() struct{}")

# sort.go, glyphs.go, config.go, paths.go
m("sort-status-default-rank", "sort.go", '"status": {{field: "status", rank: defaultStatusRank}, {field: "id"}},', '"status": {{field: "status", rank: []string{statusPending, statusInProgress, statusCompleted}}, {field: "id"}},')
m("sort-desc-ignored", "sort.go", 'if k.direction == "desc" {\n\t\t\t\t\tdelta = -delta', 'if false {\n\t\t\t\t\tdelta = -delta')
m("sort-unranked-first", "sort.go", "return len(rank)\n}", "return -1\n}")
m("sort-spec-partially-valid", "sort.go", "if !ok {\n\t\t\t\t\treturn sortPresets[\"id\"]\n\t\t\t\t}", "if !ok {\n\t\t\t\t\tcontinue\n\t\t\t\t}")
m("glyph-control-allowed", "glyphs.go", "|| unsafeGlyph.MatchString(s)", "")
m("glyph-summary-literal", "glyphs.go", 'g.CompletedSummary = glyph("completedSummary", g.Completed)', 'g.CompletedSummary = glyph("completedSummary", "✔")')
m("spinner-partial", "glyphs.go", "if !ok {\n\t\t\t\tspinner = nil\n\t\t\t\tbreak\n\t\t\t}", "if !ok {\n\t\t\t\tcontinue\n\t\t\t}")
m("config-project-not-merged", "config.go", "for k, v := range project {\n\t\tmerged[k] = v\n\t}", "")
m("config-glyphs-replaced", "config.go", "if _, g := global[\"glyphs\"]; g || project[\"glyphs\"] != nil {", "if false {")
m("config-save-everything", "config.go", "if differs(g, present, value) {\n\t\t\toverrides[key] = value", "if differs(g, present, value) || true {\n\t\t\toverrides[key] = value")
m("path-session-global-ignores-workspace", "paths.go", "if _, err := os.Stat(inWorkspace); err == nil {\n\t\treturn inWorkspace\n\t}", "")
m("path-key-keeps-colon", "paths.go", "`[/\\\\:]`", "`[/\\\\]`")

# autoclear.go and cadence.go
m("autoclear-delay-off-by-one", "autoclear.go", "currentTurn-turn >= m.delay", "currentTurn-turn > m.delay")
m("autoclear-batch-delay-off-by-one", "autoclear.go", "currentTurn-*m.allCompletedAtTurn >= m.delay", "currentTurn-*m.allCompletedAtTurn > m.delay")
m("autoclear-never-cleared", "autoclear.go", 'if !afterFinishedRun || m.getMode() == "never" {', "if !afterFinishedRun {")
m("autoclear-run-boundary-ignored", "autoclear.go", "if !afterFinishedRun ||", "if (!afterFinishedRun && false) ||")
m("autoclear-stale-entry-kept", "autoclear.go", "case t == nil || t.Status != statusCompleted:", "case t == nil:")
m("autoclear-reset-forgets-run", "autoclear.go", "m.allCompletedAtTurn = nil\n\tm.runEnded = false\n}", "m.allCompletedAtTurn = nil\n}")
m("autoclear-countdown-restarts", "autoclear.go", "if m.allCompletedAtTurn == nil {\n\t\t\tm.allCompletedAtTurn = &currentTurn", "if true {\n\t\t\tm.allCompletedAtTurn = &currentTurn")
m("cadence-no-guard-injected", "cadence.go", "if s.ReminderInjectedThisCycle {\n\t\treturn false\n\t}", "")
m("cadence-no-tasks-still-due", "cadence.go", "if !hasTasks {\n\t\treturn false\n\t}", "")
m("cadence-drain-repeats", "cadence.go", "s.ReminderDue = false\n\ts.ReminderInjectedThisCycle = true", "s.ReminderInjectedThisCycle = true")

# widget.go
m("widget-spinner-advances-on-update", "widget.go", "w.frame++\n\t\t\tw.updateLocked()", "w.frame++\n\t\t\tw.updateLocked()\n\t\t\tw.frame++")
m("widget-overflow-top-bottom-swapped", "widget.go", 'visible = listed[len(listed)-limit:]', 'visible = listed[:limit]')
m("widget-collapse-counts-all", "widget.go", "listed = nil\n\t\tfor _, t := range tasks {\n\t\t\tif t.Status != statusCompleted {\n\t\t\t\tlisted = append(listed, t)\n\t\t\t}\n\t\t}", "")
m("widget-blocked-shows-completed", "widget.go", "b != nil && b.Status != statusCompleted", "b != nil")
m("widget-agent-id-length", "widget.go", "firstUTF16Units(agentID, 5)", "firstUTF16Units(agentID, 6)", ) if False else None
m("widget-duration-hours", "widget.go", 'return fmt.Sprintf("%dh %dm", hr, remMin)', 'return fmt.Sprintf("%dh%dm", hr, remMin)')
m("widget-tokens-keep-zero", "widget.go", 'strings.TrimSuffix(s, ".0") + "k"', 's + "k"')
m("widget-timer-not-stopped", "widget.go", "} else {\n\t\tw.stopTimer()\n\t}\n\tw.registered = true", "}\n\tw.registered = true")
m("widget-empty-not-cleared", "widget.go", 'w.ui.setWidget("tasks", nil)\n\t\t\tw.registered = false\n\t\t}\n\t\tw.stopTimer()', 'w.registered = false\n\t\t}\n\t\tw.stopTimer()')

# extension.go (events, reminders, store resolution) and tools.go
m("reminder-interval", "extension.go", "reminderInterval = 4", "reminderInterval = 5")
m("reminder-active-interval", "extension.go", "activeReminderInterval = 2", "activeReminderInterval = 3")
m("reminder-cap", "extension.go", "reminderMaxTasks = 10", "reminderMaxTasks = 9")
m("reminder-overflow-text", "extension.go", "not shown — use TaskList for the full list.", "not shown - use TaskList for the full list.")
m("reminder-tag-not-stripped", "extension.go", "strings.Trim(reminderTagRe.ReplaceAllString(collapsed, \"\"), jsWhitespaceRe)", "strings.Trim(collapsed, jsWhitespaceRe)")
m("reminder-newline-kept", "extension.go", "collapsed := collapseNewlines(value)", "collapsed := value")
m("reminder-truncated-claims-full", "extension.go", 'header = prefix + " Here are your most relevant tasks (list truncated):"', 'header = prefix + " Here are the latest contents of your task list:"')
m("reminder-completed-first", "extension.go", "case statusInProgress:\n\t\t\t\treturn 0\n\t\t\tcase statusPending:\n\t\t\t\treturn 1\n\t\t\t}\n\t\t\treturn 2", "case statusInProgress:\n\t\t\t\treturn 2\n\t\t\tcase statusPending:\n\t\t\t\treturn 1\n\t\t\t}\n\t\t\treturn 0")
m("reminder-empty-text", "extension.go", "your task list is currently empty", "your task list is empty")
m("reminder-persisted-in-tool-result", "extension.go", 'if taskToolNames[name] {\n\t\tevaluateToolResult(a.cadence, name, false, cfg)\n\t\treturn map[string]any{}, nil\n\t}', 'if false {\n\t\treturn map[string]any{}, nil\n\t}')
m("stale-detection-off", "extension.go", "if gap >= activeReminderInterval {", "if gap >= activeReminderInterval && false {")
m("settled-no-run-boundary", "extension.go", "a.autoClear.onRunEnded()\n\treturn nil, nil\n}\n\n// onTurnEnd", "return nil, nil\n}\n\n// onTurnEnd")
m("resume-keeps-run-open", "extension.go", "if keepsTasks {\n\t\ta.autoClear.onRunEnded()\n\t}", "")
m("fork-not-seeded", "extension.go", "a.store.seed(*forkSeed)", "_ = forkSeed")
m("new-memory-not-cleared", "extension.go", 'if reason == "new" && a.taskScope == "memory" {', 'if false {')
m("switch-keeps-agent-map", "extension.go", "a.agents.clear()\n\t\tresetCadenceState", "resetCadenceState")
m("no-session-file-persists", "extension.go", "if file, err := ctx.SessionManager().GetSessionFile(); err == nil && file != nil && *file != \"\" {", "if _, err := ctx.SessionManager().GetSessionFile(); err == nil {")
m("session-start-no-reload-config", "extension.go", "if reloadConfig || !a.configured || a.configuredCwd != cwd {", "if !a.configured {")
m("startup-keeps-completed-list", "extension.go", "if !isResume && allCompleted(tasks) {", "if false {")
m("reattach-all-statuses", "extension.go", "ok && id != \"\" && t.Status == statusInProgress", "ok && id != \"\"")
m("completed-result-kept-when-absent", "extension.go", 'var result any = data["result"] // absent: the key is dropped, as `result: undefined` is', 'var result any = data["result"]\n\tif result == nil {\n\t\tresult = t.Metadata["result"]\n\t}')
m("failed-keeps-old-result", "extension.go", 'withMeta(t.Metadata, "result", nil, "lastError", lastError)', 'withMeta(t.Metadata, "lastError", lastError)')
m("failed-reverts-to-in-progress", "extension.go", "Status: strPtr(statusPending), Metadata: withMeta(t.Metadata, \"result\", nil, \"lastError\", lastError)", "Status: strPtr(statusInProgress), Metadata: withMeta(t.Metadata, \"result\", nil, \"lastError\", lastError)")
m("stopped-drops-result", "extension.go", "var keep any = t.Metadata[\"result\"]\n\t\tif result != \"\" {", "var keep any = t.Metadata[\"result\"]\n\t\tif false {")
m("cascade-ignores-other-blockers", "extension.go", "if d := a.store.get(dep); d == nil || d.Status != statusCompleted {", "if d := a.store.get(dep); (d == nil || d.Status != statusCompleted) && false {")
m("cascade-without-config", "extension.go", "a.cfg.flag(\"autoCascade\") && a.cascade != nil && a.latest != nil", "a.cfg.flag(\"autoCascade\") && a.latest != nil && false")
m("cascade-drops-model", "extension.go", 'options["model"] = a.cascade.model', "_ = options")
m("prompt-truncation-limit", "extension.go", "if jsLength(res) > 4000 {", "if jsLength(res) > 5000 {")
m("prompt-truncation-marker", "extension.go", "[... truncated — use TaskGet for full output]", "[... truncated]")
m("version-compare", "extension.go", "case *version > protocolVersion:\n\t\ta.pendingWarning = fmt.Sprintf(\"@tintinweb/pi-tasks is outdated", "case *version < protocolVersion:\n\t\ta.pendingWarning = fmt.Sprintf(\"@tintinweb/pi-tasks is outdated")
m("rpc-reply-listener-leaks", "extension.go", "defer unsub()\n\t\tpayload", "_ = unsub\n\t\tpayload")
m("warning-repeats", "extension.go", 'ctx.Notify(a.pendingWarning, "warning")\n\t\ta.pendingWarning = ""', 'ctx.Notify(a.pendingWarning, "warning")')
m("rpc-widget-rows-sent", "extension.go", 'if u.ctx.Mode() == "rpc" {\n\t\treturn\n\t}', "")
m("shutdown-clears-widget", "extension.go", "a.registerTools(e)", 'e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) { a.widget.dispose(); return nil, nil })\n\ta.registerTools(e)')
m("tasklist-order-by-id", "tools.go", "if d := order[x.Status] - order[y.Status]; d != 0 {\n\t\t\treturn d\n\t\t}", "if d := order[x.Status] - order[y.Status]; d != 0 && false {\n\t\t\treturn d\n\t\t}")
m("tasklist-shows-completed-blockers", "tools.go", "// Only non-completed blockers are shown.\n\t\tif open := a.openBlockers(t, false); len(open) > 0 {", "// Only non-completed blockers are shown.\n\t\tif open := append([]string{}, t.BlockedBy...); len(open) > 0 {\n\t\t\tfor i := range open {\n\t\t\t\topen[i] = \"#\" + open[i]\n\t\t\t}")
m("taskget-no-unescape", "tools.go", 'strings.ReplaceAll(t.Description, `\\n`, "\\n")', "t.Description")
m("taskget-metadata-hidden", "tools.go", "if len(t.Metadata) > 0 {", "if false {")
m("taskupdate-not-found-text", "tools.go", '"Task #%s not found"', '"Task %s not found"')
m("taskupdate-warning-format", "tools.go", 'msg += " (warning: " + strings.Join(res.Warnings, "; ") + ")"', 'msg += " (warning: " + strings.Join(res.Warnings, ", ") + ")"')
m("taskupdate-no-batch-reset", "tools.go", "case statusPending:\n\t\ta.autoClear.resetBatchCountdown()", "case statusPending:")
m("taskcreate-no-new-batch", "tools.go", "a.autoClear.startNewBatch()\n\tmeta", "meta")
m("taskcreate-agenttype-lost", "tools.go", 'meta["agentType"] = at', "_ = at")
m("taskoutput-empty-id-accepted", "tools.go", 'if taskID == "" {\n\t\treturn "", errors.New("task_id is required")\n\t}', "")
m("taskoutput-error-text", "tools.go", '"Error: " + le', '"Error " + le')
m("taskoutput-consumes-running", "tools.go", "if !a.agents.has(agentID) && updated.Status != statusInProgress {", "if true {")
m("taskoutput-stale-snapshot", "tools.go", "updated := a.store.get(resolved)\n\tif updated == nil {\n\t\tupdated = t\n\t}", "updated := t")
m("taskoutput-prefix-match-lost", "extension.go", "if k == id || strings.HasPrefix(k, id) {", "if k == id {")
m("taskstop-stale-id", "tools.go", "a.store.update(resolved, updateFields{Status: strPtr(statusCompleted)})", "a.store.update(taskID, updateFields{Status: strPtr(statusCompleted)})")
m("taskstop-completed-restopped", "tools.go", "if t != nil && t.Status == statusInProgress {\n\t\t\tif agentID", "if t != nil {\n\t\t\tif agentID")
m("taskstop-shell-id-ignored", "tools.go", 'taskID, ok = strParam(p, "shell_id")', "")
m("taskexecute-no-blocker-check", "tools.go", "if open := a.openBlockers(t, true); len(open) > 0 {\n\t\t\tresults", "if open := a.openBlockers(t, false); len(open) > 0 {\n\t\t\tresults")
m("taskexecute-pending-check", "tools.go", "if t.Status != statusPending {", "if t.Status == statusCompleted {")
m("taskexecute-launch-text", "tools.go", "Do not spawn additional agents for these tasks.", "Do not spawn more agents for these tasks.")
m("named-list-under-home", "store.go", 'path = filepath.Join(agentDir(), "tasks", listIDOrPath+".json")', 'path = filepath.Join(os.Getenv("HOME"), ".pi", "tasks", listIDOrPath+".json")')
m("ping-while-loading", "extension.go", "a.pingOnce.Do(func() { go a.ping(ctx.Events()) })", "go a.ping(ctx.Events())")
m("taskexecute-no-reping", "tools.go", "a.unlocked(func() { a.ping(ctx.Events()) })", "")
m("taskexecute-owner-not-set", "tools.go", "a.store.update(taskID, updateFields{Owner: strPtr(agentID), Metadata: withMeta(t.Metadata, \"agentId\", agentID)})", "a.store.update(taskID, updateFields{Metadata: withMeta(t.Metadata, \"agentId\", agentID)})")

# command.go
m("command-clear-all-keeps-file", "command.go", "a.store.clearAll()\n\t\t\t\tif a.isSessionScope() {\n\t\t\t\t\ta.deleteSessionFileIfEmpty()\n\t\t\t\t}", "a.store.clearAll()")
m("command-row-parsed-from-label", "command.go", "if i := slices.Index(choices, selected); i >= 0 && i < len(tasks) {\n\t\t\treturn viewTaskDetail(tasks[i].ID)\n\t\t}", "for k := 0; k < len(selected); k++ {\n\t\t\tif selected[k] == '#' {\n\t\t\t\te := k + 1\n\t\t\t\tfor e < len(selected) && selected[e] >= '0' && selected[e] <= '9' {\n\t\t\t\t\te++\n\t\t\t\t}\n\t\t\t\treturn viewTaskDetail(selected[k+1 : e])\n\t\t\t}\n\t\t}")
m("command-complete-no-track", "command.go", "a.store.update(taskID, updateFields{Status: strPtr(statusCompleted)})\n\t\t\t\t\ta.autoClear.trackCompletion(taskID, a.cadence.CurrentTurn)", "a.store.update(taskID, updateFields{Status: strPtr(statusCompleted)})")
m("command-menu-no-clear-completed", "command.go", "if completed > 0 {\n\t\t\tchoices", "if false {\n\t\t\tchoices")
m("command-back-option", "command.go", 'actions = append(actions, "✗ Delete", "← Back")', 'actions = append(actions, "✗ Delete")')

# validation
by_file = {}
for x in M:
    by_file.setdefault(x["file"], open(os.path.join(root, x["file"])).read())
bad = 0
for x in M:
    n = by_file[x["file"]].count(x["find"])
    if n != 1:
        print("find not unique/absent (%d): %s in %s" % (n, x["name"], x["file"]), file=sys.stderr)
        bad += 1
if bad:
    sys.exit(1)
with open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "mutations.json"), "w") as f:
    json.dump(M, f, indent=1, ensure_ascii=False)
    f.write("\n")
print(len(M), "mutations")
