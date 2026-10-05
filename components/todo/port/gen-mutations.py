#!/usr/bin/env python3
"""Generates port/mutations.json: one deliberate defect per contract row. Each `find` must occur exactly
once in its file (checked here), so a mutation never silently applies to the wrong place."""
import json, os, sys

root = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "extensions", "rpiv-todo")
M = []


def m(name, file, find, replace):
    M.append({"name": name, "file": file, "find": find, "replace": replace})


# reducer.go
m("subject-trim-is-go", "reducer.go", 'if jsTrim(subject) == "" {', 'if subject == "" {')
m("create-error-text", "reducer.go", '"subject required for create"', '"subject required for creat"')
m("dangling-blockedby-text", "reducer.go", '"blockedBy: #%s not found"', '"blockedBy: #%s missing"')
m("deleted-blockedby-text", "reducer.go", '"blockedBy: #%s is deleted"', '"blockedBy: #%s deleted"')
m("next-id-not-advanced", "reducer.go", 'NextID: state.NextID + 1}', 'NextID: state.NextID}')
m("created-status", "reducer.go", 'Subject: subject, Status: statusPending}', 'Subject: subject, Status: statusInProgress}')
m("create-keeps-empty-description", "reducer.go", 'truthyString(p, "description"); ok {', 'text(p, "description"); ok {')
m("update-needs-id-text", "reducer.go", '"id required for update"', '"id required for updat"')
m("owner-is-not-a-mutation", "reducer.go", 'present(p, "status") || present(p, "owner") ||', 'present(p, "status") ||')
m("transition-unchecked", "reducer.go", 'if !isTransitionValid(current.Status, to) {', 'if false {')
m("self-block-allowed", "reducer.go", 'if dep == float64(current.ID) {', 'if false {')
m("cycle-unchecked", "reducer.go", 'if detectCycle(state.Tasks, current.ID, newBlockedBy) {', 'if false {')
m("changed-ignores-subject", "reducer.go", 'before.Subject != after.Subject ||', 'false ||')
m("changed-ignores-blockedby", "reducer.go", '!sameNumberList(before.BlockedBy, after.BlockedBy) ||', 'false ||')
m("changed-ignores-metadata", "reducer.go", '!sameRecord(before.Metadata, after.Metadata)', 'false')
m("double-delete-allowed", "reducer.go", 'if current.Status == statusDeleted {', 'if false {')
m("clear-keeps-next-id", "reducer.go", 'taskState{Tasks: []task{}, NextID: 1}, Op: op{Kind: "clear"', 'taskState{Tasks: []task{}, NextID: state.NextID}, Op: op{Kind: "clear"')
m("metadata-null-keeps-key", "reducer.go", 'delete(merged, k)', 'merged[k] = v')
m("metadata-empty-kept", "reducer.go", 'if len(merged) > 0 {', 'if true {')
m("completed-may-restart", "reducer.go", 'statusCompleted:  {statusDeleted},', 'statusCompleted:  {statusDeleted, statusInProgress},')
m("same-status-invalid", "reducer.go", 'if from == to {\n\t\treturn true', 'if false {\n\t\treturn true')
m("cycle-merge-ignores-new", "reducer.go", 'if !slices.Contains(merged, d) {', 'if false {')
m("blocks-inverted-wrong", "reducer.go", 'blocks[int(dep)] = append(blocks[int(dep)], t.ID)', 'blocks[int(dep)] = append(blocks[int(dep)], int(dep))')
m("list-ignores-include-deleted", "reducer.go", 'IncludeDeleted: p["includeDeleted"] == true', 'IncludeDeleted: false')
m("remove-before-add-lost", "reducer.go", 'newBlockedBy = slices.DeleteFunc(newBlockedBy, func(dep float64) bool { return slices.Contains(remove, dep) })', 'newBlockedBy = slices.Clone(newBlockedBy)')
m("js-number-exponent", "jsutil.go", "return strconv.FormatFloat(f, 'f', -1, 64)", "return strconv.FormatFloat(f, 'e', -1, 64)")
m("js-number-zero-padded-exponent", "jsutil.go", 'return mantissa + "e" + exp[:1] + strings.TrimLeft(exp[1:], "0")', 'return mantissa + "e" + exp')
m("js-number-fixed-floor", "jsutil.go", 'a >= 1e-6 && a < 1e21', 'a >= 1e-7 && a < 1e21')
m("js-number-negative-zero", "jsutil.go", 'case f == 0:', 'case f == 0 && !math.Signbit(f):')
m("js-trim-is-go", "jsutil.go", 'func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }', 'func jsTrim(s string) string { return strings.TrimSpace(s) }')

# envelope.go
m("created-text", "envelope.go", '"Created #%d: %s (pending)"', '"Created #%d: %s"')
m("transition-arrow", "envelope.go", '" (%s → %s)"', '" (%s -> %s)"')
m("no-change-text", "envelope.go", 'already matches the requested values (status: %s)"', 'already matches the requested value (status: %s)"')
m("cleared-text", "envelope.go", '"Cleared %d tasks"', '"Cleared %d task"')
m("no-tasks-text", "envelope.go", 'return "No tasks"', 'return "No task"')
m("blocks-line-text", "envelope.go", '"  blocks: "', '"  blocks:"')
m("list-chain-suffix", "envelope.go", 'block = " ⛓ " + idList(t.BlockedBy, ",")', 'block = " ⛓" + idList(t.BlockedBy, ",")')
m("error-not-in-details", "envelope.go", 'd.Error = o.Message', '_ = o.Message')
m("active-form-line", "envelope.go", '"  activeForm: "', '"  active: "')
m("list-subject-unsanitised", "envelope.go", 'sanitizeTerminalText(t.Subject), form, block)', 't.Subject, form, block)')
m("details-field-name", "envelope.go", '"nextId":%d', '"nextid":%d')
m("details-error-key", "envelope.go", ',"error":%s', ',"err":%s')
m("task-json-order", "envelope.go", 'field("subject", t.Subject)\n\tif t.Description != nil {', 'if t.Description != nil {\n\t\tfield("description", *t.Description)\n\t}\n\tfield("subject", t.Subject)\n\tif false {')
m("get-description-line", "envelope.go", '"  description: "', '"  desc: "')

# sanitize.go
m("c1-csi-kept", "sanitize.go", '"(?:\\u001b\\\\[|\\u009b)[0-?]*[ -/]*[@-~]"', '"(?:\\u001b\\\\[)[0-?]*[ -/]*[@-~]"')
m("bidi-overrides-kept", "sanitize.go", '"[\\u200e\\u200f\\u202a-\\u202e\\u2066-\\u2069]"', '"[\\u200e\\u200f]"')
m("tab-removed", "sanitize.go", 'if c == "\\n" || c == "\\r" || c == "\\t" {', 'if c == "\\n" || c == "\\r" {')
m("separators-kept", "sanitize.go", 'value = separatorPattern.ReplaceAllString(value, " ")', '_ = separatorPattern')
m("esc-eats-line-terminator", "sanitize.go", '"\\u001b[^\\n\\r\\u2028\\u2029]"', '"(?s)\\u001b."')

# store.go / replay.go
m("replay-any-tool", "replay.go", 'msg["role"] != "toolResult" || msg["toolName"] != toolName {', 'msg["role"] != "toolResult" {')
m("evict-keeps-slot", "store.go", 'delete(s.sessions, sessionID)', '_ = sessionID')
m("replay-first-wins", "replay.go", 'result = taskState{Tasks: tasks, NextID: int(next)}', 'if len(result.Tasks) == 0 {\n\t\t\tresult = taskState{Tasks: tasks, NextID: int(next)}\n\t\t}')
m("replay-corrupt-accepted", "replay.go", '_, tasksOK := m["tasks"].([]any)', 'tasksOK := true')

# config.go
m("floor-is-two", "config.go", 'ok && n >= 3 {', 'ok && n >= 2 {')
m("default-key", "config.go", 'defaultCollapseKey = "ctrl+shift+t"', 'defaultCollapseKey = "ctrl+shift+y"')
m("key-not-lowercased", "config.go", 'raw = strings.ToLower(jsTrim(raw))', 'raw = jsTrim(raw)')
m("duplicate-modifier-ok", "config.go", 'if seen[m] || !modifierKeys[m] {', 'if !modifierKeys[m] {')
m("xdg-relative-accepted", "config.go", 'if filepath.IsAbs(xdg) {', 'if true {')
m("legacy-wins", "config.go", 'if fileExists(xdgPath) {', 'if false {')

# view.go
m("create-glyph", "view.go", '"create": "+"', '"create": "*"')
m("id-colour", "view.go", 'theme.Fg("dim", "#"+strconv.Itoa(t.ID))', 'theme.Fg("muted", "#"+strconv.Itoa(t.ID))')
m("deleted-glyph", "view.go", 'statusDeleted: "⊘"}', 'statusDeleted: "x"}')
m("update-result-status-param", "view.go", 'if s, ok := text(details.Params, "status"); ok && s != "" {', 'if s, ok := text(details.Params, "status"); ok && s == "zzz" {')
m("completed-not-struck", "view.go", 'if t.Status == statusCompleted || t.Status == statusDeleted {\n\t\tsubject = theme.Strikethrough(subject)', 'if t.Status == statusDeleted {\n\t\tsubject = theme.Strikethrough(subject)')
m("command-line-blocked-by", "view.go", 'block = "    ⛓ " + idList(t.BlockedBy, ",")', 'block = "  ⛓ " + idList(t.BlockedBy, ",")')

# overlay.go
m("last-row-glyph", "overlay.go", 'strings.Replace(lines[last], "├─", "└─", 1)', 'strings.Replace(lines[last], "├─", "└─", 0)')
m("hide-never-hides", "overlay.go", 'o.hidden[id] = true', '_ = id')
m("expand-hint-text", "overlay.go", '"{key} to expand"', '"{key} to open"')
m("overflow-summary-brackets", "overlay.go", '"+%d %s (%s)"', '"+%d %s [%s]"')
m("collapse-ignored", "overlay.go", 'if o.collapsed {\n\t\thint', 'if false {\n\t\thint')
m("no-trailing-spacer", "overlay.go", 'return append(lines, "")', 'return lines')
m("heading-counts-pending", "overlay.go", 'counts.Completed, counts.Total)', 'counts.Pending, counts.Total)')
m("budget-off-by-one", "overlay.go", 'budget := maxLines - 1', 'budget := maxLines')
m("expanded-ignored", "overlay.go", 'if env.toolsExpanded {', 'if false {')
m("empty-render-kept", "overlay.go", 'if len(lines) == 0 {\n\t\tlines = nil\n\t}', 'if len(lines) == 0 {\n\t\tlines = []string{}\n\t}')
m("nextid-reset-lost", "overlay.go", 'if o.lastNextIDKnown && state.NextID < o.lastNextID {', 'if false {')
m("collapse-spacer-lost", "overlay.go", 'return withTrailingSpacer([]string{heading, truncate(theme.Fg("dim", "└─") + " " + theme.Fg("dim", hint))})', 'return []string{heading, truncate(theme.Fg("dim", "└─") + " " + theme.Fg("dim", hint))}')

# text.go
m("truncate-no-reset", "text.go", 'return result.String() + reset + ellipsis + reset', 'return result.String() + ellipsis')
m("width-counts-ansi", "text.go", 'if n := ansiAt(s, i); n > 0 {\n\t\t\ti += n\n\t\t\tcontinue\n\t\t}\n\t\tr, size := utf8.DecodeRuneInString(s[i:])\n\t\tw += runeWidth(r)', 'r, size := utf8.DecodeRuneInString(s[i:])\n\t\tw += runeWidth(r)')

# extension.go
m("tool-name", "extension.go", 'name != toolName {', 'name == toolName {')
m("tool-error-refreshes", "extension.go", 'if isErr, _ := data["isError"].(bool); isErr {', 'if false {')
m("commit-order-lost", "extension.go", '\twait()\n\tr := applyTaskMutation', '\t_ = wait\n\tr := applyTaskMutation')
m("end-order-lost", "extension.go", 'a.hold(callID, release)', '_ = callID')
m("end-order-released-at-return", "extension.go", 'a.hold(callID, release)', 'release()')
m("shutdown-keeps-slot", "extension.go", 'a.st.evictSession(id)', '_ = id')
m("foreground-never-disposed", "extension.go", 'if id != "" && id != a.st.getActiveRenderSession() {', 'if id != "" {')
m("child-claims-foreground", "extension.go", 'if a.st.getActiveRenderSession() == "" {\n\t\ta.st.setActiveRenderSession(id)', 'if true {\n\t\ta.st.setActiveRenderSession(id)')
m("child-rebinds-overlay", "extension.go", 'if id != a.st.getActiveRenderSession() {\n\t\treturn nil, nil', 'if false {\n\t\treturn nil, nil')
m("pointer-kept-after-shutdown", "extension.go", 'a.st.clearActiveRenderSession()\n\treturn nil, derr', 'return nil, derr')
m("dispose-error-lost", "extension.go", 'return nil, derr', '_ = derr\n\treturn nil, nil')
m("rpc-draws-widget", "extension.go", 'if lines != nil {\n\t\treturn nil\n\t}', 'if false {\n\t\treturn nil\n\t}')
m("stale-error-text", "extension.go", '"stale after session replacement"', '"zzz after session replacement"')
m("agent-start-no-hide", "extension.go", 'ov.hideCompletedTasksFromPreviousTurn(envFor(ctx))', '_ = ov')
m("command-error-level", "extension.go", 'errRequiresInteractive), "error")', 'errRequiresInteractive), "warning")')
m("command-header-separator", "extension.go", 'strings.Join(header, " · ")', 'strings.Join(header, " - ")')
m("shortcut-always", "extension.go", 'key != collapseKeyOff {', 'true {')
m("guideline-char", "extension.go", 'Skip it for single trivial tasks', 'Skip it for single trivial task')
m("description-char", "extension.go", 'Use this to plan and track multi-step work', 'Use this to plan and track multi-step wor')
m("snippet-char", "extension.go", '"Manage a task list to track multi-step progress"', '"Manage a task list to track multi step progress"')
m("action-not-required", "extension.go", '"required": []any{"action"},', '"required": []any{},')
m("widget-placement", "extension.go", '"placement": "aboveEditor"', '"placement": "belowEditor"')
m("metadata-schema", "extension.go", '"patternProperties": map[string]any{"^.*$": map[string]any{}},', '"additionalProperties": true,')
m("sid-error-swallowed", "extension.go", 'id, err := sid(ctx.SessionManager())\n\tif err != nil {\n\t\treturn nil, err\n\t}\n\taction', 'id, _ := sid(ctx.SessionManager())\n\taction')
m("rowcap-lost", "extension.go", 'a.ov = newOverlay(a.st, hostWidgetRows-1)', 'a.ov = newOverlay(a.st, 0)')
m("compact-replay-lost", "extension.go", 'e.OnEvent(sdk.EventSessionCompact, a.replayAndRefresh)', '_ = a.replayAndRefresh')

bad = 0
for x in M:
    src = open(os.path.join(root, x["file"])).read()
    n = src.count(x["find"])
    if n != 1:
        print(f'{x["name"]}: find occurs {n} times in {x["file"]}', file=sys.stderr)
        bad += 1
if bad:
    sys.exit(1)
json.dump(M, open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "mutations.json"), "w"), indent=1, ensure_ascii=False)
open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "mutations.json"), "a").write("\n")
print(len(M), "mutations")
